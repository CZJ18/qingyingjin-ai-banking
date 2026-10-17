package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/risk"
	"github.com/qingyingjin/banking-service/internal/service"
	"strconv"
	"time"
)

type Service struct {
	repo      *repository.Core
	gateway   risk.SubscriptionGateway
	simulator Simulator
}

// Simulator performs only local validation, inside the same database transaction.
// Implementations must have no external merchant or financial side effects.
type Simulator interface {
	ValidateLocalCancellation(context.Context, *entity.Subscription) error
}
type LocalSimulator struct{}

func (LocalSimulator) ValidateLocalCancellation(_ context.Context, s *entity.Subscription) error {
	if s == nil || !s.IsSimulated || s.Status != entity.SubscriptionActive {
		return entity.Fail("CANCEL_FAILED", "local subscription is not cancellable")
	}
	return nil
}
func New(r *repository.Core, g risk.SubscriptionGateway, sim Simulator) *Service {
	return &Service{r, g, sim}
}
func (s *Service) Configured() bool { return s != nil && s.repo != nil }

type DetectInput struct {
	From      string `json:"from,omitempty"`
	To        string `json:"to,omitempty"`
	AccountID string `json:"account_id,omitempty"`
}
type IDInput struct {
	SubscriptionID string `json:"subscription_id"`
}
type IntentInput struct {
	SubscriptionID  string `json:"subscription_id"`
	ExpectedVersion int64  `json:"expected_version"`
}
type ManageInput struct {
	ExpectedVersion        int64  `json:"expected_version"`
	ConfirmLocalSimulation bool   `json:"confirm_local_simulation"`
	LastUsedAt             *int64 `json:"last_used_at,omitempty"`
}
type ConfirmInput struct {
	Decision         string `json:"decision"`
	ConfirmationHash string `json:"confirmation_hash"`
}
type Detail struct {
	PriceChanged     bool                              `json:"price_changed"`
	NextChargeStatus string                            `json:"next_charge_status"`
	Subscription     entity.Subscription               `json:"subscription"`
	Payments         []entity.SubscriptionPayment      `json:"payments"`
	OperationHistory []entity.SubscriptionActionIntent `json:"operation_history"`
	Forecast         ForecastResult                    `json:"forecast"`
	UsageStatus      string                            `json:"usage_status"`
	PossiblyIdle     bool                              `json:"possibly_idle"`
	UsageEvidence    string                            `json:"usage_evidence"`
}
type DuplicateGroup struct {
	SubscriptionIDs    []string `json:"subscription_ids"`
	Reason             string   `json:"reason"`
	RequiresUserReview bool     `json:"requires_user_review"`
}
type Suggestion struct {
	SubscriptionID           string `json:"subscription_id"`
	Action                   string `json:"action"`
	Reason                   string `json:"reason"`
	RequiresUserConfirmation bool   `json:"requires_user_confirmation"`
}
type Report struct {
	ID                        int64            `json:"id,string"`
	UserID                    int64            `json:"user_id,string"`
	Tool                      string           `json:"tool"`
	Candidates                []Candidate      `json:"candidates"`
	Subscriptions             []Detail         `json:"subscriptions"`
	DuplicateGroups           []DuplicateGroup `json:"duplicate_groups"`
	Suggestions               []Suggestion     `json:"suggestions"`
	TotalRecordedExpenseMinor int64            `json:"total_recorded_expense_minor"`
	ActiveRecurringForecast   ForecastResult   `json:"active_recurring_forecast"`
	CancelledAvoidedSpend     ForecastResult   `json:"cancelled_avoided_spend"`
	EvidenceTransactionIDs    []string         `json:"evidence_transaction_ids"`
	FinancialExecutionEnabled bool             `json:"financial_execution_enabled"`
	Currency                  string           `json:"currency"`
	Scope                     string           `json:"scope"`
	TraceID                   string           `json:"trace_id"`
	TaskID                    string           `json:"task_id"`
	AuditID                   string           `json:"audit_id"`
	CreatedAt                 int64            `json:"created_at"`
}

var Tools = []string{"detect_subscriptions", "get_subscription_details", "analyze_subscription_cost", "suggest_subscription_actions", "create_cancel_intent", "get_subscription_status"}

func ID(raw string) (int64, error) {
	n, e := strconv.ParseInt(raw, 10, 64)
	if e != nil || n <= 0 || fmt.Sprint(n) != raw {
		return 0, entity.Fail("INVALID_ARGUMENT", "positive canonical subscription ID required")
	}
	return n, nil
}
func audit(m service.Meta, action string, id int64, outcome, code string) *entity.Audit {
	return &entity.Audit{ID: uuid.NewString(), UserID: m.UserID, Action: action, ResourceID: id, Outcome: outcome, ErrorCode: code, TraceID: m.TraceID, TaskID: m.TaskID}
}
func (s *Service) denied(ctx context.Context, m service.Meta, action string, id int64, err error) {
	if err == nil {
		return
	}
	safe, cancel := context.WithTimeout(context.WithoutCancel(ctx), 3*time.Second)
	defer cancel()
	// Best effort when the dependency itself has failed; no foreign resource data.
	_ = s.repo.Transaction(safe, m.UserID, func(tx *repository.Tx) error {
		if e := tx.RequireUser(); e != nil {
			return e
		}
		return tx.Audit(audit(m, action, id, "denied", entity.Code(err)))
	})
}
func (s *Service) Analyze(ctx context.Context, m service.Meta, tool string, in DetectInput) (out Report, err error) {
	defer func() { s.denied(ctx, m, "subscription."+tool, 0, err) }()
	if tool != "detect_subscriptions" && tool != "analyze_subscription_cost" && tool != "suggest_subscription_actions" {
		return out, entity.Fail("INVALID_ARGUMENT", "unknown subscription analysis tool")
	}
	now := time.Now().UTC()
	from := now.AddDate(-3, 0, 0)
	to := now
	var account int64
	if in.AccountID != "" {
		account, err = ID(in.AccountID)
		if err != nil {
			return out, err
		}
	}
	if in.From != "" {
		from, err = time.Parse(time.RFC3339, in.From)
		if err != nil {
			return out, entity.Fail("INVALID_ARGUMENT", "from must be RFC3339")
		}
	}
	if in.To != "" {
		to, err = time.Parse(time.RFC3339, in.To)
		if err != nil {
			return out, entity.Fail("INVALID_ARGUMENT", "to must be RFC3339")
		}
	}
	if !to.After(from) || to.Sub(from) > 1096*24*time.Hour || from.Year() < 1971 || to.Year() > 2100 {
		return out, entity.Fail("INVALID_ARGUMENT", "detection period must be positive and <=1096 days, years 1971..2100")
	}
	if tool != "detect_subscriptions" && (in.From != "" || in.To != "" || in.AccountID != "") {
		return out, entity.Fail("INVALID_ARGUMENT", "cost and suggestions cover all currently linked payment evidence; period fields are detection only")
	}
	err = s.repo.Transaction(ctx, m.UserID, func(tx *repository.Tx) error {
		if e := tx.LockSubscriptionOwner(); e != nil {
			return e
		}
		out = Report{UserID: m.UserID, Tool: tool, Candidates: []Candidate{}, Subscriptions: []Detail{}, DuplicateGroups: []DuplicateGroup{}, Suggestions: []Suggestion{}, EvidenceTransactionIDs: []string{}, Currency: "CNY", Scope: "currently_linked_payment_evidence; not all bank expenses; forecasts are conditional and not available funds", TraceID: m.TraceID, TaskID: m.TaskID, CreatedAt: now.UnixMilli(), ActiveRecurringForecast: ForecastResult{Kind: "conditional_if_all_active_cancelled", Rounding: "sum individually floored forecasts"}, CancelledAvoidedSpend: ForecastResult{Kind: "estimated_avoided_future_spend", Rounding: "sum individually floored forecasts"}}
		if tool == "detect_subscriptions" {
			rows, e := tx.SubscriptionRows(from.UnixMilli(), to.UnixMilli(), account)
			if e != nil {
				return e
			}
			candidates, e := Detect(rows)
			if e != nil {
				return e
			}
			index := map[string]entity.Transaction{}
			for _, row := range rows {
				index[fmt.Sprint(row.ID)] = row
			}
			for j := range candidates {
				c := &candidates[j]
				sub, e := tx.StoreSubscriptionCandidate(&entity.Subscription{Base: entity.Base{UserID: m.UserID}, BankAccountID: c.AccountID, Fingerprint: c.Fingerprint, MerchantName: c.MerchantName, Category: c.Category, BillingInterval: c.Interval, AmountMinor: c.AmountMinor, Currency: "CNY", NextBillingAt: c.NextBillingAt, ConfidenceBPS: c.ConfidenceBPS, Status: "candidate", Version: 1, IsSimulated: true})
				if e != nil {
					return e
				}
				c.SubscriptionID = fmt.Sprint(sub.ID)
				dupe := map[string]bool{}
				for _, id := range c.DuplicateTransactionIDs {
					dupe[id] = true
				}
				for _, id := range c.EvidenceTransactionIDs {
					row := index[id]
					p := &entity.SubscriptionPayment{Base: entity.Base{UserID: m.UserID}, SubscriptionID: sub.ID, TransactionID: row.ID, AmountMinor: row.AmountMinor, OccurredAt: row.OccurredAt, IsPossibleDuplicate: dupe[id]}
					if e = tx.StoreSubscriptionPayment(p); e != nil {
						return e
					}
				}
			}
			out.Candidates = candidates
		}
		subs, e := tx.SubscriptionList()
		if e != nil {
			return e
		}
		seen := map[int64]bool{}
		totalEvidence := 0
		for _, sub := range subs {
			d, e := detail(tx, sub, now.UnixMilli())
			if e != nil {
				return e
			}
			out.Subscriptions = append(out.Subscriptions, d)
			totalEvidence += len(d.Payments)
			if totalEvidence > 10000 {
				return entity.Fail("ANALYSIS_LIMIT", "report exceeds 10000 payment records")
			}
			for _, p := range d.Payments {
				if !seen[p.TransactionID] {
					seen[p.TransactionID] = true
					out.TotalRecordedExpenseMinor += p.AmountMinor
					out.EvidenceTransactionIDs = append(out.EvidenceTransactionIDs, fmt.Sprint(p.TransactionID))
				}
			}
			if sub.Status == entity.SubscriptionActive {
				addForecast(&out.ActiveRecurringForecast, d.Forecast)
			}
			if sub.Status == entity.SubscriptionCancelled {
				addForecast(&out.CancelledAvoidedSpend, d.Forecast)
			}
			reason := "Review service usage with the user; payment evidence alone cannot establish inactivity"
			if d.PossiblyIdle {
				reason = "User-provided last_used_at is over 90 days ago; reconfirm current usage before proposing cancellation"
			}
			action := "review_usage"
			if sub.Status == "candidate" {
				action = "confirm_local_management"
			}
			if sub.Status != entity.SubscriptionCancelled {
				out.Suggestions = append(out.Suggestions, Suggestion{fmt.Sprint(sub.ID), action, reason, true})
			}
		}
		for i, a := range subs {
			if a.Status == entity.SubscriptionCancelled {
				continue
			}
			for _, b := range subs[i+1:] {
				if b.Status == entity.SubscriptionCancelled {
					continue
				}
				if normalizeMerchant(a.MerchantName) == normalizeMerchant(b.MerchantName) {
					if len(out.DuplicateGroups) >= 1000 {
						return entity.Fail("ANALYSIS_LIMIT", "more than 1000 possible duplicate groups")
					}
					out.DuplicateGroups = append(out.DuplicateGroups, DuplicateGroup{[]string{fmt.Sprint(a.ID), fmt.Sprint(b.ID)}, "Same normalized merchant on multiple accounts; may be distinct family or service plans, requires user review", true})
				}
			}
		}
		a := audit(m, "subscription."+tool, 0, "success", "")
		out.AuditID = a.ID
		rec := &entity.SubscriptionAnalysis{Base: entity.Base{UserID: m.UserID}, Payload: []byte("{}"), TraceID: m.TraceID, TaskID: m.TaskID, AuditID: a.ID}
		if e = tx.Insert(rec); e != nil {
			return e
		}
		out.ID = rec.ID
		a.ResourceID = rec.ID
		if e = ValidateReport(out); e != nil {
			return e
		}
		if e = tx.SetSubscriptionAnalysis(rec, out); e != nil {
			return e
		}
		return tx.Audit(a)
	})
	return out, repository.DBError(err)
}
func addForecast(a *ForecastResult, b ForecastResult) {
	a.MonthlyMinor += b.MonthlyMinor
	a.ThreeMonthMinor += b.ThreeMonthMinor
	a.AnnualMinor += b.AnnualMinor
}
func detail(tx *repository.Tx, sub entity.Subscription, now int64) (Detail, error) {
	d := Detail{Subscription: sub, UsageStatus: "unknown", UsageEvidence: "No service-usage evidence; bank payments are not usage records"}
	var e error
	d.Payments, e = tx.SubscriptionPayments(sub.ID)
	if e != nil {
		return d, e
	}
	amounts := map[int64]bool{}
	for _, p := range d.Payments {
		amounts[p.AmountMinor] = true
	}
	d.PriceChanged = len(amounts) > 1
	d.NextChargeStatus = "estimated_not_guaranteed"
	if sub.NextBillingAt <= now {
		d.NextChargeStatus = "overdue_prediction_needs_user_review"
	}
	if sub.Status == entity.SubscriptionCancelled {
		d.NextChargeStatus = "locally_cancelled_no_merchant_guarantee"
	}
	d.OperationHistory, e = tx.SubscriptionHistory(sub.ID)
	if e != nil {
		return d, e
	}
	d.Forecast, e = Forecast(sub.AmountMinor, sub.BillingInterval)
	if sub.Status == entity.SubscriptionActive {
		d.Forecast.Kind = "conditional_if_cancelled"
	}
	if sub.Status == "candidate" {
		d.Forecast.Kind = "speculative_pattern_not_confirmed_contract"
	}
	if sub.LastUsedAt != nil {
		d.UsageStatus = "user_reported"
		d.PossiblyIdle = now-*sub.LastUsedAt > 90*24*int64(time.Hour/time.Millisecond)
		d.UsageEvidence = "last_used_at explicitly supplied by authenticated user; may be stale, requires reconfirmation"
	}
	return d, e
}
func (s *Service) Get(ctx context.Context, m service.Meta, id int64) (d Detail, err error) {
	defer func() { s.denied(ctx, m, "subscription.read", id, err) }()
	err = s.repo.Transaction(ctx, m.UserID, func(tx *repository.Tx) error {
		if e := tx.RequireUser(); e != nil {
			return e
		}
		sub, e := tx.LockSubscription(id)
		if e != nil {
			return e
		}
		d, e = detail(tx, *sub, time.Now().UnixMilli())
		return e
	})
	return d, err
}
func (s *Service) GetIntent(ctx context.Context, m service.Meta, id int64) (i *entity.SubscriptionActionIntent, err error) {
	defer func() { s.denied(ctx, m, "subscription.read", id, err) }()
	err = s.repo.Transaction(ctx, m.UserID, func(tx *repository.Tx) error {
		if e := tx.RequireUser(); e != nil {
			return e
		}
		i, err = tx.LockSubscriptionIntent(id)
		return err
	})
	return i, err
}
func (s *Service) GetAnalysis(ctx context.Context, m service.Meta, id int64) (json.RawMessage, error) {
	return s.repo.GetSubscriptionAnalysis(ctx, m.UserID, id)
}
func (s *Service) Manage(ctx context.Context, m service.Meta, key string, id int64, in ManageInput) (out entity.Subscription, err error) {
	defer func() { s.denied(ctx, m, "subscription.manage", id, err) }()
	if e := risk.CheckSensitive(ctx, s.gateway, m.UserID, "subscription_manage", map[string]any{"subscription_id": fmt.Sprint(id), "input": in}); e != nil {
		return out, e
	}
	if _, trusted := s.gateway.(risk.SensitiveGuard); !trusted && !in.ConfirmLocalSimulation {
		return out, entity.Fail("APPROVAL_REQUIRED", "explicit confirm_local_simulation required")
	}
	if in.ExpectedVersion <= 0 || in.LastUsedAt != nil && (*in.LastUsedAt <= 0 || *in.LastUsedAt > time.Now().UnixMilli()) {
		return out, entity.Fail("INVALID_ARGUMENT", "version and user-reported usage time invalid")
	}
	return repository.Atomic(s.repo, ctx, m.UserID, "subscription.manage", key, []any{id, in}, func(tx *repository.Tx) (entity.Subscription, error) {
		if e := tx.LockSubscriptionOwner(); e != nil {
			return out, e
		}
		sub, e := tx.LockSubscription(id)
		if e != nil {
			return out, e
		}
		if sub.Version != in.ExpectedVersion {
			return out, entity.Fail("VERSION_CONFLICT", "subscription changed; review current version")
		}
		if sub.Status == entity.SubscriptionCancelled {
			return out, entity.Fail("STATE_CONFLICT", "cancelled subscriptions cannot be reactivated")
		}
		sub.Status = entity.SubscriptionActive
		sub.Version++
		if in.LastUsedAt != nil {
			sub.LastUsedAt = in.LastUsedAt
		}
		if e = tx.SaveSubscription(sub); e != nil {
			return out, e
		}
		if e = tx.Audit(audit(m, "subscription.manage", id, "approved", "")); e != nil {
			return out, e
		}
		return *sub, nil
	})
}
func (s *Service) CreateIntent(ctx context.Context, m service.Meta, key string, in IntentInput) (out entity.SubscriptionActionIntent, err error) {
	id, e := ID(in.SubscriptionID)
	if e != nil {
		return out, e
	}
	defer func() { s.denied(ctx, m, "subscription.cancel.propose", id, err) }()
	if in.ExpectedVersion <= 0 {
		return out, entity.Fail("INVALID_ARGUMENT", "expected_version required")
	}
	return repository.Atomic(s.repo, ctx, m.UserID, "subscription.cancel.propose", key, in, func(tx *repository.Tx) (entity.SubscriptionActionIntent, error) {
		if e := tx.LockSubscriptionOwner(); e != nil {
			return out, e
		}
		sub, e := tx.LockSubscription(id)
		if e != nil {
			return out, e
		}
		if sub.Version != in.ExpectedVersion {
			return out, entity.Fail("VERSION_CONFLICT", "subscription changed; review current version")
		}
		if s.gateway == nil {
			return out, entity.Fail("RISK_REJECTED", "subscription safety gateway required")
		}
		if e = s.gateway.CheckSubscriptionProposal(ctx, m.UserID, sub); e != nil {
			return out, e
		}
		a := audit(m, "subscription.cancel.propose", sub.ID, "pending", "")
		out = entity.SubscriptionActionIntent{Base: entity.Base{UserID: m.UserID}, SubscriptionID: sub.ID, SubscriptionVersion: sub.Version, Action: "cancel", Status: "pending_approval", ExpiresAt: time.Now().Add(15 * time.Minute).UnixMilli(), TraceID: m.TraceID, TaskID: m.TaskID, AuditID: a.ID}
		if e = tx.Insert(&out); e != nil {
			return out, e
		}
		out.ConfirmationHash = entity.SubscriptionDigest(&out)
		if e = tx.SaveSubscriptionIntent(&out); e != nil {
			return out, e
		}
		a.ResourceID = out.ID
		if e = tx.Audit(a); e != nil {
			return out, e
		}
		return out, nil
	})
}
func (s *Service) Confirm(ctx context.Context, m service.Meta, id int64, in ConfirmInput) (out entity.SubscriptionActionIntent, err error) {
	defer func() { s.denied(ctx, m, "subscription.cancel.confirm", id, err) }()
	if (in.Decision != "approve" && in.Decision != "reject") || len(in.ConfirmationHash) != 64 {
		return out, entity.Fail("INVALID_ARGUMENT", "explicit approve/reject and exact confirmation_hash required")
	}
	err = s.repo.Transaction(ctx, m.UserID, func(tx *repository.Tx) error {
		if e := tx.LockSubscriptionOwner(); e != nil {
			return e
		}
		i, e := tx.LockSubscriptionIntent(id)
		if e != nil {
			return e
		}
		if in.ConfirmationHash != i.ConfirmationHash || i.ConfirmationHash != entity.SubscriptionDigest(i) {
			return entity.Fail("APPROVAL_REQUIRED", "confirmation does not match immutable intent")
		}
		if i.Status != "pending_approval" {
			a, e := tx.SubscriptionApproval(i.ID)
			if e == nil && a.Decision == in.Decision && a.ConfirmationHash == in.ConfirmationHash {
				out = *i
				return nil
			}
			return entity.Fail("STATE_CONFLICT", "intent is already terminal")
		}
		now := time.Now().UnixMilli()
		sub, e := tx.LockSubscription(i.SubscriptionID)
		if e != nil {
			return e
		}
		terminal := ""
		failure := ""
		if i.ExpiresAt <= now {
			terminal = "expired"
			failure = "APPROVAL_EXPIRED"
		} else if sub.Version != i.SubscriptionVersion || sub.Status != entity.SubscriptionActive {
			terminal = "failed"
			failure = "VERSION_CONFLICT"
		}
		if terminal != "" {
			attempt := &entity.SubscriptionApproval{Base: entity.Base{UserID: m.UserID}, IntentID: i.ID, Decision: in.Decision, ConfirmationHash: in.ConfirmationHash, ConsumedAt: &now}
			if e = tx.Insert(attempt); e != nil {
				return e
			}
			i.Status = terminal
			i.FailureCode = failure
			if e = tx.SaveSubscriptionIntent(i); e != nil {
				return e
			}
			out = *i
			return tx.Audit(audit(m, "subscription.cancel.confirm", id, terminal, failure))
		}
		a := &entity.SubscriptionApproval{Base: entity.Base{UserID: m.UserID}, IntentID: i.ID, Decision: in.Decision, ConfirmationHash: in.ConfirmationHash}
		if e = tx.Insert(a); e != nil {
			return e
		}
		if in.Decision == "reject" {
			i.Status = "rejected"
		} else {
			if s.gateway == nil {
				return entity.Fail("RISK_REJECTED", "subscription safety gateway required")
			}
			if e = s.gateway.CheckSubscriptionCancel(ctx, m.UserID, sub, i, a); e != nil {
				return e
			}
			if s.simulator == nil {
				return entity.Fail("DEPENDENCY_UNAVAILABLE", "local cancellation simulator required")
			}
			if e = s.simulator.ValidateLocalCancellation(ctx, sub); e != nil {
				if errors.Is(e, context.DeadlineExceeded) || errors.Is(e, context.Canceled) {
					return e
				}
				i.Status = "failed"
				i.FailureCode = "CANCEL_FAILED"
			} else {
				sub.Status = entity.SubscriptionCancelled
				sub.Version++
				sub.CancelledAt = &now
				if e = tx.SaveSubscription(sub); e != nil {
					return e
				}
				i.Status = "executed"
				i.ExecutedAt = &now
			}
		}
		if e = tx.ConsumeSubscriptionApproval(i.ID, now); e != nil {
			return e
		}
		if e = tx.SaveSubscriptionIntent(i); e != nil {
			return e
		}
		out = *i
		return tx.Audit(audit(m, "subscription.cancel.confirm", id, i.Status, i.FailureCode))
	})
	return out, repository.DBError(err)
}
