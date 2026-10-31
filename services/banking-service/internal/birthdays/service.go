package birthdays

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/qingyingjin/banking-service/internal/entity"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Service struct{ db *gorm.DB }

func New(db *gorm.DB) *Service { return &Service{db: db} }
func load(db *gorm.DB, user int64) (State, error) {
	var raw sql.NullString
	if err := db.Model(&entity.User{}).Select("birthday_calendar").Where("id=? AND status=?", user, "active").Row().Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return State{}, entity.Fail("NOT_FOUND", "active user not found")
		}
		return State{}, err
	}
	if !raw.Valid {
		return Default(), nil
	}
	var state State
	if len(raw.String) > 32768 || json.Unmarshal([]byte(raw.String), &state) != nil || Validate(state) != nil {
		return State{}, entity.Fail("DEPENDENCY_UNAVAILABLE", "invalid saved birthday calendar")
	}
	return state, nil
}
func (s *Service) Read(ctx context.Context, user int64) (View, error) {
	state, err := load(s.db.WithContext(ctx), user)
	if err != nil {
		return View{}, err
	}
	return AsView(state, time.Now())
}
func (s *Service) Configure(ctx context.Context, user int64, in Input) (out View, err error) {
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var owner entity.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND status=?", user, "active").First(&owner).Error; err != nil {
			return err
		}
		old, err := load(tx, user)
		if err != nil {
			return err
		}
		next, err := Replace(old, in)
		if err != nil {
			return err
		}
		raw, err := json.Marshal(next)
		if err != nil {
			return err
		}
		if err = tx.Model(&entity.User{}).Where("id=?", user).UpdateColumn("birthday_calendar", string(raw)).Error; err != nil {
			return err
		}
		out, err = AsView(next, time.Now())
		return err
	})
	return
}
