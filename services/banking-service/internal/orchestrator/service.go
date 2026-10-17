package orchestrator

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/goal"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/service"
	"gorm.io/gorm"
	"strconv"
	"sync"
	"time"
)

type Service struct {
	repo    *Repository
	runner  Runner
	planner InstructionPlanner
	mu      sync.Mutex
	active  map[string]activeWave
}
type activeWave struct {
	token  string
	cancel context.CancelFunc
}

func New(db *gorm.DB, runner Runner, planner InstructionPlanner) *Service {
	return &Service{repo: NewRepository(db), runner: runner, planner: planner, active: map[string]activeWave{}}
}
func (s *Service) Configured() bool { return s != nil && s.repo != nil && s.repo.db != nil }
func ID(id string) error {
	u, e := uuid.Parse(id)
	if e != nil || u.String() != id {
		return entity.Fail("INVALID_ARGUMENT", "canonical orchestration UUID required")
	}
	return nil
}
func (s *Service) Submit(ctx context.Context, m service.Meta, key string, in SubmitInput) (View, error) {
	var out View
	if len(key) < 1 || len(key) > 128 {
		return out, entity.Fail("INVALID_ARGUMENT", "Idempotency-Key required")
	}
	for _, c := range key {
		if c < 33 || c > 126 {
			return out, entity.Fail("INVALID_ARGUMENT", "ASCII idempotency key required")
		}
	}
	if in.Strategy == "" {
		in.Strategy = "parallel_read"
	}
	if in.MaxAttempts == 0 {
		in.MaxAttempts = 2
	}
	if in.TimeoutMS == 0 {
		in.TimeoutMS = 30000
	}
	if (in.Strategy != "sequential" && in.Strategy != "parallel_read") || in.MaxAttempts < 1 || in.MaxAttempts > 3 || in.TimeoutMS < 100 || in.TimeoutMS > 60000 {
		return out, entity.Fail("INVALID_ARGUMENT", "invalid strategy, bounded retry or timeout")
	}
	sources := 0
	if len(in.Tasks) > 0 {
		sources++
	}
	if in.Instruction != "" {
		sources++
	}
	if in.TaskPlanID != "" {
		sources++
	}
	if sources != 1 {
		return out, entity.Fail("INVALID_ARGUMENT", "choose one instruction, TaskPlan or structured task list")
	}
	requestHash := hash(in)
	err := s.repo.tx(ctx, m.UserID, func(tx *gorm.DB) error {
		var existing Job
		e := tx.Where("user_id=? AND idempotency_key=?", m.UserID, key).First(&existing).Error
		if e == nil {
			if existing.RequestHash != requestHash {
				return entity.Fail("IDEMPOTENCY_CONFLICT", "key bound to different task request")
			}
			out, e = load(tx, existing)
			return e
		}
		if e != gorm.ErrRecordNotFound {
			return e
		}
		tasks := in.Tasks
		if in.Instruction != "" {
			if s.planner == nil {
				return entity.Fail("COZE_UNAVAILABLE", "configure published Supervisor workflow before interpreting instructions")
			}
			params, _ := json.Marshal(map[string]any{"instruction": in.Instruction, "bindings": in.Bindings})
			tasks = []TaskSpec{{Key: "understand", AgentType: "supervisor", Tool: "plan_instruction", Parameters: params, Dependencies: []string{}}}
		}
		if in.TaskPlanID != "" {
			var e error
			tasks, e = fromGoalPlan(tx, m.UserID, in.TaskPlanID, in.Bindings)
			if e != nil {
				return e
			}
		}
		specs, e := ValidateTasks(tasks)
		if e != nil {
			return e
		}
		j := Job{ID: uuid.NewString(), UserID: m.UserID, Status: "queued", Strategy: in.Strategy, Version: 1, MaxAttempts: in.MaxAttempts, TimeoutMS: in.TimeoutMS, UseCoze: in.UseCoze, SourcePlanID: in.TaskPlanID, RequestHash: requestHash, IdempotencyKey: key, TraceID: m.TraceID, AuditID: uuid.NewString()}
		if j.TraceID == "" {
			j.TraceID = j.ID
		}
		if e = tx.Create(&j).Error; e != nil {
			return e
		}
		if e = tx.Create(&entity.Audit{ID: j.AuditID, UserID: j.UserID, Action: "orchestrator.submit", Outcome: "succeeded", TraceID: j.TraceID, TaskID: j.ID}).Error; e != nil {
			return e
		}
		if e = insertTasks(tx, j, specs, ""); e != nil {
			return e
		}
		if e = trace(tx, j, "", "submitted", "queued", ""); e != nil {
			return e
		}
		out, e = load(tx, j)
		return e
	})
	return out, err
}
func fromGoalPlan(tx *gorm.DB, user int64, id string, bindings Bindings) ([]TaskSpec, error) {
	n, e := strconv.ParseInt(id, 10, 64)
	if e != nil || n <= 0 || strconv.FormatInt(n, 10) != id {
		return nil, entity.Fail("INVALID_ARGUMENT", "canonical TaskPlan ID required")
	}
	var source goal.TaskPlan
	if e = tx.Where("id=? AND user_id=?", n, user).First(&source).Error; e != nil {
		return nil, repository.DBError(e)
	}
	var current goal.Goal
	if e = tx.Where("id=? AND user_id=?", source.GoalID, user).First(&current).Error; e != nil {
		return nil, e
	}
	if current.CurrentVersion != source.Version {
		return nil, entity.Fail("VERSION_CONFLICT", "Goal Compiler plan is no longer current")
	}
	var plan goal.Plan
	if e = json.Unmarshal(source.PlanJSON, &plan); e != nil {
		return nil, e
	}
	if e = goal.ValidatePlan(plan); e != nil {
		return nil, e
	}
	mapping := map[string]string{"query_accounts": "get_account_summary", "analyze_bills": "analyze_spending", "check_subscriptions": "detect_subscriptions", "calculate_budget": "compare_budget", "generate_savings_plan": "replan_financial_plan", "propose_financial_operation": "require_gateway"}
	tasks := []TaskSpec{}
	for _, t := range plan.Tasks {
		tool := mapping[t.TaskType]
		policy, ok := Lookup(tool)
		if !ok {
			return nil, entity.Fail("INVALID_TASK_PLAN", "unsupported compiled task")
		}
		params := map[string]any{}
		if policy.Agent == "billing" || tool == "detect_subscriptions" {
			if bindings.From != "" {
				params["from"] = bindings.From
			}
			if bindings.To != "" {
				params["to"] = bindings.To
			}
			if bindings.AccountID != "" {
				if tool == "detect_subscriptions" {
					params["account_id"] = bindings.AccountID
				} else {
					params["account_id"] = bindings.AccountID
				}
			}
		}
		if policy.Agent == "savings" && bindings.FinancialPlanID != "" {
			params["plan_id"] = bindings.FinancialPlanID
		}
		raw, _ := json.Marshal(params)
		tasks = append(tasks, TaskSpec{Key: t.TaskID, AgentType: policy.Agent, Tool: tool, ExecutionMode: policy.Mode, Parameters: raw, Dependencies: append([]string{}, t.Dependencies...), Blocked: t.Status == "blocked"})
	}
	return tasks, nil
}
func (s *Service) Get(ctx context.Context, user int64, id string) (View, error) {
	if e := ID(id); e != nil {
		return View{}, e
	}
	return s.repo.Get(ctx, user, id)
}
func (s *Service) Result(ctx context.Context, user int64, id string) (Result, error) {
	v, e := s.Get(ctx, user, id)
	out := Result{JobID: id, Status: v.Job.Status, Outputs: []AgentOutput{}}
	if e != nil {
		return out, e
	}
	for _, t := range v.Tasks {
		var a AgentOutput
		if e = json.Unmarshal(t.Output, &a); e != nil {
			return out, e
		}
		if a.TaskID == t.ID {
			out.Outputs = append(out.Outputs, a)
		}
	}
	return out, nil
}
func (s *Service) Trace(ctx context.Context, user int64, id string, after int64, limit int) (TraceView, error) {
	if e := ID(id); e != nil {
		return TraceView{}, e
	}
	return s.repo.Trace(ctx, user, id, after, limit)
}
func (s *Service) Events(ctx context.Context, user int64, id string, after int64, limit int) ([]ExecutionTrace, error) {
	if e := ID(id); e != nil {
		return nil, e
	}
	return s.repo.Events(ctx, user, id, after, limit)
}
func terminal(s string) bool { return s == "succeeded" || s == "failed" || s == "cancelled" }
func (s *Service) interrupt(id string) {
	s.mu.Lock()
	cancel := s.active[id].cancel
	s.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}
func (s *Service) releaseWave(id, token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active[id].token == token {
		delete(s.active, id)
	}
}
func (s *Service) Control(ctx context.Context, m service.Meta, id, action string) (View, error) {
	var out View
	if e := ID(id); e != nil {
		return out, e
	}
	err := s.repo.tx(ctx, m.UserID, func(tx *gorm.DB) error {
		j, e := owned(tx, m.UserID, id, true)
		if e != nil {
			return e
		}
		if action == "cancel" && j.Status == "cancelled" {
			out, e = load(tx, j)
			return e
		}
		if terminal(j.Status) {
			return entity.Fail("STATE_CONFLICT", "terminal task cannot transition")
		}
		switch action {
		case "cancel", "pause":
			state := "paused"
			if action == "cancel" {
				state = "cancelled"
			}
			var tasks []AgentTask
			if e = tx.Where("user_id=? AND job_id=? AND status<>'succeeded'", m.UserID, id).Find(&tasks).Error; e != nil {
				return e
			}
			for _, t := range tasks {
				status := t.Status
				if action == "cancel" {
					status = "cancelled"
				} else if t.Status == "running" {
					status = "pending"
					if t.Mode != "read_only" || t.Attempts >= j.MaxAttempts {
						status = "review_required"
					}
				}
				if e = tx.Model(&AgentTask{}).Where("id=?", t.ID).Updates(map[string]any{"status": status}).Error; e != nil {
					return e
				}
			}
			if e = tx.Model(&AgentExecution{}).Where("user_id=? AND job_id=? AND status='running'", m.UserID, id).Updates(map[string]any{"status": "interrupted", "error_code": "INTERRUPTED", "finished_at": time.Now().UnixMilli()}).Error; e != nil {
				return e
			}
			if e = tx.Model(&ToolInvocation{}).Where("user_id=? AND job_id=? AND status='running'", m.UserID, id).Updates(map[string]any{"status": "interrupted", "error_code": "INTERRUPTED", "finished_at": time.Now().UnixMilli()}).Error; e != nil {
				return e
			}
			j.Status = state
			j.Version++
			j.LeaseToken = ""
			j.LeaseUntil = 0
			if e = tx.Save(&j).Error; e != nil {
				return e
			}
		case "resume":
			if j.Status != "paused" && j.Status != "awaiting_gateway" {
				return entity.Fail("STATE_CONFLICT", "resume only paused or gateway-waiting task")
			}
			if e = tx.Model(&AgentTask{}).Where("job_id=? AND status='awaiting_gateway' AND attempts<?", j.ID, j.MaxAttempts).Update("status", "pending").Error; e != nil {
				return e
			}
			if e = recompute(tx, &j); e != nil {
				return e
			}
		default:
			return entity.Fail("INVALID_ARGUMENT", "unknown task control")
		}
		if e = trace(tx, j, "", action, j.Status, ""); e != nil {
			return e
		}
		out, e = load(tx, j)
		return e
	})
	if err == nil && (action == "cancel" || action == "pause") {
		s.interrupt(id)
	}
	return out, err
}
func (s *Service) Confirm(ctx context.Context, m service.Meta, id string, in ConfirmInput) (View, error) {
	var out View
	if e := ID(id); e != nil {
		return out, e
	}
	if in.Decision != "approve" && in.Decision != "reject" {
		return out, entity.Fail("INVALID_ARGUMENT", "explicit decision required")
	}
	err := s.repo.tx(ctx, m.UserID, func(tx *gorm.DB) error {
		j, e := owned(tx, m.UserID, id, true)
		if e != nil {
			return e
		}
		if j.Version != in.ExpectedVersion || terminal(j.Status) || j.Status == "running" {
			return entity.Fail("VERSION_CONFLICT", "review current task version")
		}
		var t AgentTask
		if e = tx.Where("job_id=? AND user_id=? AND task_key=?", id, m.UserID, in.TaskKey).First(&t).Error; e != nil {
			return e
		}
		if t.Confirmation == in.Decision {
			out, e = load(tx, j)
			return e
		}
		if t.Status != "awaiting_confirmation" || t.ProtectedBlocked {
			return entity.Fail("STATE_CONFLICT", "this task cannot accept confirmation")
		}
		v, e := load(tx, j)
		if e != nil {
			return e
		}
		for _, d := range v.Dependencies {
			if d.TaskID == t.ID {
				for _, up := range v.Tasks {
					if up.ID == d.DependsOn && up.Status != "succeeded" {
						return entity.Fail("STATE_CONFLICT", "confirmation requires completed evidence dependencies")
					}
				}
			}
		}
		t.Confirmation = in.Decision
		if in.Decision == "reject" {
			t.Status = "cancelled"
			t.ErrorCode = "USER_REJECTED"
		} else {
			t.Status = "pending"
		}
		if e = tx.Save(&t).Error; e != nil {
			return e
		}
		if e = trace(tx, j, t.ID, "confirmed", t.Status, ""); e != nil {
			return e
		}
		if j.Status != "paused" {
			if e = recompute(tx, &j); e != nil {
				return e
			}
		} else {
			j.Version++
			if e = tx.Save(&j).Error; e != nil {
				return e
			}
		}
		out, e = load(tx, j)
		return e
	})
	return out, err
}
func (s *Service) Inputs(ctx context.Context, m service.Meta, id string, in InputsInput) (View, error) {
	var out View
	if e := ID(id); e != nil {
		return out, e
	}
	err := s.repo.tx(ctx, m.UserID, func(tx *gorm.DB) error {
		j, e := owned(tx, m.UserID, id, true)
		if e != nil {
			return e
		}
		if j.Version != in.ExpectedVersion || terminal(j.Status) || j.Status == "running" {
			return entity.Fail("VERSION_CONFLICT", "review current task version")
		}
		var t AgentTask
		if e = tx.Where("job_id=? AND user_id=? AND task_key=?", id, m.UserID, in.TaskKey).First(&t).Error; e != nil {
			return e
		}
		if t.ProtectedBlocked || t.Status != "awaiting_input" {
			return entity.Fail("STATE_CONFLICT", "only missing inputs can be supplied; goal restrictions remain")
		}
		if e = ValidateParameters(t.Tool, in.Parameters, true); e != nil {
			return e
		}
		t.Parameters = in.Parameters
		t.Status = "pending"
		t.Confirmation = ""
		if t.Mode == "intent" || t.Mode == "user_confirmation" {
			t.Status = "awaiting_confirmation"
		}
		if e = tx.Save(&t).Error; e != nil {
			return e
		}
		if e = trace(tx, j, t.ID, "inputs", t.Status, ""); e != nil {
			return e
		}
		if j.Status != "paused" {
			if e = recompute(tx, &j); e != nil {
				return e
			}
		} else {
			j.Version++
			if e = tx.Save(&j).Error; e != nil {
				return e
			}
		}
		out, e = load(tx, j)
		return e
	})
	return out, err
}

// Use a narrow Coze planner for the persisted Supervisor understanding task.
func (s *Service) understand(ctx context.Context, call Call) (AgentOutput, error) {
	out := AgentOutput{TaskID: call.TaskID, AgentType: "supervisor", Status: "succeeded", Evidence: []Evidence{}, Errors: []TaskError{}, NextActions: []NextAction{}}
	if s.planner == nil {
		return out, entity.Fail("COZE_UNAVAILABLE", "Supervisor workflow not configured")
	}
	var in struct {
		Instruction string   `json:"instruction"`
		Bindings    Bindings `json:"bindings"`
	}
	if e := strictJSON(call.Parameters, &in); e != nil {
		return out, e
	}
	var specs []TaskSpec
	var e error
	if planner, ok := s.planner.(interface {
		PlanWithEvidence(context.Context, service.Meta, string, Bindings) ([]TaskSpec, string, error)
	}); ok {
		specs, out.WorkflowExecuteID, e = planner.PlanWithEvidence(ctx, call.Meta, in.Instruction, in.Bindings)
	} else {
		specs, e = s.planner.Plan(ctx, call.Meta, in.Instruction, in.Bindings)
	}
	if e != nil {
		return out, e
	}
	specs, e = ValidateTasks(specs)
	if e != nil {
		return out, e
	}
	for _, spec := range specs {
		if spec.Tool == "plan_instruction" || spec.Key == "understand" || spec.Key == "auto_verify_subscriptions" {
			return out, entity.Fail("INVALID_TASK_PLAN", "recursive Supervisor tasks are forbidden")
		}
	}
	out.Result, _ = json.Marshal(map[string]any{"tasks": specs})
	return out, nil
}
