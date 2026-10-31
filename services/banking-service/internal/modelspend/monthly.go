package modelspend

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"
	_ "time/tzdata"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// These are model-expense units (1e-8 currency), never bank-account cents.
// Limits are provisioned by an operator; callers cannot increase their limit.
type UserLimit struct {
	ID         string `gorm:"primaryKey"`
	ProjectID  string
	UserID     int64
	Currency   string
	LimitUnits int64
	CreatedAt  int64 `gorm:"autoCreateTime:milli"`
}

func (UserLimit) TableName() string { return "banking_model_user_limits" }

type UserWindow struct {
	ID                               string `gorm:"primaryKey"`
	LimitID, Period, Currency        string
	UserID                           int64
	LimitUnits, UsedUnits, HeldUnits int64
	Frozen                           bool
	CreatedAt                        int64 `gorm:"autoCreateTime:milli"`
	UpdatedAt                        int64 `gorm:"autoUpdateTime:milli"`
}

func (UserWindow) TableName() string { return "banking_model_user_windows" }

type AttemptOwner struct {
	ID       string `gorm:"primaryKey"` // exact immutable supplier attempt ID
	UserID   int64
	WindowID string
}

func (AttemptOwner) TableName() string { return "banking_model_attempt_owners" }

type userKey struct{}
type providerKey struct{}

// WithProviderOrigin records the gateway's fixed validated upstream, never a
// client-selected URL. The reserve audit binds this origin to price/model policy.
func WithProviderOrigin(ctx context.Context, origin string) context.Context {
	return context.WithValue(ctx, providerKey{}, origin)
}

// WithUser is used only after server-side identity verification. It must never
// be populated from a body user_id, OpenAI user field, or an unsigned ID header.
func WithUser(ctx context.Context, user int64) context.Context {
	return context.WithValue(ctx, userKey{}, user)
}
func userFrom(ctx context.Context) int64 { user, _ := ctx.Value(userKey{}).(int64); return user }
func userLimitID(project string, user int64) string {
	return hashText(fmt.Sprintf("%s\x00%d", project, user))
}
func monthlyPeriod(now time.Time) string {
	zone, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		panic("embedded Asia/Shanghai timezone unavailable")
	}
	return now.In(zone).Format("2006-01")
}
func monthlyCanReserve(limit, used, held, reserve int64) bool {
	return limit >= 0 && used >= 0 && held >= 0 && reserve > 0 && used <= limit && held <= limit-used && reserve <= limit-used-held
}

// ProvisionUserLimit is create-only: changing an existing quota requires a
// separate reviewed administrative process, never an automatic balance reset.
func (s *Service) ProvisionUserLimit(ctx context.Context, user int64, amount string) error {
	limit, err := ParseMoney(amount)
	if !s.ready() || user <= 0 || err != nil {
		return ErrPolicy
	}
	row := UserLimit{ID: userLimitID(s.policy.ProjectID, user), ProjectID: s.policy.ProjectID, UserID: user, Currency: s.policy.Currency, LimitUnits: limit}
	return ledgerError(s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing UserLimit
		e := tx.Where("id=?", row.ID).Take(&existing).Error
		if errors.Is(e, gorm.ErrRecordNotFound) {
			// No upsert: MySQL ON DUPLICATE KEY UPDATE would invoke the
			// immutable policy's UPDATE trigger even for an exact replay.
			return tx.Create(&row).Error
		}
		if e != nil {
			return e
		}
		if existing.ProjectID != row.ProjectID || existing.UserID != user || existing.Currency != row.Currency || existing.LimitUnits != limit {
			return ErrConflict
		}
		return nil
	}))
}

// Project pool is locked first, then quota/window, throughout dispatch and
// settlement. Both holds commit atomically before any provider HTTP request.
func (s *Service) reserveUser(tx *gorm.DB, user, now int64) (before, after *UserWindow, err error) {
	var limit UserLimit
	// Limits cannot change or be deleted. The project lock already serializes
	// admission, so locking this immutable row adds no safety and would require
	// UPDATE permission on a table the gateway must only read in PostgreSQL.
	if e := tx.Where("id=? AND project_id=? AND user_id=?", userLimitID(s.policy.ProjectID, user), s.policy.ProjectID, user).Take(&limit).Error; e != nil {
		return nil, nil, e
	}
	if limit.Currency != s.policy.Currency || limit.LimitUnits < 0 {
		return nil, nil, ErrConflict
	}
	period := monthlyPeriod(time.UnixMilli(now))
	w := UserWindow{ID: hashText(limit.ID + "\x00" + period), LimitID: limit.ID, UserID: user, Period: period, Currency: limit.Currency, LimitUnits: limit.LimitUnits, CreatedAt: now, UpdatedAt: now}
	if e := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&w).Error; e != nil {
		return nil, nil, e
	}
	if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=?", w.ID, user).Take(&w).Error; e != nil {
		return nil, nil, e
	}
	if w.LimitID != limit.ID || w.Currency != limit.Currency || w.Period != period || w.LimitUnits != limit.LimitUnits {
		return nil, nil, ErrConflict
	}
	if w.Frozen || !monthlyCanReserve(w.LimitUnits, w.UsedUnits, w.HeldUnits, s.reserveUnits) {
		return nil, nil, ErrUserQuota
	}
	original := w
	w.HeldUnits += s.reserveUnits
	w.UpdatedAt = now
	if e := tx.Save(&w).Error; e != nil {
		return nil, nil, e
	}
	return &original, &w, nil
}

func (s *Service) settleUser(tx *gorm.DB, a Attempt, actual int64) (before, after *UserWindow, err error) {
	var binding AttemptOwner
	e := tx.Where("id=?", a.ID).Take(&binding).Error
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return nil, nil, nil
	} // historical schema020 project-only attempt
	if e != nil {
		return nil, nil, e
	}
	var w UserWindow
	if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=?", binding.WindowID, binding.UserID).Take(&w).Error; e != nil {
		return nil, nil, e
	}
	if w.HeldUnits < a.ReservedUnits || w.UsedUnits > math.MaxInt64-actual || w.Currency != s.policy.Currency || w.LimitID != userLimitID(s.policy.ProjectID, binding.UserID) {
		return nil, nil, ErrUnavailable
	}
	original := w
	w.HeldUnits -= a.ReservedUnits
	w.UsedUnits += actual
	w.UpdatedAt = time.Now().UnixMilli()
	if actual > a.ReservedUnits || w.UsedUnits > w.LimitUnits || w.HeldUnits > w.LimitUnits-w.UsedUnits {
		w.Frozen = true
	}
	if e := tx.Save(&w).Error; e != nil {
		return nil, nil, e
	}
	return &original, &w, nil
}

// UserBudget is a read-only authenticated view; absence is not a free allowance.
func (s *Service) UserBudget(ctx context.Context, user int64) (UserWindow, error) {
	if !s.ready() || user <= 0 {
		return UserWindow{}, ErrUnavailable
	}
	var limit UserLimit
	if e := s.db.WithContext(ctx).Where("id=? AND user_id=?", userLimitID(s.policy.ProjectID, user), user).Take(&limit).Error; e != nil {
		return UserWindow{}, ledgerError(e)
	}
	period := monthlyPeriod(time.Now())
	w := UserWindow{ID: hashText(limit.ID + "\x00" + period), LimitID: limit.ID, UserID: user, Period: period, Currency: limit.Currency, LimitUnits: limit.LimitUnits}
	var saved UserWindow
	e := s.db.WithContext(ctx).Where("id=? AND user_id=?", w.ID, user).Take(&saved).Error
	if errors.Is(e, gorm.ErrRecordNotFound) {
		return w, nil
	}
	return saved, ledgerError(e)
}
