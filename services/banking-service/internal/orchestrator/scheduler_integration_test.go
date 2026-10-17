//go:build integration

package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/goal"
	"github.com/qingyingjin/banking-service/internal/migration"
	"github.com/qingyingjin/banking-service/internal/platform"
	"github.com/qingyingjin/banking-service/internal/service"
	"math/rand/v2"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fixtureRunner func(context.Context, Call) (AgentOutput, error)

func (f fixtureRunner) Run(ctx context.Context, c Call) (AgentOutput, error) { return f(ctx, c) }
func fixtureOutput(c Call) AgentOutput {
	return AgentOutput{TaskID: c.TaskID, AgentType: c.AgentType, Status: "succeeded", Result: json.RawMessage(fmt.Sprintf(`{"user_id":"%d","fixture":true}`, c.Meta.UserID)), Evidence: []Evidence{}, Errors: []TaskError{}, NextActions: []NextAction{}}
}
func schedulerHarness(t *testing.T, runner Runner) (*Service, service.Meta) {
	t.Helper()
	if p := os.Getenv("BANKING_TEST_ENV_FILE"); p != "" {
		if e := godotenv.Load(p); e != nil {
			t.Fatal(e)
		}
	}
	cfg, e := config.Load(os.Getenv)
	if e != nil {
		t.Fatal(e)
	}
	conn, e := platform.Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(conn.Close)
	if e = migration.Apply(context.Background(), conn.SQL); e != nil {
		t.Fatal(e)
	}
	uid := int64(3_000_000_000_000) + rand.Int64N(1_000_000_000_000)
	if e = conn.DB.Create(&entity.User{ID: uid, DisplayName: "module7 integration fixture", Status: "active"}).Error; e != nil {
		t.Fatal(e)
	}
	s := New(conn.DB, runner, nil)
	t.Cleanup(func() {
		conn.DB.Model(&Job{}).Where("user_id=?", uid).Updates(map[string]any{"status": "cancelled", "lease_token": ""})
	})
	return s, service.Meta{UserID: uid, TraceID: "module7-integration", TaskID: "module7-test"}
}
func readSpec(key string, deps ...string) TaskSpec {
	return TaskSpec{Key: key, AgentType: "subscription", Tool: "analyze_subscription_cost", Parameters: json.RawMessage(`{}`), Dependencies: deps}
}
func submitFixture(t *testing.T, s *Service, m service.Meta, tasks []TaskSpec, modify func(*SubmitInput)) View {
	t.Helper()
	in := SubmitInput{Tasks: tasks, TimeoutMS: 1000}
	if modify != nil {
		modify(&in)
	}
	v, e := s.Submit(context.Background(), m, uuid.NewString(), in)
	if e != nil {
		t.Fatal(e)
	}
	return v
}
func wave(t *testing.T, s *Service) {
	t.Helper()
	if _, e := s.ProcessOne(context.Background()); e != nil {
		t.Fatal(e)
	}
}
func current(t *testing.T, s *Service, m service.Meta, id string) View {
	t.Helper()
	v, e := s.Get(context.Background(), m.UserID, id)
	if e != nil {
		t.Fatal(e)
	}
	return v
}

func TestSchedulerParallelAndDependencies(t *testing.T) {
	var running, peak atomic.Int32
	run := fixtureRunner(func(ctx context.Context, c Call) (AgentOutput, error) {
		n := running.Add(1)
		for old := peak.Load(); n > old && !peak.CompareAndSwap(old, n); old = peak.Load() {
		}
		defer running.Add(-1)
		time.Sleep(35 * time.Millisecond)
		return fixtureOutput(c), nil
	})
	s, m := schedulerHarness(t, run)
	v := submitFixture(t, s, m, []TaskSpec{readSpec("a"), readSpec("b"), readSpec("c"), readSpec("after", "a", "b", "c")}, nil)
	wave(t, s)
	if peak.Load() != 3 {
		t.Fatalf("parallel peak %d", peak.Load())
	}
	v = current(t, s, m, v.Job.ID)
	for _, task := range v.Tasks {
		if task.Key == "after" && task.Attempts != 0 {
			t.Fatal("dependency ran early")
		}
	}
	wave(t, s)
	v = current(t, s, m, v.Job.ID)
	if v.Job.Status != "succeeded" {
		t.Fatalf("state %s", v.Job.Status)
	}
	tr, e := s.Trace(context.Background(), m.UserID, v.Job.ID, 0, 100)
	if e != nil {
		t.Fatal(e)
	}
	if len(tr.Executions) != 4 || len(tr.Invocations) != 4 {
		t.Fatalf("trace %+v", tr)
	}
	for _, inv := range tr.Invocations {
		var count int64
		s.repo.db.Model(&entity.Audit{}).Where("id=?", inv.AuditID).Count(&count)
		if count != 1 {
			t.Fatal("missing invocation audit")
		}
	}
	peak.Store(0)
	v = submitFixture(t, s, m, []TaskSpec{readSpec("a"), readSpec("b")}, func(in *SubmitInput) { in.Strategy = "sequential" })
	wave(t, s)
	wave(t, s)
	if peak.Load() != 1 || current(t, s, m, v.Job.ID).Job.Status != "succeeded" {
		t.Fatal("sequential execution violated")
	}
}

func TestSchedulerRetryTimeoutAndInvalidResult(t *testing.T) {
	for _, mode := range []string{"retry", "timeout", "invalid", "upstream"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			s, m := schedulerHarness(t, fixtureRunner(func(ctx context.Context, c Call) (AgentOutput, error) {
				n := calls.Add(1)
				if mode == "retry" && n == 1 {
					return AgentOutput{}, entity.Fail("DEPENDENCY_UNAVAILABLE", "fixture retry")
				}
				if mode == "timeout" {
					<-ctx.Done()
					return AgentOutput{}, ctx.Err()
				}
				if mode == "upstream" {
					return AgentOutput{}, entity.Fail("FORBIDDEN", "fixture denied")
				}
				out := fixtureOutput(c)
				if mode == "invalid" {
					out.FinancialExecutionEnabled = true
				}
				return out, nil
			}))
			v := submitFixture(t, s, m, []TaskSpec{readSpec("first"), readSpec("after", "first")}, func(in *SubmitInput) { in.TimeoutMS = 100; in.MaxAttempts = 2 })
			wave(t, s)
			time.Sleep(150 * time.Millisecond)
			wave(t, s)
			if mode == "retry" {
				wave(t, s)
			}
			v = current(t, s, m, v.Job.ID)
			want := "failed"
			if mode == "retry" {
				want = "succeeded"
			}
			if v.Job.Status != want {
				t.Fatalf("%s: %s", mode, v.Job.Status)
			}
			for _, task := range v.Tasks {
				if mode != "retry" && task.Key == "after" && task.Attempts != 0 {
					t.Fatal("failed upstream invoked dependent")
				}
				if task.Attempts > 2 {
					t.Fatal("unbounded retry")
				}
			}
		})
	}
}

func TestSchedulerIdempotencyAndOwnership(t *testing.T) {
	s, m := schedulerHarness(t, fixtureRunner(func(_ context.Context, c Call) (AgentOutput, error) { return fixtureOutput(c), nil }))
	in := SubmitInput{Tasks: []TaskSpec{readSpec("a")}}
	key := uuid.NewString()
	v, e := s.Submit(context.Background(), m, key, in)
	if e != nil {
		t.Fatal(e)
	}
	again, e := s.Submit(context.Background(), m, key, in)
	if e != nil || again.Job.ID != v.Job.ID {
		t.Fatal("duplicate created job", e)
	}
	in.Strategy = "sequential"
	if _, e = s.Submit(context.Background(), m, key, in); entity.Code(e) != "IDEMPOTENCY_CONFLICT" {
		t.Fatal("changed request accepted", e)
	}
	wave(t, s)
	wave(t, s)
	tr, e := s.Trace(context.Background(), m.UserID, v.Job.ID, 0, 100)
	if e != nil || len(tr.Executions) != 1 {
		t.Fatal("completed task repeated", e)
	}
	other := m
	other.UserID++
	s.repo.db.Create(&entity.User{ID: other.UserID, DisplayName: "other fixture", Status: "active"})
	if _, e = s.Get(context.Background(), other.UserID, v.Job.ID); e == nil {
		t.Fatal("foreign job disclosed")
	}
	if _, e = s.Control(context.Background(), other, v.Job.ID, "cancel"); e == nil {
		t.Fatal("foreign cancellation allowed")
	}
}

func TestSchedulerPauseResumeAndCancelFence(t *testing.T) {
	for _, action := range []string{"pause", "cancel"} {
		t.Run(action, func(t *testing.T) {
			entered := make(chan struct{}, 1)
			var calls atomic.Int32
			s, m := schedulerHarness(t, fixtureRunner(func(ctx context.Context, c Call) (AgentOutput, error) {
				if calls.Add(1) == 1 {
					entered <- struct{}{}
					<-ctx.Done()
					return AgentOutput{}, ctx.Err()
				}
				return fixtureOutput(c), nil
			}))
			v := submitFixture(t, s, m, []TaskSpec{readSpec("a")}, nil)
			done := make(chan error, 1)
			go func() { _, e := s.ProcessOne(context.Background()); done <- e }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("not started")
			}
			if _, e := s.Control(context.Background(), m, v.Job.ID, action); e != nil {
				t.Fatal(e)
			}
			if e := <-done; e != nil {
				t.Fatal(e)
			}
			v = current(t, s, m, v.Job.ID)
			want := "paused"
			if action == "cancel" {
				want = "cancelled"
			}
			if v.Job.Status != want {
				t.Fatal("late response resurrected job", v.Job.Status)
			}
			if action == "pause" {
				if _, e := s.Control(context.Background(), m, v.Job.ID, "resume"); e != nil {
					t.Fatal(e)
				}
				wave(t, s)
				if current(t, s, m, v.Job.ID).Job.Status != "succeeded" {
					t.Fatal("resume failed")
				}
			}
		})
	}
}

func TestSchedulerCrashRecoveryAndIntentIsolation(t *testing.T) {
	s, m := schedulerHarness(t, fixtureRunner(func(_ context.Context, c Call) (AgentOutput, error) { return fixtureOutput(c), nil }))
	v := submitFixture(t, s, m, []TaskSpec{readSpec("a")}, nil)
	j, _, e := s.repo.claim(context.Background())
	if e != nil || j.ID != v.Job.ID {
		t.Fatal("claim", e)
	}
	s.repo.db.Model(&Job{}).Where("id=?", j.ID).Update("lease_until", 1)
	wave(t, s)
	if current(t, s, m, j.ID).Job.Status != "succeeded" {
		t.Fatal("read recovery failed")
	}
	// Gateway isolation uses a runner that cannot bypass the boundary.
	s.runner = fixtureRunner(func(_ context.Context, c Call) (AgentOutput, error) {
		if !c.Confirmed {
			t.Fatal("unconfirmed sensitive invocation")
		}
		return AgentOutput{}, entity.Fail("GATEWAY_UNAVAILABLE", "module8 absent")
	})
	v = submitFixture(t, s, m, []TaskSpec{{Key: "cancel", AgentType: "subscription", Tool: "create_cancel_intent", Parameters: json.RawMessage(`{"subscription_id":"1","expected_version":1}`)}}, nil)
	wave(t, s)
	v = current(t, s, m, v.Job.ID)
	if v.Job.Status != "awaiting_confirmation" || v.Tasks[0].Attempts != 0 {
		t.Fatal("unconfirmed action ran")
	}
	v, e = s.Confirm(context.Background(), m, v.Job.ID, ConfirmInput{TaskKey: "cancel", ExpectedVersion: v.Job.Version, Decision: "approve"})
	if e != nil {
		t.Fatal(e)
	}
	wave(t, s)
	v = current(t, s, m, v.Job.ID)
	if v.Job.Status != "awaiting_gateway" {
		t.Fatal("gateway was bypassed", v.Job.Status)
	}
	// An uncertain non-read operation after a process crash must never auto retry.
	s.repo.db.Model(&Job{}).Where("id=?", v.Job.ID).Updates(map[string]any{"status": "running", "lease_until": 1, "lease_token": "crashed"})
	s.repo.db.Model(&AgentTask{}).Where("id=?", v.Tasks[0].ID).Update("status", "running")
	wave(t, s)
	if current(t, s, m, v.Job.ID).Job.Status != "review_required" {
		t.Fatal("uncertain action retried")
	}
}

func TestSchedulerCompetingClaims(t *testing.T) {
	var calls atomic.Int32
	s, m := schedulerHarness(t, fixtureRunner(func(_ context.Context, c Call) (AgentOutput, error) {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return fixtureOutput(c), nil
	}))
	v := submitFixture(t, s, m, []TaskSpec{readSpec("a")}, nil)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for range 2 {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := s.ProcessOne(context.Background()); errs <- e }()
	}
	wg.Wait()
	for range 2 {
		if e := <-errs; e != nil {
			t.Fatal(e)
		}
	}
	if calls.Load() != 1 || current(t, s, m, v.Job.ID).Job.Status != "succeeded" {
		t.Fatal("lease allowed duplicate invocation")
	}
}

type fixturePlanner func(context.Context, service.Meta, string, Bindings) ([]TaskSpec, error)

func (f fixturePlanner) Plan(c context.Context, m service.Meta, text string, b Bindings) ([]TaskSpec, error) {
	return f(c, m, text, b)
}
func TestSchedulerSupervisorCompilationAndMissingInputs(t *testing.T) {
	s, m := schedulerHarness(t, fixtureRunner(func(_ context.Context, c Call) (AgentOutput, error) { return fixtureOutput(c), nil }))
	s.planner = fixturePlanner(func(_ context.Context, _ service.Meta, text string, _ Bindings) ([]TaskSpec, error) {
		return []TaskSpec{readSpec("professional")}, nil
	})
	v, e := s.Submit(context.Background(), m, uuid.NewString(), SubmitInput{Instruction: "fixture Supervisor instruction"})
	if e != nil {
		t.Fatal(e)
	}
	wave(t, s)
	v = current(t, s, m, v.Job.ID)
	if len(v.Tasks) != 2 || len(v.Dependencies) != 1 {
		t.Fatal("Supervisor graph was not persisted")
	}
	wave(t, s)
	if current(t, s, m, v.Job.ID).Job.Status != "succeeded" {
		t.Fatal("professional not called")
	}
	s.planner = fixturePlanner(func(_ context.Context, _ service.Meta, _ string, _ Bindings) ([]TaskSpec, error) {
		return []TaskSpec{{Key: "execute", AgentType: "savings", Tool: "execute_transfer", Parameters: json.RawMessage(`{}`)}}, nil
	})
	v, e = s.Submit(context.Background(), m, uuid.NewString(), SubmitInput{Instruction: "fixture invalid model output"})
	if e != nil {
		t.Fatal(e)
	}
	wave(t, s)
	if current(t, s, m, v.Job.ID).Job.Status != "failed" {
		t.Fatal("invalid Supervisor graph accepted")
	}
	v = submitFixture(t, s, m, []TaskSpec{{Key: "budget", AgentType: "billing", Tool: "compare_budget", Parameters: json.RawMessage(`{}`)}}, nil)
	wave(t, s)
	v = current(t, s, m, v.Job.ID)
	if v.Job.Status != "awaiting_input" {
		t.Fatal("missing budget period was invented")
	}
	v, e = s.Inputs(context.Background(), m, v.Job.ID, InputsInput{TaskKey: "budget", ExpectedVersion: v.Job.Version, Parameters: json.RawMessage(`{"from":"2026-01-01T00:00:00Z","to":"2026-02-01T00:00:00Z"}`)})
	if e != nil {
		t.Fatal(e)
	}
	wave(t, s)
	if current(t, s, m, v.Job.ID).Job.Status != "succeeded" {
		t.Fatal("input completion failed")
	}
}

func TestSchedulerGoalCompilerRestrictions(t *testing.T) {
	s, m := schedulerHarness(t, nil)
	n := int64(3)
	parsed := goal.Parsed{GoalType: "healthy_lifestyle", PeriodMonths: &n, Currency: "CNY", Constraints: []goal.Constraint{}, ProhibitedOperations: []string{}, MissingInformation: []string{}, Evidence: map[string]string{}}
	plan, e := goal.Compile(parsed, goal.FinancialContext{}, json.RawMessage(`{}`))
	if e != nil {
		t.Fatal(e)
	}
	raw, _ := json.Marshal(plan)
	g := goal.Goal{Base: entity.Base{UserID: m.UserID}, CurrentVersion: 1, Status: "active"}
	if e = s.repo.db.Create(&g).Error; e != nil {
		t.Fatal(e)
	}
	version := goal.GoalVersion{Base: entity.Base{UserID: m.UserID}, GoalID: g.ID, Version: 1, Text: "fixture parsed compiler goal", ContextJSON: json.RawMessage(`{}`), ParsedJSON: json.RawMessage(`{}`), ParseStatus: "parsed", TraceID: m.TraceID, AuditID: uuid.NewString()}
	if e = s.repo.db.Create(&version).Error; e != nil {
		t.Fatal(e)
	}
	source := goal.TaskPlan{Base: entity.Base{UserID: m.UserID}, GoalID: g.ID, GoalVersionID: version.ID, Version: 1, Status: plan.Status, PlanJSON: raw, TraceID: m.TraceID, AuditID: uuid.NewString()}
	if e = s.repo.db.Create(&source).Error; e != nil {
		t.Fatal(e)
	}
	v, e := s.Submit(context.Background(), m, uuid.NewString(), SubmitInput{TaskPlanID: fmt.Sprint(source.ID)})
	if e != nil {
		t.Fatal(e)
	}
	if len(v.Tasks) != len(plan.Tasks) {
		t.Fatal("compiler tasks lost")
	}
	blocked := 0
	for _, task := range v.Tasks {
		if task.ProtectedBlocked {
			blocked++
			if _, e = s.Inputs(context.Background(), m, v.Job.ID, InputsInput{TaskKey: task.Key, ExpectedVersion: v.Job.Version, Parameters: json.RawMessage(`{}`)}); e == nil {
				t.Fatal("compiler safety was overridden")
			}
		}
	}
	if blocked == 0 {
		t.Fatal("fixture did not exercise compiler safety")
	}
	s.repo.db.Model(&g).Update("current_version", 2)
	if _, e = s.Submit(context.Background(), m, uuid.NewString(), SubmitInput{TaskPlanID: fmt.Sprint(source.ID)}); entity.Code(e) != "VERSION_CONFLICT" {
		t.Fatal("stale compiler plan accepted", e)
	}
}
func TestSchedulerRevokedOwnerDoesNotBlockOtherUsers(t *testing.T) {
	s, m := schedulerHarness(t, fixtureRunner(func(_ context.Context, c Call) (AgentOutput, error) { return fixtureOutput(c), nil }))
	first := submitFixture(t, s, m, []TaskSpec{readSpec("revoked")}, nil)
	if e := s.repo.db.Model(&entity.User{}).Where("id=?", m.UserID).Update("status", "inactive").Error; e != nil {
		t.Fatal(e)
	}
	other := m
	other.UserID++
	if e := s.repo.db.Create(&entity.User{ID: other.UserID, DisplayName: "active module7 fixture", Status: "active"}).Error; e != nil {
		t.Fatal(e)
	}
	second := submitFixture(t, s, other, []TaskSpec{readSpec("active")}, nil)
	wave(t, s)
	if current(t, s, other, second.Job.ID).Job.Status != "succeeded" {
		t.Fatal("revoked queue head blocked active user")
	}
	var task AgentTask
	s.repo.db.Where("job_id=?", first.Job.ID).First(&task)
	if task.Attempts != 0 {
		t.Fatal("revoked task executed")
	}
}
