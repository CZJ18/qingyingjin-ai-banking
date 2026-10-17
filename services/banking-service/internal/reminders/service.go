package reminders

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"log/slog"
	"time"
)

type Service struct{ db *gorm.DB }

func New(db *gorm.DB) *Service { return &Service{db: db} }
func load(db *gorm.DB, user int64) (State, error) {
	var raw sql.NullString
	if e := db.Model(&entity.User{}).Select("renewal_reminders").Where("id = ?", user).Row().Scan(&raw); e != nil {
		if e == sql.ErrNoRows {
			return State{}, entity.Fail("NOT_FOUND", "user not found")
		}
		return State{}, e
	}
	if !raw.Valid {
		return Default(), nil
	}
	var state State
	if len(raw.String) > 2<<20 || json.Unmarshal([]byte(raw.String), &state) != nil || state.SchemaVersion != 1 || state.Items == nil || len(state.Items) > MaxNotices || state.Settings.LeadDays < 0 || state.Settings.LeadDays > 30 || state.Settings.Version < 1 {
		return State{}, entity.Fail("DEPENDENCY_UNAVAILABLE", "invalid persisted renewal notices")
	}
	seen := map[string]bool{}
	seenIDs := map[string]bool{}
	for _, n := range state.Items {
		key := time.UnixMilli(n.ChargeAt).Format(time.RFC3339Nano) + ":" + jsonID(n.SubscriptionID)
		if uuid.Validate(n.ID) != nil || seenIDs[n.ID] || seen[key] || n.Version < 1 || n.SubscriptionID <= 0 || n.Currency != "CNY" || n.AmountMinor <= 0 || n.AmountMinor > entity.MaxBalance || (n.Status != "unread" && n.Status != "read" && n.Status != "withdrawn" && n.Status != "expired") {
			return State{}, entity.Fail("DEPENDENCY_UNAVAILABLE", "invalid persisted renewal notice")
		}
		seen[key] = true
		seenIDs[n.ID] = true
	}
	return state, nil
}
func jsonID(id int64) string { b, _ := json.Marshal(id); return string(b) }
func save(tx *gorm.DB, user int64, s State) error {
	raw, e := json.Marshal(s)
	if e != nil {
		return e
	}
	return tx.Model(&entity.User{}).Where("id = ?", user).UpdateColumn("renewal_reminders", string(raw)).Error
}
func (s *Service) Read(ctx context.Context, user int64) (View, error) {
	state, e := load(s.db.WithContext(ctx), user)
	return AsView(state), e
}
func (s *Service) mutate(ctx context.Context, user int64, fn func(*gorm.DB, State) (State, error)) (out View, err error) {
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var owner entity.User
		e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND status = ?", user, "active").First(&owner).Error
		if e == gorm.ErrRecordNotFound {
			return entity.Fail("NOT_FOUND", "active user not found")
		}
		if e != nil {
			return e
		}
		state, e := load(tx, user)
		if e != nil {
			return e
		}
		next, e := fn(tx, state)
		if e != nil {
			return e
		}
		if e = save(tx, user, next); e != nil {
			return e
		}
		out = AsView(next)
		return nil
	})
	return
}
func reconcileDB(tx *gorm.DB, user int64, state State, now time.Time) (State, error) {
	var subs []entity.Subscription
	if e := tx.Where("user_id = ?", user).Order("id").Limit(1001).Find(&subs).Error; e != nil {
		return state, e
	}
	if len(subs) > 1000 {
		return state, entity.Fail("STATE_CONFLICT", "renewal scan supports up to 1000 subscriptions per user")
	}
	return Reconcile(state, subs, now, uuid.NewString)
}
func (s *Service) Generate(ctx context.Context, user int64, now time.Time) (View, error) {
	return s.mutate(ctx, user, func(tx *gorm.DB, state State) (State, error) { return reconcileDB(tx, user, state, now) })
}
func (s *Service) Configure(ctx context.Context, user int64, enabled bool, days int, expected int64) (View, error) {
	if days < 0 || days > 30 || expected < 1 {
		return View{}, entity.Fail("INVALID_ARGUMENT", "lead_days 0..30 and settings version required")
	}
	return s.mutate(ctx, user, func(tx *gorm.DB, state State) (State, error) {
		if state.Settings.Version != expected {
			return state, entity.Fail("VERSION_CONFLICT", "reminder settings changed")
		}
		state.Settings = Settings{Enabled: enabled, LeadDays: days, Version: expected + 1}
		return reconcileDB(tx, user, state, time.Now())
	})
}
func (s *Service) ReadNotice(ctx context.Context, user int64, id string, expected int64) (View, error) {
	if uuid.Validate(id) != nil || expected < 1 {
		return View{}, entity.Fail("INVALID_ARGUMENT", "notice ID and version required")
	}
	return s.mutate(ctx, user, func(tx *gorm.DB, state State) (State, error) {
		var e error
		state, e = reconcileDB(tx, user, state, time.Now())
		if e != nil {
			return state, e
		}
		return MarkRead(state, id, expected, time.Now())
	})
}

// Sweep uses keyset batches. Owner row locks also serialize separate worker processes.
func (s *Service) Sweep(ctx context.Context, now time.Time) error {
	var cursor int64
	var first error
	for {
		var ids []int64
		e := s.db.WithContext(ctx).Model(&entity.User{}).Where("id > ? AND status = ? AND (renewal_reminders IS NOT NULL OR EXISTS (SELECT 1 FROM banking_subscriptions WHERE banking_subscriptions.user_id = banking_users.id AND banking_subscriptions.status = ?))", cursor, "active", "active").Order("id").Limit(50).Pluck("id", &ids).Error
		if e != nil {
			return e
		}
		if len(ids) == 0 {
			return first
		}
		for _, user := range ids {
			cursor = user
			child, cancel := context.WithTimeout(ctx, 3*time.Second)
			_, err := s.Generate(child, user, now)
			cancel()
			if err != nil && first == nil {
				first = err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
	}
}
func (s *Service) Run(ctx context.Context, log *slog.Logger, interval time.Duration) {
	if interval <= 0 {
		interval = time.Minute
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		cycle, cancel := context.WithTimeout(ctx, 45*time.Second)
		e := s.Sweep(cycle, time.Now())
		cancel()
		if e != nil && ctx.Err() == nil {
			log.Warn("renewal_scan_failed")
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
