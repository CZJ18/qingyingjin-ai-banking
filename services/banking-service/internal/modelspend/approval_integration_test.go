//go:build integration

package modelspend

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func approvalFixture(t *testing.T, s *Service) (Approval, context.Context) {
	t.Helper()
	hash, _ := s.policy.Hash()
	a := Approval{ID: "synthetic-approval", RunID: "synthetic-current-run", ProviderOrigin: "https://api.deepseek.com/v1", Model: s.policy.Model, ProjectID: s.policy.ProjectID, Database: "synthetic_test", Currency: s.policy.Currency,
		Amount: FormatMoney(s.reserveUnits * 3), PolicySHA256: hash, Scope: "SQL only synthetic approval; no supplier requests authorized", UserID: 42, TaskRequestSHA256: strings.Repeat("b", 64), WorkflowID: "123", WorkflowSHA256: strings.Repeat("c", 64),
		ValidFrom: time.Now().Add(-time.Minute).UTC().Format(time.RFC3339), ValidUntil: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), MaxRequests: 3, MaxConcurrent: 1, ContextTokens: s.policy.ContextTokens, MaxOutputTokens: s.policy.MaxOutputTokens}
	ctx := WithProviderOrigin(WithWorkflowBinding(WithUser(context.Background(), 42), WorkflowBinding{TaskRequestDigest: a.TaskRequestSHA256, WorkflowID: a.WorkflowID, WorkflowSHA256: a.WorkflowSHA256, PolicySHA256: hash, Model: a.Model, ContextTokens: a.ContextTokens, MaxOutputTokens: a.MaxOutputTokens}), a.ProviderOrigin)
	if e := s.ProvisionUserLimit(ctx, 42, s.policy.Limit); e != nil {
		t.Fatal(e)
	}
	if e := s.UseApproval(a, a.RunID, a.Database); e != nil {
		t.Fatal(e)
	}
	return a, ctx
}

func TestSQLApprovalConcurrencyUnknownAndRestartAreDurable(t *testing.T) {
	s, conn := spendHarness(t, 100)
	a, ctx := approvalFixture(t, s)
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted, denied := 0, 0
	var id string
	for i := range 8 {
		wg.Go(func() {
			v, e := s.Reserve(ctx, fmt.Sprint("parallel-", i), strings.Repeat("a", 64))
			mu.Lock()
			defer mu.Unlock()
			if e == nil {
				accepted++
				id = v.ID
			} else if errors.Is(e, ErrApprovalQuota) {
				denied++
			} else {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	if accepted != 1 || denied != 7 {
		t.Fatal(accepted, denied)
	}
	if e := s.Unknown(ctx, id); e != nil {
		t.Fatal(e)
	}
	fresh, e := New(conn.DB, s.policy)
	if e != nil {
		t.Fatal(e)
	}
	if e = fresh.UseApproval(a, a.RunID, a.Database); e != nil {
		t.Fatal(e)
	}
	if _, e = fresh.Reserve(ctx, "after-restart", strings.Repeat("a", 64)); !errors.Is(e, ErrApprovalQuota) {
		t.Fatal("unknown slot released after restart", e)
	}
	var before, after int64
	conn.DB.Model(&Event{}).Where("project_id=?", a.ProjectID).Count(&before)
	w, e := fresh.ApprovalBudget(ctx)
	if e != nil || w.RemainingRequests != 2 || w.RemainingConcurrent != 0 {
		t.Fatal(w, e)
	}
	conn.DB.Model(&Event{}).Where("project_id=?", a.ProjectID).Count(&after)
	if before != after {
		t.Fatal("preflight mutated ledger")
	}
}

func TestSQLApprovalRequestCapReplayAndScope(t *testing.T) {
	s, conn := spendHarness(t, 100)
	a, ctx := approvalFixture(t, s)
	a.MaxRequests = 1
	a.MaxConcurrent = 1
	a.AdditionalWorkflows = map[string]string{"456": strings.Repeat("d", 64)}
	if e := s.UseApproval(a, a.RunID, a.Database); e != nil {
		t.Fatal(e)
	}
	first, e := s.Reserve(ctx, "once", strings.Repeat("a", 64))
	if e != nil {
		t.Fatal(e)
	}
	report := Report{Model: s.policy.Model, ProviderID: "synthetic-provider", Usage: Usage{PromptTokens: 2, CompletionTokens: 1, TotalTokens: 3}, ResponseBody: []byte(`{"synthetic":true}`), ContentType: "application/json"}
	if e = s.Observe(ctx, first.ID, report); e != nil {
		t.Fatal(e)
	}
	replay, e := s.Reserve(ctx, "once", strings.Repeat("a", 64))
	if e != nil || replay.ID != first.ID {
		t.Fatal("replay consumed quota", e)
	}
	fresh, e := New(conn.DB, s.policy)
	if e != nil {
		t.Fatal(e)
	}
	fresh.UseApproval(a, a.RunID, a.Database)
	additional := WithWorkflowBinding(ctx, WorkflowBinding{TaskRequestDigest: a.TaskRequestSHA256, WorkflowID: "456", WorkflowSHA256: a.AdditionalWorkflows["456"], PolicySHA256: a.PolicySHA256, Model: a.Model, ContextTokens: a.ContextTokens, MaxOutputTokens: a.MaxOutputTokens})
	if _, e = fresh.Reserve(additional, "explanation-workflow", strings.Repeat("a", 64)); !errors.Is(e, ErrApprovalQuota) {
		t.Fatal("additional workflow received a separate quota", e)
	}
	if _, e = fresh.Reserve(ctx, "twice", strings.Repeat("a", 64)); !errors.Is(e, ErrApprovalQuota) {
		t.Fatal("request limit bypassed after response/restart", e)
	}
	if _, e = fresh.Reserve(WithUser(ctx, 43), "other-owner", strings.Repeat("a", 64)); !errors.Is(e, ErrApproval) {
		t.Fatal("approval owner mismatch", e)
	}
	changed := a
	changed.MaxRequests = 2
	fresh.UseApproval(changed, changed.RunID, changed.Database)
	if _, e = fresh.Reserve(ctx, "larger-approval", strings.Repeat("a", 64)); !errors.Is(e, ErrConflict) {
		t.Fatal("approval amended to reset cap", e)
	}
}

func TestSQLApprovalMoneyCapAppliesEvenWithRequestsRemaining(t *testing.T) {
	s, _ := spendHarness(t, 100)
	a, ctx := approvalFixture(t, s)
	a.Amount = FormatMoney(s.reserveUnits)
	if e := s.UseApproval(a, a.RunID, a.Database); e != nil {
		t.Fatal(e)
	}
	first, e := s.Reserve(ctx, "one-risk-bound", strings.Repeat("a", 64))
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Observe(ctx, first.ID, Report{Model: s.policy.Model, ProviderID: "synthetic-budget-provider", Usage: Usage{PromptTokens: 2, CompletionTokens: 1, TotalTokens: 3}, ResponseBody: []byte(`{"synthetic":true}`), ContentType: "application/json"}); e != nil {
		t.Fatal(e)
	}
	window, e := s.ApprovalBudget(ctx)
	if e != nil || window.RemainingRequests != 2 || window.RemainingConcurrent != 1 || window.RemainingBudget != "0.00000000" {
		t.Fatal(window, e)
	}
	if _, e = s.Reserve(ctx, "cannot-use-unused-requests", strings.Repeat("a", 64)); !errors.Is(e, ErrApprovalQuota) {
		t.Fatal("cost bound ignored", e)
	}
}
