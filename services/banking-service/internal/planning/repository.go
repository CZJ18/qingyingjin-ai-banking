package planning

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/billing"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/goal"
	coreRepo "github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/service"
	"github.com/qingyingjin/banking-service/internal/subscription"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strconv"
	"time"
)

type Repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db} }
func digest(v any) string                   { raw, _ := json.Marshal(v); return fmt.Sprintf("%x", sha256.Sum256(raw)) }
func requireUser(tx *gorm.DB, user int64) error {
	var u entity.User
	return coreRepo.DBError(tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND status='active'", user).First(&u).Error)
}
func source(s *Snapshot, kind string, id, version int64) {
	s.Sources = append(s.Sources, Source{kind, strconv.FormatInt(id, 10), version})
}

// Snapshot reads existing module tables inside the caller's SQL transaction;
// the reused Billing analyzer owns classification and all budget arithmetic.
func snapshot(tx *gorm.DB, user int64, settings Settings, now time.Time) (Snapshot, error) {
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	s := Snapshot{Period: start.Format("2006-01"), Settings: settings, Goals: []GoalFunds{}, Sources: []Source{}, Blockers: []string{}, ReviewNotes: []string{}}
	if settings.Period != s.Period {
		s.Blockers = append(s.Blockers, "settings from a different UTC month require review")
	}
	s.Settings.Cashflows = []Cashflow{}
	for _, c := range settings.Cashflows {
		if c.DueAt < end.UnixMilli() {
			s.Settings.Cashflows = append(s.Settings.Cashflows, c)
		}
	}
	var accounts []entity.BankAccount
	if err := tx.Where("user_id=? AND id IN ?", user, settings.AccountIDs).Order("id").Find(&accounts).Error; err != nil {
		return s, err
	}
	if len(accounts) != len(settings.AccountIDs) {
		return s, entity.Fail("NOT_FOUND", "planning account unavailable")
	}
	for _, a := range accounts {
		if a.SavingsGoalID != nil || a.AccountType == "savings" || a.Currency != "CNY" || !a.IsSimulated {
			return s, entity.Fail("INVALID_ARGUMENT", "only simulated CNY daily accounts may fund advice")
		}
		if a.Status == "active" {
			s.BalanceMinor += a.BalanceMinor
		}
		source(&s, "account", a.ID, a.Version)
	}
	var allAccounts []entity.BankAccount
	if err := tx.Where("user_id=?", user).Order("id").Find(&allAccounts).Error; err != nil {
		return s, err
	}
	var transactions []entity.Transaction
	if err := tx.Where("user_id=? AND kind='external' AND occurred_at>=? AND occurred_at<?", user, start.AddDate(0, -1, 0).UnixMilli(), end.UnixMilli()).Order("occurred_at,id").Limit(billing.MaxRows + 1).Find(&transactions).Error; err != nil {
		return s, err
	}
	if len(transactions) > billing.MaxRows {
		return s, entity.Fail("ANALYSIS_LIMIT", "monthly financial evidence exceeds limit")
	}
	for _, t := range transactions {
		source(&s, "transaction", t.ID, t.UpdatedAt)
	}
	var budgets []entity.Budget
	if err := tx.Where("user_id=? AND month=?", user, s.Period).Order("id").Find(&budgets).Error; err != nil {
		return s, err
	}
	for i := range budgets {
		b := &budgets[i]
		if err := tx.Where("user_id=? AND budget_id=?", user, b.ID).Order("id").Find(&b.Categories).Error; err != nil {
			return s, err
		}
		if err := tx.Where("user_id=? AND budget_id=?", user, b.ID).Order("id").Find(&b.Rules).Error; err != nil {
			return s, err
		}
		source(&s, "budget", b.ID, b.UpdatedAt)
	}
	p, err := billing.Normalize(billing.Input{From: start.Format(time.RFC3339), To: end.Format(time.RFC3339), Timezone: "UTC", Limit: 100})
	if err != nil {
		return s, err
	}
	report, err := billing.Analyze(user, "generate_billing_report", p, billing.Snapshot{Accounts: allAccounts, Transactions: transactions, Budgets: budgets, At: now.UnixMilli()})
	if err != nil {
		return s, err
	}
	for _, b := range report.BudgetStatus {
		s.LifeFloorMinor = max(s.LifeFloorMinor, max(int64(0), b.RemainingMinor))
		s.EssentialFloorMinor = max(s.EssentialFloorMinor, b.EssentialReserveRemainingMinor)
	}
	for _, a := range report.Anomalies {
		s.ReviewNotes = append(s.ReviewNotes, "Billing review: "+a.Type+"; no presumed refund or savings")
		for _, e := range a.Evidence {
			source(&s, "billing_anomaly_transaction", e.TransactionID, 0)
		}
	}
	// Historical reports are referenced, while current figures are recomputed.
	var stored billing.StoredReport
	e := tx.Where("user_id=?", user).Order("created_at DESC,id DESC").First(&stored).Error
	if e == nil {
		s.Sources = append(s.Sources, Source{"billing_report", stored.ID, stored.CreatedAt})
	} else if e != gorm.ErrRecordNotFound {
		return s, e
	}
	var analysis entity.SubscriptionAnalysis
	e = tx.Where("user_id=?", user).Order("id DESC").First(&analysis).Error
	if e == nil {
		source(&s, "subscription_analysis", analysis.ID, analysis.CreatedAt)
	} else if e != gorm.ErrRecordNotFound {
		return s, e
	}
	desiredLifeGoals := []goal.Parsed{}
	for _, id := range settings.LifeGoalIDs {
		var g goal.Goal
		if err := tx.Where("id=? AND user_id=?", id, user).First(&g).Error; err != nil {
			return s, coreRepo.DBError(err)
		}
		var v goal.GoalVersion
		if err := tx.Where("goal_id=? AND user_id=? AND version=?", id, user, g.CurrentVersion).First(&v).Error; err != nil {
			return s, err
		}
		source(&s, "life_goal_version", v.ID, v.Version)
		if v.ParseStatus != "parsed" {
			s.Blockers = append(s.Blockers, "life goal is not parsed: "+strconv.FormatInt(id, 10))
			continue
		}
		var parsed goal.Parsed
		if err := json.Unmarshal(v.ParsedJSON, &parsed); err != nil {
			return s, err
		}
		desiredLifeGoals = append(desiredLifeGoals, parsed)
		var compiled goal.TaskPlan
		if err := tx.Where("goal_version_id=? AND user_id=?", v.ID, user).First(&compiled).Error; err != nil {
			return s, err
		}
		var plan goal.Plan
		if err := json.Unmarshal(compiled.PlanJSON, &plan); err != nil {
			return s, err
		}
		if len(plan.MissingInformation) > 0 {
			s.Blockers = append(s.Blockers, "life goal needs additional financial information")
		}
		// Conflicting savings amounts are negotiated below, not silently rewritten.
		for _, c := range plan.Conflicts {
			if c.Code != "INSUFFICIENT_BUDGET" {
				s.Blockers = append(s.Blockers, "goal conflict: "+c.Code)
			}
		}
		if v := plan.FinancialContext.SpendingBudgetMinor; v != nil {
			s.LifeFloorMinor = max(s.LifeFloorMinor, max(int64(0), *v-report.TotalExpense))
		}
		// No category evidence proves a full essential monthly floor was already
		// paid. Preserve it in full rather than subtract unrelated shopping.
		if v := plan.FinancialContext.MonthlyIncomeMinor; v != nil {
			s.Settings.BaselineIncomeMinor = max(s.Settings.BaselineIncomeMinor, *v)
		}
		if v := plan.FinancialContext.EssentialExpensesMinor; v != nil {
			s.EssentialFloorMinor = max(s.EssentialFloorMinor, *v)
		}
		for _, c := range parsed.Constraints {
			if c.Kind == "mandatory" {
				s.Blockers = append(s.Blockers, "mandatory natural-language constraint needs structured verification: "+c.Description)
			}
			s.ReviewNotes = append(s.ReviewNotes, c.Kind+": "+c.Description)
		}
		for _, op := range parsed.ProhibitedOperations {
			s.ReviewNotes = append(s.ReviewNotes, "prohibited operation: "+op)
		}
	}
	for _, id := range settings.SavingsGoalIDs {
		var g entity.SavingsGoal
		if err := tx.Where("id=? AND user_id=?", id, user).First(&g).Error; err != nil {
			return s, coreRepo.DBError(err)
		}
		var a entity.BankAccount
		if err := tx.Where("id=? AND user_id=?", g.AccountID, user).First(&a).Error; err != nil {
			return s, err
		}
		var plan entity.SavingsPlan
		if err := tx.Where("goal_id=? AND user_id=?", id, user).First(&plan).Error; err != nil {
			return s, err
		}
		var entries []entity.SavingsLedger
		if err := tx.Where("goal_id=? AND user_id=? AND created_at>=? AND created_at<?", id, user, start.UnixMilli(), end.UnixMilli()).Order("id").Limit(10001).Find(&entries).Error; err != nil {
			return s, err
		}
		if len(entries) > 10000 {
			return s, entity.Fail("ANALYSIS_LIMIT", "savings evidence limit")
		}
		gf := GoalFunds{ID: id, TargetMinor: g.TargetMinor, CurrentMinor: a.BalanceMinor, MonthlyMinor: plan.MonthlyMinor, Eligible: g.Status == "active" && a.Status == "active" && g.StartsAt <= now.UnixMilli() && g.EndsAt > now.UnixMilli()}
		for _, l := range entries {
			if l.DeltaMinor > 0 {
				gf.DepositedThisPeriodMinor += l.DeltaMinor
			}
			source(&s, "savings_ledger", l.ID, l.CreatedAt)
		}
		s.Goals = append(s.Goals, gf)
		source(&s, "savings_goal", g.ID, g.UpdatedAt)
		source(&s, "savings_account", a.ID, a.Version)
	}
	var totalMonthly, totalTarget int64
	for _, g := range s.Goals {
		totalMonthly += g.MonthlyMinor
		totalTarget += g.TargetMinor
	}
	for _, g := range desiredLifeGoals {
		if (g.SavingsMonthlyMinor != nil && *g.SavingsMonthlyMinor != totalMonthly) || (g.SavingsTargetMinor != nil && *g.SavingsTargetMinor != totalTarget) {
			s.Blockers = append(s.Blockers, "life goal and selected savings goal definitions differ; reconcile explicitly")
		}
	}
	var subs []entity.Subscription
	if err := tx.Where("user_id=?", user).Order("id").Limit(1001).Find(&subs).Error; err != nil {
		return s, err
	}
	if len(subs) > 1000 {
		return s, entity.Fail("ANALYSIS_LIMIT", "subscription limit")
	}
	for _, sub := range subs {
		source(&s, "subscription", sub.ID, sub.Version)
		f, e := subscription.Forecast(sub.AmountMinor, sub.BillingInterval)
		if e != nil {
			return s, e
		}
		if sub.Status == entity.SubscriptionCancelled {
			s.CancelledForecastMinor += f.MonthlyMinor
			continue
		}
		if sub.Status == entity.SubscriptionActive {
			var payments []entity.SubscriptionPayment
			if err := tx.Where("user_id=? AND subscription_id=?", user, sub.ID).Order("occurred_at DESC,id DESC").Limit(1).Find(&payments).Error; err != nil {
				return s, err
			}
			amount := sub.AmountMinor
			for _, pay := range payments {
				source(&s, "subscription_payment", pay.ID, pay.UpdatedAt)
				source(&s, "subscription_transaction", pay.TransactionID, pay.OccurredAt)
				if pay.AmountMinor != sub.AmountMinor {
					amount = max(amount, pay.AmountMinor)
					s.ReviewNotes = append(s.ReviewNotes, "subscription price observation differs from adopted contract; reserve larger amount until confirmed")
				}
			}
			due, overdue, e := subscriptionReserve(amount, sub.NextBillingAt, sub.BillingInterval, start, end, now)
			if e != nil {
				return s, e
			}
			s.SubscriptionDueMinor += due
			if s.SubscriptionDueMinor > 1000*entity.MaxBalance {
				return s, entity.Fail("ANALYSIS_LIMIT", "aggregate subscription obligations exceed planning bound")
			}
			if overdue {
				s.ReviewNotes = append(s.ReviewNotes, "subscription overdue estimate requires review; preserve historical and remaining monthly obligations")
			}
		}
		if sub.Status == "candidate" {
			s.ReviewNotes = append(s.ReviewNotes, "unconfirmed subscription candidate "+strconv.FormatInt(sub.ID, 10)+" requires review; no automatic cancellation")
		}
	}
	return s, nil
}

func (r *Repository) owned(tx *gorm.DB, user, id int64, lock bool) (FinancialPlan, error) {
	var p FinancialPlan
	q := tx.Where("user_id=? AND id=?", user, id)
	if lock {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	e := q.First(&p).Error
	return p, coreRepo.DBError(e)
}
func (r *Repository) appendVersion(tx *gorm.DB, m service.Meta, p *FinancialPlan, ev *ReplanEvent, reason string) (PlanVersion, error) {
	var settings Settings
	if err := json.Unmarshal(p.SettingsJSON, &settings); err != nil {
		return PlanVersion{}, err
	}
	s, err := snapshot(tx, m.UserID, settings, time.Now().UTC())
	if err != nil {
		return PlanVersion{}, err
	}
	result, err := Calculate(s)
	if err != nil {
		return PlanVersion{}, err
	}
	var old PlanVersion
	var impact int64 = result.SuggestedSavingsMinor
	if p.CurrentVersion > 0 {
		if err := tx.Where("user_id=? AND plan_id=? AND version=?", m.UserID, p.ID, p.CurrentVersion).First(&old).Error; err != nil {
			return PlanVersion{}, err
		}
		var previous Result
		if err := json.Unmarshal(old.ResultJSON, &previous); err != nil {
			return PlanVersion{}, err
		}
		impact -= previous.SuggestedSavingsMinor
	}
	sr, _ := json.Marshal(s)
	rr, _ := json.Marshal(result)
	a := entity.Audit{ID: uuid.NewString(), UserID: m.UserID, Action: "planning.replan", ResourceID: p.ID, Outcome: "succeeded", TraceID: m.TraceID, TaskID: m.TaskID}
	v := PlanVersion{Base: entity.Base{UserID: m.UserID}, PlanID: p.ID, Version: p.CurrentVersion + 1, PreviousVersion: p.CurrentVersion, Reason: clip(reason, 255), ImpactMinor: impact, DataDigest: digest(s), NeedsConfirmation: true, SnapshotJSON: sr, ResultJSON: rr, TraceID: m.TraceID, TaskID: m.TaskID, AuditID: a.ID}
	if ev != nil {
		v.EventID = &ev.ID
	}
	if err := tx.Create(&v).Error; err != nil {
		return v, err
	}
	constraints := []PlanConstraint{{Kind: "mandatory", Description: "normal living and necessary spending protected", AmountMinor: result.LivingReserveMinor}, {Kind: "safety", Description: "additional obligations, cash buffer, income risk and safety reserve", AmountMinor: result.AdditionalObligationsMinor + result.CashflowBufferMinor + result.IncomeRiskReserveMinor + result.SafetyReserveMinor}, {Kind: "safety", Description: "future income and subscription savings are not available balance"}}
	for _, note := range s.Blockers {
		constraints = append(constraints, PlanConstraint{Kind: "mandatory", Description: note})
	}
	for _, note := range s.ReviewNotes {
		if len(note) <= 512 {
			constraints = append(constraints, PlanConstraint{Kind: "source_constraint", Description: note})
		}
	}
	for i := range constraints {
		constraints[i].Description = clip(constraints[i].Description, 512)
		constraints[i].UserID = m.UserID
		constraints[i].PlanVersionID = v.ID
		if err := tx.Create(&constraints[i]).Error; err != nil {
			return v, err
		}
	}
	p.CurrentVersion = v.Version
	if err := tx.Save(p).Error; err != nil {
		return v, err
	}
	if err := tx.Create(&a).Error; err != nil {
		return v, err
	}
	return v, nil
}
func view(tx *gorm.DB, p FinancialPlan) (View, error) {
	v := View{Plan: p, Constraints: []PlanConstraint{}, Decisions: []PlanDecision{}}
	if err := tx.Where("user_id=? AND plan_id=? AND version=?", p.UserID, p.ID, p.CurrentVersion).First(&v.Version).Error; err != nil {
		return v, err
	}
	if err := json.Unmarshal(v.Version.ResultJSON, &v.Result); err != nil {
		return v, err
	}
	if err := tx.Where("user_id=? AND plan_version_id=?", p.UserID, v.Version.ID).Order("id").Find(&v.Constraints).Error; err != nil {
		return v, err
	}
	if err := tx.Where("user_id=? AND plan_id=?", p.UserID, p.ID).Order("id").Find(&v.Decisions).Error; err != nil {
		return v, err
	}
	return v, nil
}
func (r *Repository) transaction(ctx context.Context, user int64, fn func(*gorm.DB) error) error {
	return coreRepo.DBError(r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := requireUser(tx, user); err != nil {
			return err
		}
		return fn(tx)
	}))
}
