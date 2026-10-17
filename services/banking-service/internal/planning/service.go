package planning

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	coreRepo "github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/service"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

type Service struct{ repo *Repository }

func New(db *gorm.DB) *Service      { return &Service{NewRepository(db)} }
func (s *Service) Configured() bool { return s != nil && s.repo != nil && s.repo.db != nil }
func ID(raw string) (int64, error) {
	n, e := strconv.ParseInt(raw, 10, 64)
	if e != nil || n <= 0 || strconv.FormatInt(n, 10) != raw {
		return 0, entity.Fail("INVALID_ARGUMENT", "canonical positive ID required")
	}
	return n, nil
}
func prepare(settings *Settings) error {
	if settings.Period == "" {
		settings.Period = time.Now().UTC().Format("2006-01")
	}
	if settings.Period != time.Now().UTC().Format("2006-01") {
		return entity.Fail("INVALID_ARGUMENT", "settings must refer to current UTC month")
	}
	if settings.LifeGoalIDs == nil {
		settings.LifeGoalIDs = []int64{}
	}
	if settings.Cashflows == nil {
		settings.Cashflows = []Cashflow{}
	}
	return ValidateSettings(*settings)
}
func (s *Service) Create(ctx context.Context, m service.Meta, in CreateInput) (View, error) {
	var out View
	if len(strings.TrimSpace(in.Name)) == 0 || len(in.Name) > 128 {
		return out, entity.Fail("INVALID_ARGUMENT", "plan name required")
	}
	if e := prepare(&in.Settings); e != nil {
		return out, e
	}
	err := s.repo.transaction(ctx, m.UserID, func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&FinancialPlan{}).Where("user_id=? AND status='active'", m.UserID).Count(&count).Error; err != nil {
			return err
		}
		if count >= 100 {
			return entity.Fail("ANALYSIS_LIMIT", "at most 100 active plans")
		}
		raw, _ := json.Marshal(in.Settings)
		p := FinancialPlan{Base: entity.Base{UserID: m.UserID}, Name: in.Name, SettingsJSON: raw, Status: "active"}
		if err := tx.Create(&p).Error; err != nil {
			return err
		}
		if _, err := s.repo.appendVersion(tx, m, &p, nil, "initial planning"); err != nil {
			return err
		}
		var err error
		out, err = view(tx, p)
		return err
	})
	return out, err
}
func (s *Service) Get(ctx context.Context, user, id int64) (View, error) {
	var out View
	err := s.repo.transaction(ctx, user, func(tx *gorm.DB) error {
		p, e := s.repo.owned(tx, user, id, false)
		if e != nil {
			return e
		}
		out, e = view(tx, p)
		return e
	})
	return out, err
}
func (s *Service) Versions(ctx context.Context, user, id int64, pagination ...int) ([]PlanVersion, error) {
	out := []PlanVersion{}
	limit, offset := 100, 0
	if len(pagination) == 2 {
		limit, offset = pagination[0], pagination[1]
	}
	if limit < 1 || limit > 100 || offset < 0 || offset > 1000000 {
		return out, entity.Fail("INVALID_ARGUMENT", "invalid history pagination")
	}
	err := s.repo.transaction(ctx, user, func(tx *gorm.DB) error {
		if _, e := s.repo.owned(tx, user, id, false); e != nil {
			return e
		}
		return tx.Where("user_id=? AND plan_id=?", user, id).Order("version DESC").Limit(limit).Offset(offset).Find(&out).Error
	})
	return out, err
}
func (s *Service) Update(ctx context.Context, m service.Meta, id int64, in UpdateInput) (View, error) {
	var out View
	if e := prepare(&in.Settings); e != nil {
		return out, e
	}
	err := s.repo.transaction(ctx, m.UserID, func(tx *gorm.DB) error {
		p, e := s.repo.owned(tx, m.UserID, id, true)
		if e != nil {
			return e
		}
		if p.CurrentVersion != in.ExpectedVersion {
			return entity.Fail("VERSION_CONFLICT", "plan changed")
		}
		p.SettingsJSON, _ = json.Marshal(in.Settings)
		if _, e = s.repo.appendVersion(tx, m, &p, nil, "user updated cashflow, necessary expenses or reserves"); e != nil {
			return e
		}
		out, e = view(tx, p)
		return e
	})
	return out, err
}
func (s *Service) Replan(ctx context.Context, m service.Meta, id int64) (View, error) {
	var out View
	err := s.repo.transaction(ctx, m.UserID, func(tx *gorm.DB) error {
		p, e := s.repo.owned(tx, m.UserID, id, true)
		if e != nil {
			return e
		}
		if _, e = s.repo.appendVersion(tx, m, &p, nil, "explicit advisory replan"); e != nil {
			return e
		}
		out, e = view(tx, p)
		return e
	})
	return out, err
}
func (s *Service) Decide(ctx context.Context, m service.Meta, id int64, in DecisionInput) (PlanDecision, error) {
	var out PlanDecision
	if !in.ConfirmAdvice || (in.Option != "conservative" && in.Option != "balanced" && in.Option != "aggressive") {
		return out, entity.Fail("INVALID_ARGUMENT", "explicit advice confirmation and valid option required")
	}
	err := s.repo.transaction(ctx, m.UserID, func(tx *gorm.DB) error {
		p, e := s.repo.owned(tx, m.UserID, id, true)
		if e != nil {
			return e
		}
		if p.CurrentVersion != in.ExpectedVersion {
			return entity.Fail("VERSION_CONFLICT", "plan changed; review current suggestions")
		}
		v, e := view(tx, p)
		if e != nil {
			return e
		}
		var settings Settings
		if e = json.Unmarshal(p.SettingsJSON, &settings); e != nil {
			return e
		}
		snap, e := snapshot(tx, m.UserID, settings, time.Now().UTC())
		if e != nil {
			return e
		}
		if digest(snap) != v.Version.DataDigest || in.DataDigest != v.Version.DataDigest {
			return entity.Fail("VERSION_CONFLICT", "source data changed; replan and confirm again")
		}
		e = tx.Where("user_id=? AND plan_version_id=?", m.UserID, v.Version.ID).First(&out).Error
		if e == nil {
			if out.Option != in.Option {
				return entity.Fail("STATE_CONFLICT", "this advice version already has a different decision")
			}
			return nil
		}
		if e != gorm.ErrRecordNotFound {
			return e
		}
		a := entity.Audit{ID: uuid.NewString(), UserID: m.UserID, Action: "planning.choose_advice", ResourceID: p.ID, Outcome: "succeeded", TraceID: m.TraceID, TaskID: m.TaskID}
		out = PlanDecision{Base: entity.Base{UserID: m.UserID}, PlanID: p.ID, PlanVersionID: v.Version.ID, Option: in.Option, DataDigest: in.DataDigest, AuditID: a.ID}
		if e = tx.Create(&out).Error; e != nil {
			return e
		}
		if e = tx.Model(&PlanVersion{}).Where("id=? AND user_id=?", v.Version.ID, m.UserID).Update("needs_confirmation", false).Error; e != nil {
			return e
		}
		return tx.Create(&a).Error
	})
	return out, err
}

var eventTypes = map[string]bool{"transaction_added": true, "account_changed": true, "necessary_expense_changed": true, "subscription_changed": true, "subscription_price_observed": true, "life_goal_changed": true, "savings_goal_changed": true, "savings_executed": true}

func (s *Service) Publish(ctx context.Context, m service.Meta, key string, in EventInput) (ReplanEvent, error) {
	var out ReplanEvent
	if !eventTypes[in.Type] || len(in.ResourceID) == 0 || len(in.ResourceID) > 128 || len(in.Reason) == 0 || len(in.Reason) > 255 || len(key) == 0 || len(key) > 120 {
		return out, entity.Fail("INVALID_ARGUMENT", "known event type, resource, reason and Idempotency-Key required")
	}
	for _, c := range key {
		if c < 33 || c > 126 {
			return out, entity.Fail("INVALID_ARGUMENT", "ASCII key required")
		}
	}
	err := s.repo.transaction(ctx, m.UserID, func(tx *gorm.DB) error {
		candidate := ReplanEvent{Base: entity.Base{UserID: m.UserID}, EventKey: "api:" + key, RequestHash: digest(in), Type: in.Type, ResourceID: in.ResourceID, Reason: in.Reason, Status: "pending", TraceID: m.TraceID, TaskID: m.TaskID}
		if e := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&candidate).Error; e != nil {
			return e
		}
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id=? AND event_key=?", m.UserID, candidate.EventKey).First(&out).Error; e != nil {
			return e
		}
		if out.RequestHash != candidate.RequestHash {
			return entity.Fail("IDEMPOTENCY_CONFLICT", "event key already bound to different content")
		}
		return nil
	})
	return out, err
}

// ProcessOne atomically consumes one durable outbox event and its plan versions.
// A crash rolls back both the versions and acknowledgement. SQL row locks
// serialize concurrent workers; unique(plan_id,event_id) defends against replay.
func (s *Service) ProcessOne(ctx context.Context) (bool, error) {
	var candidate ReplanEvent
	e := s.repo.db.WithContext(ctx).Where("status='pending' AND retry_at<=?", time.Now().UnixMilli()).Order("id").First(&candidate).Error
	if e == gorm.ErrRecordNotFound {
		return false, nil
	}
	if e != nil {
		return false, coreRepo.DBError(e)
	}
	ev := candidate
	found := true
	err := s.repo.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Uniform order: user -> event -> plan. The prefetch is only a hint;
		// current locking reads recheck status after another worker may finish.
		if e := requireUser(tx, candidate.UserID); e != nil {
			return e
		}
		e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND status='pending' AND retry_at<=?", candidate.ID, time.Now().UnixMilli()).First(&ev).Error
		if e == gorm.ErrRecordNotFound {
			found = false
			return nil
		}
		if e != nil {
			return e
		}
		found = true

		// The queue contains historical source events; do not fabricate history for
		// plans created after those mutations.
		var plans []FinancialPlan
		if e = tx.Where("user_id=? AND status='active' AND created_at<=?", ev.UserID, ev.CreatedAt).Order("id").Find(&plans).Error; e != nil {
			return e
		}
		for _, p := range plans {
			p, e = s.repo.owned(tx, ev.UserID, p.ID, true)
			if e != nil {
				return e
			}
			var count int64
			if e = tx.Model(&PlanVersion{}).Where("plan_id=? AND event_id=?", p.ID, ev.ID).Count(&count).Error; e != nil {
				return e
			}
			if count > 0 {
				continue
			}
			if e = requireUser(tx, ev.UserID); e != nil {
				return e
			}
			m := service.Meta{UserID: ev.UserID, TraceID: ev.TraceID, TaskID: ev.TaskID}
			if m.TraceID == "" {
				m.TraceID = "replan-event-" + strconv.FormatInt(ev.ID, 10)
			}
			if m.TaskID == "" {
				m.TaskID = "replan-event-" + strconv.FormatInt(ev.ID, 10)
			}
			if _, e = s.repo.appendVersion(tx, m, &p, &ev, ev.Type+": "+ev.Reason); e != nil {
				return e
			}
		}
		return tx.Model(&ReplanEvent{}).Where("id=?", ev.ID).Updates(map[string]any{"status": "processed", "error_code": "", "updated_at": time.Now().UnixMilli()}).Error
	})
	if err != nil && found {
		retry := time.Now().Add(5 * time.Second).UnixMilli()
		e := s.repo.db.WithContext(ctx).Model(&ReplanEvent{}).Where("id=? AND status='pending'", ev.ID).Updates(map[string]any{"attempts": gorm.Expr("attempts+1"), "retry_at": retry, "error_code": entity.Code(err), "updated_at": time.Now().UnixMilli()}).Error
		if e != nil {
			return found, e
		}
	}
	return found, coreRepo.DBError(err)
}
func (s *Service) Run(ctx context.Context, log *slog.Logger) {
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			for i := 0; i < 100; i++ {
				job, cancel := context.WithTimeout(ctx, 10*time.Second)
				found, e := s.ProcessOne(job)
				cancel()
				if e != nil {
					log.Error("planning_event_failed", "error_code", entity.Code(e))
					break
				}
				if !found {
					break
				}
			}
		}
	}
}
func (s *Service) UpdateGoal(ctx context.Context, m service.Meta, id int64, in GoalUpdateInput) (entity.SavingsGoal, error) {
	var g entity.SavingsGoal
	if in.TargetMinor <= 0 || !bounded(in.TargetMinor) || in.MonthlyMinor <= 0 || !bounded(in.MonthlyMinor) {
		return g, entity.Fail("INVALID_ARGUMENT", "positive bounded goal amounts required")
	}
	err := s.repo.transaction(ctx, m.UserID, func(tx *gorm.DB) error {
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=?", id, m.UserID).First(&g).Error; e != nil {
			return e
		}
		var previous entity.SavingsPlan
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("goal_id=? AND user_id=?", id, m.UserID).First(&previous).Error; e != nil {
			return e
		}
		if g.TargetMinor != in.ExpectedTargetMinor || previous.MonthlyMinor != in.ExpectedMonthlyMinor || g.EndsAt != in.ExpectedEndsAt {
			return entity.Fail("VERSION_CONFLICT", "savings goal target changed")
		}
		if in.EndsAt <= g.StartsAt || in.EndsAt-g.StartsAt > int64(10*365*24*time.Hour/time.Millisecond) || in.EndsAt > time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli() {
			return entity.Fail("INVALID_ARGUMENT", "invalid goal period")
		}
		g.TargetMinor = in.TargetMinor
		g.EndsAt = in.EndsAt
		if e := tx.Save(&g).Error; e != nil {
			return e
		}
		if e := tx.Model(&entity.SavingsPlan{}).Where("user_id=? AND goal_id=?", m.UserID, id).Update("monthly_minor", in.MonthlyMinor).Error; e != nil {
			return e
		}
		previous.MonthlyMinor = in.MonthlyMinor
		g.Plan = &previous
		var account entity.BankAccount
		if e := tx.Where("id=? AND user_id=?", g.AccountID, m.UserID).First(&account).Error; e != nil {
			return e
		}
		g.CurrentMinor = account.BalanceMinor
		g.ProgressBasisPoints = g.CurrentMinor * 10000 / g.TargetMinor
		return tx.Create(&entity.Audit{ID: uuid.NewString(), UserID: m.UserID, Action: "planning.goal_settings", ResourceID: id, Outcome: "succeeded", TraceID: m.TraceID, TaskID: m.TaskID}).Error
	})
	return g, err
}
