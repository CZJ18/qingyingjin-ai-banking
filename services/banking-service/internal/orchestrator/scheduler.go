package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/qingyingjin/banking-service/internal/billing"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/skills"
	"gorm.io/gorm"
	"log/slog"
	"sort"
	"time"
)

func sortReady(tasks []AgentTask) {
	sort.SliceStable(tasks, func(i, j int) bool {
		if tasks[i].Priority != tasks[j].Priority {
			return tasks[i].Priority > tasks[j].Priority
		}
		if tasks[i].CreatedAt != tasks[j].CreatedAt {
			return tasks[i].CreatedAt < tasks[j].CreatedAt
		}
		return tasks[i].ID < tasks[j].ID
	})
}

type completion struct {
	task     AgentTask
	output   AgentOutput
	err      error
	finished int64
}

// ProcessOne leases one bounded wave. External calls never hold SQL locks.
func (s *Service) ProcessOne(ctx context.Context) (bool, error) {
	j, tasks, e := s.repo.claim(ctx)
	if e != nil || j.ID == "" {
		return false, e
	}
	wave, cancel := context.WithCancel(ctx)
	s.mu.Lock()
	s.active[j.ID] = activeWave{token: j.LeaseToken, cancel: cancel}
	s.mu.Unlock()
	defer func() { cancel(); s.releaseWave(j.ID, j.LeaseToken) }()
	v, e := s.repo.Get(ctx, j.UserID, j.ID)
	if e != nil {
		return true, e
	}
	if v.Job.Status != "running" || v.Job.LeaseToken != j.LeaseToken {
		return true, nil
	}
	results := make(chan completion, len(tasks))
	for _, task := range tasks {
		task := task
		call := Call{SkillID: task.SkillID, SkillVersion: task.SkillVersion, Mode: task.Mode, Meta: metaJob(j, task), JobID: j.ID, TaskID: task.ID, AgentType: task.AgentType, Tool: task.Tool, Parameters: task.Parameters, Dependencies: []AgentOutput{}, UseCoze: j.UseCoze, Confirmed: task.Confirmation == "approve", InvocationKey: "orchestrator:" + task.ID, LeaseToken: j.LeaseToken}
		for _, d := range v.Dependencies {
			if d.TaskID == task.ID {
				for _, up := range v.Tasks {
					if up.ID == d.DependsOn {
						var out AgentOutput
						if json.Unmarshal(up.Output, &out) == nil {
							call.Dependencies = append(call.Dependencies, out)
						}
					}
				}
			}
		}
		go func() {
			limited, stop := context.WithTimeout(wave, time.Duration(j.TimeoutMS)*time.Millisecond)
			defer stop()
			answer := make(chan completion, 1)
			go func() {
				var out AgentOutput
				var err error
				_, err = skills.Resolve(skills.Request{SkillID: call.SkillID, SkillVersion: call.SkillVersion, Agent: call.AgentType, Tool: call.Tool, Mode: call.Mode})
				if err != nil {
					answer <- completion{task, out, err, time.Now().UnixMilli()}
					return
				}
				switch call.Tool {
				case "plan_instruction":
					out, err = s.understand(limited, call)
				case "request_confirmation":
					if !call.Confirmed {
						err = entity.Fail("AUTHORIZATION_REQUIRED", "explicit confirmation required")
					} else {
						out = AgentOutput{TaskID: call.TaskID, AgentType: call.AgentType, Status: "succeeded", Result: json.RawMessage(`{"decision":"approve"}`), Evidence: []Evidence{}, Errors: []TaskError{}, NextActions: []NextAction{}}
					}
				default:
					if s.runner == nil {
						err = entity.Fail("DEPENDENCY_UNAVAILABLE", "executor unavailable")
					} else {
						out, err = s.runner.Run(limited, call)
					}
				}
				answer <- completion{task, out, err, time.Now().UnixMilli()}
			}()
			select {
			case c := <-answer:
				if c.err == nil {
					c.err = ValidateOutput(call, c.output)
				}
				results <- c
			case <-limited.Done():
				results <- completion{task: task, err: limited.Err(), finished: time.Now().UnixMilli()}
			}
		}()
	}
	done := make([]completion, 0, len(tasks))
	for range tasks {
		done = append(done, <-results)
	}
	// Cancelled callers still need a bounded durable fence/checkpoint operation.
	persist, stop := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer stop()
	e = s.repo.tx(persist, j.UserID, func(tx *gorm.DB) error {
		current, e := owned(tx, j.UserID, j.ID, true)
		if e != nil {
			return e
		}
		if current.Status != "running" || current.LeaseToken != j.LeaseToken {
			return nil
		}
		for _, c := range done {
			t := c.task
			code := entity.Code(c.err)
			if errors.Is(c.err, context.DeadlineExceeded) {
				code = "TASK_TIMEOUT"
			}
			if errors.Is(c.err, context.Canceled) {
				code = "INTERRUPTED"
			}
			status := "succeeded"
			retry := false
			if c.err != nil {
				status = "failed"
				switch code {
				case "GATEWAY_UNAVAILABLE":
					status = "awaiting_gateway"
				case "INPUT_REQUIRED":
					status = "awaiting_input"
				case "INTERRUPTED":
					if t.Mode != "read_only" {
						status = "review_required"
					}
				case "TASK_TIMEOUT":
					if t.Mode != "read_only" {
						status = "review_required"
					}
				}
				retry = t.Mode == "read_only" && t.Attempts < j.MaxAttempts && (code == "TASK_TIMEOUT" || code == "COZE_UNAVAILABLE" || code == "DEPENDENCY_UNAVAILABLE" || code == "INTERRUPTED")
				if retry {
					status = "pending"
					t.RetryAt = time.Now().UnixMilli() + int64(100*t.Attempts)
				}
				c.output = AgentOutput{TaskID: t.ID, AgentType: t.AgentType, Status: "failed", Result: json.RawMessage(`{}`), Evidence: []Evidence{}, Errors: []TaskError{{Code: code, Retryable: retry}}, NextActions: []NextAction{}, WorkflowExecuteID: c.output.WorkflowExecuteID}
			}
			if c.err == nil && t.Tool == "plan_instruction" {
				var compiled struct {
					Tasks []TaskSpec `json:"tasks"`
				}
				if e = json.Unmarshal(c.output.Result, &compiled); e != nil {
					return e
				}
				if e = insertTasks(tx, current, compiled.Tasks, t.ID); e != nil {
					return e
				}
			}
			c.output.SkillID, c.output.SkillVersion, c.output.RuleVersion = t.SkillID, t.SkillVersion, "1"
			t.Status = status
			t.ErrorCode = code
			t.Output, _ = json.Marshal(c.output)
			if e = tx.Save(&t).Error; e != nil {
				return e
			}
			var ex AgentExecution
			if e = tx.Where("task_id=? AND attempt=? AND lease_token=?", t.ID, t.Attempts, j.LeaseToken).First(&ex).Error; e != nil {
				return e
			}
			ex.Status = c.output.Status
			ex.Output = t.Output
			ex.ErrorCode = code
			ex.FinishedAt = c.finished
			ex.DurationMS = c.finished - ex.StartedAt
			ex.WorkflowExecuteID = c.output.WorkflowExecuteID
			if e = tx.Save(&ex).Error; e != nil {
				return e
			}
			if e = tx.Model(&ToolInvocation{}).Where("execution_id=?", ex.ID).Updates(map[string]any{"status": ex.Status, "finished_at": ex.FinishedAt, "duration_ms": ex.DurationMS, "error_code": code}).Error; e != nil {
				return e
			}
			if e = trace(tx, current, t.ID, "completed", status, code); e != nil {
				return e
			}
			if c.err == nil {
				if e = dynamicVerification(tx, current, t, c.output); e != nil {
					return e
				}
			}
		}
		return recompute(tx, &current)
	})
	return true, e
}

// Only evidence-backed duplicate billing results can add a verification stage.
// It never inserts a cancellation or a financial execution task.
func dynamicVerification(tx *gorm.DB, j Job, t AgentTask, out AgentOutput) error {
	if t.AgentType != "billing" {
		return nil
	}
	verify := false
	for _, a := range out.NextActions {
		if a.Type == "verify_subscriptions" {
			verify = true
		}
	}
	if !verify {
		return nil
	}
	var report billing.Report
	if json.Unmarshal(out.Result, &report) != nil {
		return nil
	}
	evidence := false
	for _, a := range report.Anomalies {
		if a.Type == "possible_duplicate" && len(a.Evidence) > 0 {
			evidence = true
		}
	}
	if !evidence {
		return nil
	}
	var count int64
	if e := tx.Model(&AgentTask{}).Where("job_id=? AND task_key='auto_verify_subscriptions'", j.ID).Count(&count).Error; e != nil {
		return e
	}
	if count > 0 {
		return nil
	}
	var params map[string]json.RawMessage
	if e := json.Unmarshal(t.Parameters, &params); e != nil {
		return e
	}
	clean := map[string]json.RawMessage{}
	for _, key := range []string{"from", "to", "account_id"} {
		if val, ok := params[key]; ok {
			clean[key] = val
		}
	}
	raw, _ := json.Marshal(clean)
	specs, e := ValidateTasks([]TaskSpec{{Key: "auto_verify_subscriptions", AgentType: "subscription", Tool: "detect_subscriptions", Parameters: raw, Priority: 1000}})
	if e != nil {
		return e
	}
	if e = insertTasks(tx, j, specs, t.ID); e != nil {
		return e
	}
	var added AgentTask
	if e = tx.Where("job_id=? AND task_key='auto_verify_subscriptions'", j.ID).First(&added).Error; e != nil {
		return e
	}
	var savings []AgentTask
	if e = tx.Where("job_id=? AND agent_type='savings' AND status IN ?", j.ID, []string{"pending", "awaiting_input", "awaiting_confirmation"}).Find(&savings).Error; e != nil {
		return e
	}
	for _, task := range savings {
		if e = tx.Create(&AgentDependency{UserID: j.UserID, JobID: j.ID, TaskID: task.ID, DependsOn: added.ID}).Error; e != nil {
			return e
		}
	}
	return trace(tx, j, added.ID, "planned", added.Status, "")
}

func (s *Service) Run(ctx context.Context, log *slog.Logger) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, e := s.ProcessOne(ctx); e != nil {
				log.Error("orchestration_wave_failed", "code", entity.Code(e))
			}
		}
	}
}
