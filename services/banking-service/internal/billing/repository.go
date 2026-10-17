package billing

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	coreRepo "github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/service"
	"gorm.io/gorm"
	"time"
)

type Repository struct{ db *gorm.DB }

func NewRepository(db *gorm.DB) *Repository { return &Repository{db} }
func (r *Repository) Snapshot(ctx context.Context, user int64, p Normalized) (Snapshot, error) {
	s := Snapshot{Accounts: []entity.BankAccount{}, Transactions: []entity.Transaction{}, Budgets: []entity.Budget{}, At: time.Now().UnixMilli()}
	e := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var u entity.User
		if e := tx.Where("id=? AND status='active'", user).First(&u).Error; e != nil {
			return e
		}
		if p.AccountID != nil {
			var a entity.BankAccount
			if e := tx.Where("id=? AND user_id=?", *p.AccountID, user).First(&a).Error; e != nil {
				return e
			}
		}
		if e := tx.Where("user_id=?", user).Order("id ASC").Find(&s.Accounts).Error; e != nil {
			return e
		}
		if e := tx.Where("user_id=? AND occurred_at>=? AND occurred_at<? AND kind='external'", user, p.LoadFrom.UnixMilli(), p.LoadTo.UnixMilli()).Order("occurred_at ASC,id ASC").Limit(MaxRows + 1).Find(&s.Transactions).Error; e != nil {
			return e
		}
		if len(s.Transactions) > MaxRows {
			return entity.Fail("ANALYSIS_LIMIT", "analysis exceeds 10000 records; shorten the period")
		}
		if e := tx.Where("user_id=? AND month>=? AND month<=?", user, p.Start.UTC().Format("2006-01"), p.End.Add(-time.Nanosecond).UTC().Format("2006-01")).Order("month ASC,id ASC").Find(&s.Budgets).Error; e != nil {
			return e
		}
		for i := range s.Budgets {
			b := &s.Budgets[i]
			b.Categories = []entity.BudgetCategory{}
			b.Rules = []entity.BudgetRule{}
			if e := tx.Where("user_id=? AND budget_id=?", user, b.ID).Order("id ASC").Find(&b.Categories).Error; e != nil {
				return e
			}
			if e := tx.Where("user_id=? AND budget_id=?", user, b.ID).Order("id ASC").Find(&b.Rules).Error; e != nil {
				return e
			}
		}
		return nil
	}, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	return s, coreRepo.DBError(e)
}
func (r *Repository) Save(ctx context.Context, m service.Meta, report *Report) error {
	a := entity.Audit{ID: uuid.NewString(), UserID: m.UserID, Action: "billing." + report.Tool, Outcome: "succeeded", TraceID: m.TraceID, TaskID: m.TaskID}
	report.AuditID = a.ID
	report.TraceID = m.TraceID
	report.TaskID = m.TaskID
	raw, e := json.Marshal(report)
	if e != nil {
		return e
	}
	return r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row := StoredReport{ID: report.ID, UserID: m.UserID, ReportJSON: raw, AuditID: a.ID, TraceID: m.TraceID, TaskID: m.TaskID}
		if e := tx.Create(&row).Error; e != nil {
			return e
		}
		return tx.Create(&a).Error
	})
}
func (r *Repository) Get(ctx context.Context, user int64, id string) (Report, error) {
	var stored StoredReport
	var out Report
	e := r.db.WithContext(ctx).Where("user_id=? AND id=?", user, id).First(&stored).Error
	if e != nil {
		return out, coreRepo.DBError(e)
	}
	e = json.Unmarshal(stored.ReportJSON, &out)
	return out, e
}
