package lifestyle

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/qingyingjin/banking-service/internal/entity"
	coreRepo "github.com/qingyingjin/banking-service/internal/repository"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strings"
)

type Repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) *Repository { return &Repository{db} }
func digest(v any) string                   { raw, _ := json.Marshal(v); return fmt.Sprintf("%x", sha256.Sum256(raw)) }
func (r *Repository) transaction(ctx context.Context, user int64, fn func(*gorm.DB) error) error {
	if user <= 0 {
		return entity.Fail("INVALID_ARGUMENT", "positive user required")
	}
	return coreRepo.DBError(r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var u entity.User
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND status='active'", user).First(&u).Error; e != nil {
			return e
		}
		return fn(tx)
	}))
}

// atomic shares the existing durable idempotency store and active-user lock.
func atomic[T any](r *Repository, ctx context.Context, muser int64, action, key string, in any, fn func(*gorm.DB) (T, error)) (T, error) {
	var out T
	if key == "" || len(key) > 128 || strings.IndexFunc(key, func(r rune) bool { return r < 33 || r > 126 }) >= 0 {
		return out, entity.Fail("INVALID_ARGUMENT", "Idempotency-Key required, 1..128 ASCII characters")
	}
	hash := digest(in)
	e := r.transaction(ctx, muser, func(tx *gorm.DB) error {
		var cached entity.Idempotency
		q := tx.Where("user_id=? AND action=? AND idem_key=?", muser, action, key).Find(&cached)
		if q.Error != nil {
			return q.Error
		}
		if q.RowsAffected > 0 {
			if cached.RequestHash != hash {
				return entity.Fail("IDEMPOTENCY_CONFLICT", "request key is already bound to another input")
			}
			return json.Unmarshal(cached.Response, &out)
		}
		var err error
		out, err = fn(tx)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(out)
		if err != nil {
			return err
		}
		return tx.Create(&entity.Idempotency{UserID: muser, Action: action, Key: key, RequestHash: hash, Response: raw}).Error
	})
	return out, e
}
func ownedGoal(tx *gorm.DB, user, id int64) (Goal, error) {
	var g Goal
	e := tx.Where("id=? AND user_id=?", id, user).First(&g).Error
	return g, e
}
func goalView(tx *gorm.DB, g Goal, pagination ...int) (GoalView, error) {
	v := GoalView{Goal: g, Versions: []GoalVersion{}}
	limit, offset, e := page(pagination)
	if e != nil {
		return v, e
	}
	e = tx.Where("goal_id=? AND user_id=?", g.ID, g.UserID).Order("version DESC").Limit(limit).Offset(offset).Find(&v.Versions).Error
	return v, e
}
func page(args []int) (int, int, error) {
	limit, offset := 100, 0
	if len(args) == 2 {
		limit, offset = args[0], args[1]
	} else if len(args) != 0 {
		return 0, 0, entity.Fail("INVALID_ARGUMENT", "invalid pagination")
	}
	if limit < 1 || limit > 100 || offset < 0 || offset > 1000000 {
		return 0, 0, entity.Fail("INVALID_ARGUMENT", "invalid pagination")
	}
	return limit, offset, nil
}
func planView(tx *gorm.DB, user, id int64) (PlanView, error) {
	v := PlanView{Recommendations: []Recommendation{}}
	if e := tx.Where("id=? AND user_id=?", id, user).First(&v.Plan).Error; e != nil {
		return v, e
	}
	e := tx.Where("plan_id=? AND user_id=?", id, user).Order("id").Find(&v.Recommendations).Error
	return v, e
}
