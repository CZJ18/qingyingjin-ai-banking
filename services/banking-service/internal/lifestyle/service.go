package lifestyle

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/billing"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/planning"
	"github.com/qingyingjin/banking-service/internal/service"
	"gorm.io/gorm"
	"time"
)

type Service struct {
	repo   *Repository
	Parser *Parser
}

func New(db *gorm.DB) *Service      { return &Service{repo: NewRepository(db)} }
func (s *Service) Configured() bool { return s != nil && s.repo != nil && s.repo.db != nil }
func audit(tx *gorm.DB, m service.Meta, action string, id int64) (string, error) {
	a := entity.Audit{ID: uuid.NewString(), UserID: m.UserID, Action: action, ResourceID: id, Outcome: "succeeded", TraceID: m.TraceID, TaskID: m.TaskID}
	return a.ID, tx.Create(&a).Error
}
func (s *Service) SaveGoal(ctx context.Context, m service.Meta, key string, id int64, in GoalInput) (GoalView, error) {
	raw, e := json.Marshal(in.Spec)
	if e != nil {
		return GoalView{}, e
	}
	spec, e := DecodeSpec(raw, in.Text)
	if e != nil {
		return GoalView{}, e
	}
	in.Spec = spec
	if id == 0 && in.ExpectedVersion != 0 || id > 0 && in.ExpectedVersion < 1 {
		return GoalView{}, entity.Fail("INVALID_ARGUMENT", "invalid expected goal version")
	}
	return atomic(s.repo, ctx, m.UserID, fmt.Sprintf("lifestyle.goal.%d", id), key, in, func(tx *gorm.DB) (GoalView, error) {
		var g Goal
		if id == 0 {
			var count int64
			if e := tx.Model(&Goal{}).Where("user_id=?", m.UserID).Count(&count).Error; e != nil {
				return GoalView{}, e
			}
			if count >= 100 {
				return GoalView{}, entity.Fail("ANALYSIS_LIMIT", "at most 100 lifestyle goals")
			}
			g = Goal{Base: entity.Base{UserID: m.UserID}, CurrentVersion: 1}
			if e := tx.Create(&g).Error; e != nil {
				return GoalView{}, e
			}
		} else {
			var e error
			g, e = ownedGoal(tx, m.UserID, id)
			if e != nil {
				return GoalView{}, e
			}
			if g.CurrentVersion != in.ExpectedVersion {
				return GoalView{}, entity.Fail("VERSION_CONFLICT", "goal version changed")
			}
			g.CurrentVersion++
			if e = tx.Model(&g).Updates(map[string]any{"current_version": g.CurrentVersion}).Error; e != nil {
				return GoalView{}, e
			}
			if e = tx.Model(&Recommendation{}).Where("user_id=? AND goal_id=? AND status='pending'", m.UserID, id).Updates(map[string]any{"status": "superseded", "version": gorm.Expr("version+1")}).Error; e != nil {
				return GoalView{}, e
			}
		}
		raw, _ := json.Marshal(spec)
		v := GoalVersion{Base: entity.Base{UserID: m.UserID}, GoalID: g.ID, Version: g.CurrentVersion, Text: in.Text, SpecJSON: raw, TraceID: m.TraceID}
		if e := tx.Create(&v).Error; e != nil {
			return GoalView{}, e
		}
		if _, e := audit(tx, m, "lifestyle.goal.save", g.ID); e != nil {
			return GoalView{}, e
		}
		return goalView(tx, g)
	})
}
func (s *Service) Goal(ctx context.Context, user, id int64, pagination ...int) (GoalView, error) {
	var v GoalView
	e := s.repo.transaction(ctx, user, func(tx *gorm.DB) error {
		g, e := ownedGoal(tx, user, id)
		if e != nil {
			return e
		}
		v, e = goalView(tx, g, pagination...)
		return e
	})
	return v, e
}
func loadSnapshot(tx *gorm.DB, user int64, g Goal, in CompileInput) (Snapshot, error) {
	s := Snapshot{Sources: in, Categories: []Category{}, EvidenceTransactionIDs: []string{}}
	if g.CurrentVersion != in.ExpectedVersion {
		return s, entity.Fail("VERSION_CONFLICT", "goal version changed")
	}
	var version GoalVersion
	if e := tx.Where("user_id=? AND goal_id=? AND version=?", user, g.ID, g.CurrentVersion).First(&version).Error; e != nil {
		return s, e
	}
	if e := json.Unmarshal(version.SpecJSON, &s.Goal); e != nil {
		return s, e
	}
	var source []any
	if in.BudgetID > 0 {
		var b entity.Budget
		if e := tx.Where("id=? AND user_id=?", in.BudgetID, user).First(&b).Error; e != nil {
			return s, e
		}
		var cats []entity.BudgetCategory
		if e := tx.Where("user_id=? AND budget_id=?", user, b.ID).Order("id").Find(&cats).Error; e != nil {
			return s, e
		}
		s.BudgetMinor = b.TotalMinor
		for _, c := range cats {
			s.Categories = append(s.Categories, Category{Name: c.Category, AllocatedMinor: c.AllocatedMinor, EssentialReserveMinor: c.EssentialReserveMinor})
		}
		source = append(source, b, cats)
	}
	if in.BillingReportID != "" {
		var stored billing.StoredReport
		if e := tx.Where("id=? AND user_id=?", in.BillingReportID, user).First(&stored).Error; e != nil {
			return s, e
		}
		var report billing.Report
		if e := json.Unmarshal(stored.ReportJSON, &report); e != nil {
			return s, e
		}
		if e := billing.ValidateReport(report); e != nil {
			return s, e
		}
		if report.UserID != user || report.ID != stored.ID {
			return s, entity.Fail("TOOL_OUTPUT_INVALID", "report ownership mismatch")
		}
		from, e1 := time.Parse(time.RFC3339, report.AnalysisPeriod.From)
		to, e2 := time.Parse(time.RFC3339, report.AnalysisPeriod.To)
		loc, e3 := time.LoadLocation(report.AnalysisPeriod.Timezone)
		if e1 == nil && e2 == nil && e3 == nil {
			start := time.Date(from.In(loc).Year(), from.In(loc).Month(), 1, 0, 0, 0, 0, loc)
			s.CompleteMonth = from.Equal(start) && to.Equal(start.AddDate(0, 1, 0)) && !to.After(time.Now())
		}
		var accounts []entity.BankAccount
		if e := tx.Where("user_id=?", user).Order("id").Find(&accounts).Error; e != nil {
			return s, e
		}
		if len(accounts) != len(report.Accounts) {
			s.CompleteMonth = false
		}
		ids := map[int64]bool{}
		for _, a := range report.Accounts {
			if a.UserID != user || ids[a.ID] {
				return s, entity.Fail("TOOL_OUTPUT_INVALID", "report account evidence invalid")
			}
			ids[a.ID] = true
		}
		for _, a := range accounts {
			if !ids[a.ID] {
				s.CompleteMonth = false
			}
		}
		source = append(source, accounts)
		s.EvidenceTransactionIDs = report.EvidenceTransactionIDs
		seenCategories := map[string]bool{}
		for i, c := range s.Categories {
			canonical, known := billing.CanonicalCategory(c.Name)
			if !known {
				continue
			}
			if seenCategories[canonical] {
				return s, entity.Fail("INVALID_ARGUMENT", "merge duplicate category aliases before lifestyle planning")
			}
			seenCategories[canonical] = true
			for _, x := range report.CategoryBreakdown {
				if x.Category == canonical {
					s.Categories[i].ObservedMinor = x.ExpenseMinor
				}
			}
		}
		source = append(source, report)
	}
	if in.FinancialPlanID > 0 {
		snap, result, e := planning.LifestyleSnapshot(tx, user, in.FinancialPlanID)
		if e != nil {
			return s, e
		}
		if len(result.Blockers) == 0 {
			ceiling := result.SavingsCapMinor
			s.SavingsCeilingMinor = &ceiling
		}
		source = append(source, snap, result)
	}
	source = append(source, s.Goal, s.Categories)
	s.SourceDigest = digest(source)
	return s, nil
}
func (s *Service) Compile(ctx context.Context, m service.Meta, key string, id int64, in CompileInput) (PlanView, error) {
	if id <= 0 || in.ExpectedVersion < 1 || in.BudgetID < 0 || in.FinancialPlanID < 0 || len(in.BillingReportID) > 64 {
		return PlanView{}, entity.Fail("INVALID_ARGUMENT", "invalid source bindings")
	}
	return atomic(s.repo, ctx, m.UserID, fmt.Sprintf("lifestyle.compile.%d", id), key, in, func(tx *gorm.DB) (PlanView, error) {
		g, e := ownedGoal(tx, m.UserID, id)
		if e != nil {
			return PlanView{}, e
		}
		snap, e := loadSnapshot(tx, m.UserID, g, in)
		if e != nil {
			return PlanView{}, e
		}
		r, e := Calculate(snap)
		if e != nil {
			return PlanView{}, e
		}
		var count int64
		if e = tx.Model(&Plan{}).Where("user_id=? AND goal_id=?", m.UserID, id).Count(&count).Error; e != nil {
			return PlanView{}, e
		}
		if count >= 1000 {
			return PlanView{}, entity.Fail("ANALYSIS_LIMIT", "at most 1000 retained plans per goal")
		}
		sr, _ := json.Marshal(snap)
		rr, _ := json.Marshal(r)
		p := Plan{Base: entity.Base{UserID: m.UserID}, GoalID: id, GoalVersion: g.CurrentVersion, InputDigest: snap.SourceDigest, SnapshotJSON: sr, ResultJSON: rr, ExpiresAt: time.Now().Add(24 * time.Hour).UnixMilli(), TraceID: m.TraceID, AuditID: uuid.NewString()}
		if e = tx.Create(&p).Error; e != nil {
			return PlanView{}, e
		}
		if e = tx.Model(&Recommendation{}).Where("user_id=? AND goal_id=? AND status='pending'", m.UserID, id).Updates(map[string]any{"status": "superseded", "version": gorm.Expr("version+1")}).Error; e != nil {
			return PlanView{}, e
		}
		for _, o := range r.Options {
			raw, _ := json.Marshal(o)
			rec := Recommendation{Base: entity.Base{UserID: m.UserID}, GoalID: id, PlanID: p.ID, Type: "lifestyle_budget_" + o.Name, ExpectedImpactMinor: o.ExpenseReductionMinor, ImpactPeriod: "monthly_forecast", Explanation: o.Explanation, Confidence: "historical_evidence_requires_user_review", Status: "pending", Version: 1, ExpiresAt: p.ExpiresAt, RuleVersion: r.RuleVersion, OptionJSON: raw}
			if e = tx.Create(&rec).Error; e != nil {
				return PlanView{}, e
			}
		}
		a := entity.Audit{ID: p.AuditID, UserID: m.UserID, Action: "lifestyle.compile", ResourceID: p.ID, Outcome: "succeeded", TraceID: m.TraceID, TaskID: m.TaskID}
		if e = tx.Create(&a).Error; e != nil {
			return PlanView{}, e
		}
		return planView(tx, m.UserID, p.ID)
	})
}
func (s *Service) Plan(ctx context.Context, user, id int64) (PlanView, error) {
	var v PlanView
	e := s.repo.transaction(ctx, user, func(tx *gorm.DB) error { var e error; v, e = planView(tx, user, id); return e })
	return v, e
}
func (s *Service) Plans(ctx context.Context, user, goalID int64, pagination ...int) ([]PlanView, error) {
	out := []PlanView{}
	limit, offset, err := page(pagination)
	if err != nil {
		return out, err
	}
	e := s.repo.transaction(ctx, user, func(tx *gorm.DB) error {
		if _, e := ownedGoal(tx, user, goalID); e != nil {
			return e
		}
		var ps []Plan
		if e := tx.Where("user_id=? AND goal_id=?", user, goalID).Order("id DESC").Limit(limit).Offset(offset).Find(&ps).Error; e != nil {
			return e
		}
		for _, p := range ps {
			v, e := planView(tx, user, p.ID)
			if e != nil {
				return e
			}
			out = append(out, v)
		}
		return nil
	})
	return out, e
}
func (s *Service) Decide(ctx context.Context, m service.Meta, key string, id int64, in DecisionInput) (Decision, error) {
	if in.ExpectedVersion < 1 || (in.Decision != "accept" && in.Decision != "reject") || len(in.PlanDigest) != 64 {
		return Decision{}, entity.Fail("INVALID_ARGUMENT", "version, digest and accept/reject required")
	}
	return atomic(s.repo, ctx, m.UserID, fmt.Sprintf("lifestyle.decision.%d", id), key, in, func(tx *gorm.DB) (Decision, error) {
		var rec Recommendation
		if e := tx.Where("id=? AND user_id=?", id, m.UserID).First(&rec).Error; e != nil {
			return Decision{}, e
		}
		if rec.Version != in.ExpectedVersion {
			return Decision{}, entity.Fail("VERSION_CONFLICT", "recommendation version changed")
		}
		if rec.Status != "pending" || rec.ExpiresAt <= time.Now().UnixMilli() {
			return Decision{}, entity.Fail("STATE_CONFLICT", "recommendation is inactive or expired")
		}
		v, e := planView(tx, m.UserID, rec.PlanID)
		if e != nil {
			return Decision{}, e
		}
		if v.Plan.InputDigest != in.PlanDigest {
			return Decision{}, entity.Fail("VERSION_CONFLICT", "decision must bind exact plan data")
		}
		var snap Snapshot
		if e = json.Unmarshal(v.Plan.SnapshotJSON, &snap); e != nil {
			return Decision{}, e
		}
		g, e := ownedGoal(tx, m.UserID, rec.GoalID)
		if e != nil {
			return Decision{}, e
		}
		fresh, e := loadSnapshot(tx, m.UserID, g, snap.Sources)
		if e != nil {
			return Decision{}, e
		}
		if fresh.SourceDigest != in.PlanDigest {
			return Decision{}, entity.Fail("VERSION_CONFLICT", "financial sources changed; compile a new plan")
		}
		status := "accepted"
		if in.Decision == "reject" {
			status = "rejected"
		}
		if e = tx.Model(&rec).Updates(map[string]any{"status": status, "version": rec.Version + 1}).Error; e != nil {
			return Decision{}, e
		}
		// Selecting one option supersedes siblings. This records a preference only.
		if status == "accepted" {
			if e = tx.Model(&Recommendation{}).Where("user_id=? AND plan_id=? AND id<>? AND status='pending'", m.UserID, rec.PlanID, id).Updates(map[string]any{"status": "superseded", "version": gorm.Expr("version+1")}).Error; e != nil {
				return Decision{}, e
			}
		}
		aid, e := audit(tx, m, "lifestyle.recommendation."+in.Decision, id)
		if e != nil {
			return Decision{}, e
		}
		d := Decision{Base: entity.Base{UserID: m.UserID}, RecommendationID: id, Decision: in.Decision, RequestKey: key, InputDigest: digest(in), AuditID: aid, TraceID: m.TraceID}
		e = tx.Create(&d).Error
		return d, e
	})
}
func (s *Service) Decisions(ctx context.Context, user, id int64) ([]Decision, error) {
	out := []Decision{}
	e := s.repo.transaction(ctx, user, func(tx *gorm.DB) error {
		var rec Recommendation
		if e := tx.Where("id=? AND user_id=?", id, user).First(&rec).Error; e != nil {
			return e
		}
		return tx.Where("user_id=? AND recommendation_id=?", user, id).Order("id DESC").Limit(100).Find(&out).Error
	})
	return out, e
}
