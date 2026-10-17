package orchestrator

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/service"
	"github.com/qingyingjin/banking-service/internal/skills"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

type Repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) *Repository { return &Repository{db} }
func hash(v any) string                     { raw, _ := json.Marshal(v); return fmt.Sprintf("%x", sha256.Sum256(raw)) }
func (r *Repository) tx(ctx context.Context, user int64, fn func(*gorm.DB) error) error {
	return repository.DBError(r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var u entity.User
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND status='active'", user).First(&u).Error; e != nil {
			return e
		}
		return fn(tx)
	}))
}
func owned(tx *gorm.DB, user int64, id string, lock bool) (Job, error) {
	var j Job
	q := tx.Where("user_id=? AND id=?", user, id)
	if lock {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	e := q.First(&j).Error
	return j, repository.DBError(e)
}
func load(tx *gorm.DB, j Job) (View, error) {
	v := View{Job: j, Tasks: []AgentTask{}, Dependencies: []AgentDependency{}}
	if e := tx.Where("user_id=? AND job_id=?", j.UserID, j.ID).Order("created_at,id").Find(&v.Tasks).Error; e != nil {
		return v, e
	}
	// Backfill only exact legacy contracts, including terminal tasks. Never reschedule.
	for i := range v.Tasks {
		t := &v.Tasks[i]
		if t.SkillID != "" || t.SkillVersion != 0 {
			continue
		}
		b, err := skills.Resolve(skills.Request{Agent: t.AgentType, Tool: t.Tool, Mode: t.Mode})
		if err != nil {
			continue
		}
		update := tx.Model(&AgentTask{}).Where("id=? AND user_id=? AND job_id=? AND status=? AND skill_id='' AND skill_version=0", t.ID, j.UserID, j.ID, t.Status).Updates(map[string]any{"skill_id": b.SkillID, "skill_version": b.SkillVersion, "mode": b.Mode})
		if update.Error != nil {
			return v, update.Error
		}
		if update.RowsAffected == 1 {
			t.SkillID, t.SkillVersion, t.Mode = b.SkillID, b.SkillVersion, b.Mode
		}
	}
	e := tx.Where("user_id=? AND job_id=?", j.UserID, j.ID).Order("id").Find(&v.Dependencies).Error
	return v, e
}
func trace(tx *gorm.DB, j Job, task, event, status, code string) error {
	a := entity.Audit{ID: uuid.NewString(), UserID: j.UserID, Action: "orchestrator." + event, Outcome: "recorded", TraceID: j.TraceID, TaskID: task}
	if task == "" {
		a.TaskID = j.ID
	}
	if e := tx.Create(&a).Error; e != nil {
		return e
	}
	var binding AgentTask
	if task != "" {
		if e := tx.Where("id=? AND user_id=? AND job_id=?", task, j.UserID, j.ID).First(&binding).Error; e != nil {
			return e
		}
	}
	return tx.Create(&ExecutionTrace{SkillID: binding.SkillID, SkillVersion: binding.SkillVersion, UserID: j.UserID, JobID: j.ID, TaskID: task, Event: event, Status: status, ErrorCode: code, AuditID: a.ID, TraceID: j.TraceID}).Error
}
func insertTasks(tx *gorm.DB, j Job, specs []TaskSpec, parent string) error {
	canonical, err := ValidateTasks(specs)
	if err != nil {
		return err
	}
	specs = canonical
	ids := map[string]string{}
	for _, s := range specs {
		ids[s.Key] = uuid.NewString()
	}
	for _, s := range specs {
		status := "pending"
		if s.Blocked {
			status = "blocked"
		} else if e := ValidateParameters(s.Tool, s.Parameters, true); e != nil {
			if entity.Code(e) == "INPUT_REQUIRED" {
				status = "awaiting_input"
			} else {
				return e
			}
		}
		task := AgentTask{SkillID: s.SkillID, SkillVersion: s.SkillVersion, ID: ids[s.Key], UserID: j.UserID, JobID: j.ID, Key: s.Key, AgentType: s.AgentType, Tool: s.Tool, Mode: s.ExecutionMode, Priority: s.Priority, Status: status, Parameters: s.Parameters, Output: json.RawMessage(`{}`), ProtectedBlocked: s.Blocked}
		if s.ExecutionMode == "intent" || s.ExecutionMode == "user_confirmation" {
			if status == "pending" {
				task.Status = "awaiting_confirmation"
			}
		}
		if e := tx.Create(&task).Error; e != nil {
			return e
		}
	}
	for _, s := range specs {
		deps := append([]string{}, s.Dependencies...)
		for _, key := range deps {
			if e := tx.Create(&AgentDependency{UserID: j.UserID, JobID: j.ID, TaskID: ids[s.Key], DependsOn: ids[key]}).Error; e != nil {
				return e
			}
		}
		if parent != "" {
			if e := tx.Create(&AgentDependency{UserID: j.UserID, JobID: j.ID, TaskID: ids[s.Key], DependsOn: parent}).Error; e != nil {
				return e
			}
		}
	}
	return nil
}
func recompute(tx *gorm.DB, j *Job) error {
	v, e := load(tx, *j)
	if e != nil {
		return e
	}
	state := "succeeded"
	pending := false
	for _, t := range v.Tasks {
		if t.Status == "failed" || t.Status == "timed_out" || t.Status == "cancelled" {
			state = "failed"
			break
		}
		if t.Status != "succeeded" {
			pending = true
		}
	}
	if state != "failed" && pending {
		state = "queued"
		for _, s := range []string{"review_required", "blocked", "awaiting_gateway", "awaiting_input", "awaiting_confirmation"} {
			for _, t := range v.Tasks {
				if t.Status == s {
					state = s
					break
				}
			}
			if state == s {
				break
			}
		}
		// Runnable read stages are allowed to complete before asking for later
		// dependency-bound confirmation or missing input.
		for _, t := range v.Tasks {
			if t.Status == "pending" {
				ready := true
				for _, d := range v.Dependencies {
					if d.TaskID == t.ID {
						for _, up := range v.Tasks {
							if up.ID == d.DependsOn && up.Status != "succeeded" {
								ready = false
							}
						}
					}
				}
				if ready {
					state = "queued"
					break
				}
			}
		}
	}
	j.Status = state
	j.LeaseToken = ""
	j.LeaseUntil = 0
	if state == "queued" {
		first := true
		for _, t := range v.Tasks {
			ready := t.Status == "pending"
			for _, d := range v.Dependencies {
				if d.TaskID == t.ID {
					for _, up := range v.Tasks {
						if up.ID == d.DependsOn && up.Status != "succeeded" {
							ready = false
						}
					}
				}
			}
			if ready && (first || t.RetryAt < j.LeaseUntil) {
				j.LeaseUntil = t.RetryAt
				first = false
			}
		}
	}
	j.Version++
	if e := tx.Save(j).Error; e != nil {
		return e
	}
	if state == "failed" {
		if e := tx.Model(&AgentTask{}).Where("user_id=? AND job_id=? AND status IN ?", j.UserID, j.ID, []string{"pending", "awaiting_input", "awaiting_confirmation", "awaiting_gateway", "blocked"}).Updates(map[string]any{"status": "blocked", "error_code": "UPSTREAM_FAILED"}).Error; e != nil {
			return e
		}
	}
	return trace(tx, *j, "", "state", state, "")
}
func (r *Repository) Get(ctx context.Context, user int64, id string) (View, error) {
	var v View
	e := r.tx(ctx, user, func(tx *gorm.DB) error {
		j, e := owned(tx, user, id, false)
		if e != nil {
			return e
		}
		v, e = load(tx, j)
		return e
	})
	return v, e
}
func (r *Repository) Events(ctx context.Context, user int64, id string, after int64, limit int) ([]ExecutionTrace, error) {
	out := []ExecutionTrace{}
	if after < 0 || limit < 1 || limit > 100 {
		return out, entity.Fail("INVALID_ARGUMENT", "bounded event pagination required")
	}
	e := r.tx(ctx, user, func(tx *gorm.DB) error {
		if _, e := owned(tx, user, id, false); e != nil {
			return e
		}
		return tx.Where("user_id=? AND job_id=? AND id>?", user, id, after).Order("id").Limit(limit).Find(&out).Error
	})
	return out, e
}
func (r *Repository) Trace(ctx context.Context, user int64, id string, after int64, limit int) (TraceView, error) {
	out := TraceView{Events: []ExecutionTrace{}, Executions: []AgentExecution{}, Invocations: []ToolInvocation{}}
	events, e := r.Events(ctx, user, id, after, limit)
	if e != nil {
		return out, e
	}
	out.Events = events
	e = r.tx(ctx, user, func(tx *gorm.DB) error {
		if _, e := owned(tx, user, id, false); e != nil {
			return e
		}
		if e := tx.Where("user_id=? AND job_id=?", user, id).Order("started_at DESC,id").Limit(100).Find(&out.Executions).Error; e != nil {
			return e
		}
		return tx.Where("user_id=? AND job_id=?", user, id).Order("started_at DESC,id").Limit(100).Find(&out.Invocations).Error
	})
	return out, e
}
func (r *Repository) claim(ctx context.Context) (Job, []AgentTask, error) {
	var hint Job
	now := time.Now().UnixMilli()
	e := r.db.WithContext(ctx).Where("EXISTS (SELECT 1 FROM banking_users u WHERE u.id=banking_agent_jobs.user_id AND u.status='active')").Where("(status='queued' AND lease_until<=?) OR (status='running' AND lease_until<?)", now, now).Order("created_at,id").First(&hint).Error
	if e == gorm.ErrRecordNotFound {
		return Job{}, nil, nil
	}
	if e != nil {
		return Job{}, nil, e
	}
	var j Job
	ready := []AgentTask{}
	e = r.tx(ctx, hint.UserID, func(tx *gorm.DB) error {
		var e error
		j, e = owned(tx, hint.UserID, hint.ID, true)
		if e != nil {
			return e
		}
		now := time.Now().UnixMilli()
		if j.Status != "queued" && (j.Status != "running" || j.LeaseUntil >= now) {
			j.ID = ""
			return nil
		}
		if j.Status == "running" {
			var tasks []AgentTask
			if e = tx.Where("user_id=? AND job_id=? AND status='running'", j.UserID, j.ID).Find(&tasks).Error; e != nil {
				return e
			}
			for _, t := range tasks {
				status := "pending"
				if t.Mode != "read_only" {
					status = "review_required"
				}
				if t.Attempts >= j.MaxAttempts && status == "pending" {
					status = "failed"
				}
				if e = tx.Model(&AgentTask{}).Where("id=?", t.ID).Updates(map[string]any{"status": status, "error_code": "INTERRUPTED"}).Error; e != nil {
					return e
				}
				if e = trace(tx, j, t.ID, "recovered", status, "INTERRUPTED"); e != nil {
					return e
				}
			}
			if e = tx.Model(&AgentExecution{}).Where("user_id=? AND job_id=? AND status='running'", j.UserID, j.ID).Updates(map[string]any{"status": "interrupted", "error_code": "INTERRUPTED", "finished_at": now}).Error; e != nil {
				return e
			}
			if e = tx.Model(&ToolInvocation{}).Where("user_id=? AND job_id=? AND status='running'", j.UserID, j.ID).Updates(map[string]any{"status": "interrupted", "error_code": "INTERRUPTED", "finished_at": now}).Error; e != nil {
				return e
			}
		}
		v, e := load(tx, j)
		if e != nil {
			return e
		}
		// Revalidate durable bindings under the owner/job lock before selecting work.
		for i := range v.Tasks {
			t := &v.Tasks[i]
			b, err := skills.Resolve(skills.Request{SkillID: t.SkillID, SkillVersion: t.SkillVersion, Agent: t.AgentType, Tool: t.Tool, Mode: t.Mode})
			if err != nil {
				if t.Status != "succeeded" && t.Status != "failed" && t.Status != "cancelled" && t.Status != "blocked" {
					if e = tx.Model(&AgentTask{}).Where("user_id=? AND job_id=? AND id=? AND status=?", j.UserID, j.ID, t.ID, t.Status).Updates(map[string]any{"status": "review_required", "error_code": "INVALID_TASK_PLAN"}).Error; e != nil {
						return e
					}
					t.Status = "review_required"
					t.ErrorCode = "INVALID_TASK_PLAN"
					if e = trace(tx, j, t.ID, "skill_rejected", t.Status, t.ErrorCode); e != nil {
						return e
					}
				}
				continue
			}
			if t.SkillID == "" && t.SkillVersion == 0 {
				if e = tx.Model(&AgentTask{}).Where("user_id=? AND job_id=? AND id=? AND status=? AND skill_id='' AND skill_version=0", j.UserID, j.ID, t.ID, t.Status).Updates(map[string]any{"skill_id": b.SkillID, "skill_version": b.SkillVersion, "mode": b.Mode}).Error; e != nil {
					return e
				}
				t.SkillID, t.SkillVersion, t.Mode = b.SkillID, b.SkillVersion, b.Mode
			}
		}
		byID := map[string]AgentTask{}
		for _, t := range v.Tasks {
			byID[t.ID] = t
		}
		for _, t := range v.Tasks {
			if t.Status != "pending" || t.RetryAt > now {
				continue
			}
			ok := true
			for _, d := range v.Dependencies {
				if d.TaskID == t.ID && byID[d.DependsOn].Status != "succeeded" {
					ok = false
				}
			}
			if ok {
				ready = append(ready, t)
			}
		}
		sortReady(ready)
		if len(ready) > 0 {
			n := 1
			if j.Strategy == "parallel_read" && ready[0].Mode == "read_only" {
				for n < len(ready) && n < 4 && ready[n].Mode == "read_only" {
					n++
				}
			}
			ready = ready[:n]
		}
		if len(ready) == 0 {
			if e = recompute(tx, &j); e != nil {
				return e
			}
			j.ID = ""
			return nil
		}
		j.Status = "running"
		j.LeaseToken = uuid.NewString()
		j.LeaseUntil = now + j.TimeoutMS + 30000
		j.Version++
		if e = tx.Save(&j).Error; e != nil {
			return e
		}
		for i := range ready {
			t := &ready[i]
			t.Status = "running"
			t.Attempts++
			if e = tx.Save(t).Error; e != nil {
				return e
			}
			callInput, _ := json.Marshal(map[string]any{"skill_id": t.SkillID, "skill_version": t.SkillVersion, "rule_version": "1", "tool": t.Tool, "parameters": json.RawMessage(t.Parameters), "dependency_task_ids": dependencyIDs(v.Dependencies, t.ID)})
			execution := AgentExecution{ID: uuid.NewString(), UserID: j.UserID, JobID: j.ID, TaskID: t.ID, AgentType: t.AgentType, Attempt: t.Attempts, Status: "running", StartedAt: now, Input: callInput, Output: json.RawMessage(`{}`), LeaseToken: j.LeaseToken}
			if e = tx.Create(&execution).Error; e != nil {
				return e
			}
			inv := ToolInvocation{ID: uuid.NewString(), UserID: j.UserID, JobID: j.ID, TaskID: t.ID, ExecutionID: execution.ID, Tool: t.Tool, InvocationKey: "orchestrator:" + t.ID, Status: "running", StartedAt: now, AuditID: uuid.NewString()}
			if e = tx.Create(&entity.Audit{ID: inv.AuditID, UserID: j.UserID, Action: "orchestrator.invoke", Outcome: "started", TraceID: j.TraceID, TaskID: t.ID}).Error; e != nil {
				return e
			}
			if e = tx.Create(&inv).Error; e != nil {
				return e
			}
			if e = trace(tx, j, t.ID, "started", "running", ""); e != nil {
				return e
			}
		}
		return nil
	})
	return j, ready, repository.DBError(e)
}
func dependencyIDs(deps []AgentDependency, id string) []string {
	out := []string{}
	for _, d := range deps {
		if d.TaskID == id {
			out = append(out, d.DependsOn)
		}
	}
	return out
}
func metaJob(j Job, t AgentTask) service.Meta {
	return service.Meta{UserID: j.UserID, TraceID: j.TraceID, TaskID: t.ID}
}
