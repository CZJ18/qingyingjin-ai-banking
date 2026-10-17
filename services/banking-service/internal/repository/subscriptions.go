package repository

import (
	"context"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/entity"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// Subscription writes reuse the existing transaction/idempotency/audit machinery.
// No method in this file writes accounts, transfers or money ledgers.
func (tx *Tx) LockSubscriptionOwner() error {
	var u entity.User
	return DBError(tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND status='active'", tx.userID).First(&u).Error)
}
func (tx *Tx) SubscriptionRows(from, to, account int64) ([]entity.Transaction, error) {
	var rows []entity.Transaction
	q := tx.db.Where("user_id=? AND kind='external' AND direction='expense' AND occurred_at>=? AND occurred_at<?", tx.userID, from, to)
	if account > 0 {
		var a entity.BankAccount
		if e := tx.db.Where("user_id=? AND id=?", tx.userID, account).First(&a).Error; e != nil {
			return nil, DBError(e)
		}
		q = q.Where("bank_account_id=?", account)
	}
	e := q.Order("occurred_at,id").Limit(10001).Find(&rows).Error
	if len(rows) > 10000 {
		return nil, entity.Fail("ANALYSIS_LIMIT", "more than 10000 transactions; narrow detection period")
	}
	return rows, e
}
func (tx *Tx) LockSubscription(id int64) (*entity.Subscription, error) {
	var s entity.Subscription
	e := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id=? AND id=?", tx.userID, id).First(&s).Error
	return &s, DBError(e)
}
func (tx *Tx) StoreSubscriptionCandidate(s *entity.Subscription) (*entity.Subscription, error) {
	var old entity.Subscription
	e := tx.db.Where("user_id=? AND fingerprint=?", tx.userID, s.Fingerprint).First(&old).Error
	if e == gorm.ErrRecordNotFound {
		if e = tx.Insert(s); e != nil {
			return nil, e
		}
		return s, nil
	}
	if e != nil {
		return nil, e
	}
	if old.Status == "candidate" && (old.AmountMinor != s.AmountMinor || old.BillingInterval != s.BillingInterval || old.NextBillingAt != s.NextBillingAt || old.ConfidenceBPS != s.ConfidenceBPS) {
		s.Base = old.Base
		s.Version = old.Version + 1
		if e = tx.SaveSubscription(s); e != nil {
			return nil, e
		}
		return s, nil
	}
	return &old, nil
}
func (tx *Tx) SaveSubscription(s *entity.Subscription) error {
	if s.UserID != tx.userID {
		return entity.Fail("FORBIDDEN", "subscription owner mismatch")
	}
	return tx.db.Model(&entity.Subscription{}).Where("user_id=? AND id=?", tx.userID, s.ID).Select("category", "billing_interval", "amount_minor", "next_billing_at", "confidence_bps", "status", "version", "last_used_at", "cancelled_at", "updated_at").Updates(s).Error
}
func (tx *Tx) StoreSubscriptionPayment(p *entity.SubscriptionPayment) error {
	if p.UserID != tx.userID {
		return entity.Fail("FORBIDDEN", "payment owner mismatch")
	}
	var t entity.Transaction
	if e := tx.db.Where("user_id=? AND id=? AND kind='external' AND direction='expense'", tx.userID, p.TransactionID).First(&t).Error; e != nil {
		return DBError(e)
	}
	if t.AmountMinor != p.AmountMinor || t.OccurredAt != p.OccurredAt {
		return entity.Fail("INVALID_TRANSACTION_DATA", "evidence snapshot mismatch")
	}
	return tx.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "user_id"}, {Name: "transaction_id"}}, DoUpdates: clause.Assignments(map[string]any{"is_possible_duplicate": gorm.Expr("? OR ?", clause.Column{Table: p.TableName(), Name: "is_possible_duplicate"}, p.IsPossibleDuplicate)})}).Create(p).Error
}
func (tx *Tx) LockSubscriptionIntent(id int64) (*entity.SubscriptionActionIntent, error) {
	var i entity.SubscriptionActionIntent
	e := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id=? AND id=?", tx.userID, id).First(&i).Error
	return &i, DBError(e)
}
func (tx *Tx) SaveSubscriptionIntent(i *entity.SubscriptionActionIntent) error {
	return tx.db.Model(&entity.SubscriptionActionIntent{}).Where("user_id=? AND id=?", tx.userID, i.ID).Select("status", "confirmation_hash", "executed_at", "failure_code", "updated_at").Updates(i).Error
}
func (tx *Tx) SubscriptionApproval(id int64) (*entity.SubscriptionApproval, error) {
	var a entity.SubscriptionApproval
	e := tx.db.Where("user_id=? AND intent_id=?", tx.userID, id).First(&a).Error
	return &a, DBError(e)
}
func (tx *Tx) ConsumeSubscriptionApproval(id, at int64) error {
	result := tx.db.Model(&entity.SubscriptionApproval{}).Where("user_id=? AND intent_id=? AND consumed_at IS NULL", tx.userID, id).Update("consumed_at", at)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return entity.Fail("STATE_CONFLICT", "authorization already consumed")
	}
	return nil
}
func (tx *Tx) SubscriptionList() ([]entity.Subscription, error) {
	var rows []entity.Subscription
	e := tx.db.Where("user_id=?", tx.userID).Order("id").Limit(1001).Find(&rows).Error
	if len(rows) > 1000 {
		return nil, entity.Fail("ANALYSIS_LIMIT", "more than 1000 subscriptions")
	}
	return rows, e
}
func (tx *Tx) SubscriptionPayments(id int64) ([]entity.SubscriptionPayment, error) {
	var p []entity.SubscriptionPayment
	e := tx.db.Where("user_id=? AND subscription_id=?", tx.userID, id).Order("occurred_at,id").Limit(10001).Find(&p).Error
	if len(p) > 10000 {
		return nil, entity.Fail("ANALYSIS_LIMIT", "more than 10000 subscription payments")
	}
	return p, e
}
func (tx *Tx) SubscriptionHistory(id int64) ([]entity.SubscriptionActionIntent, error) {
	var h []entity.SubscriptionActionIntent
	e := tx.db.Where("user_id=? AND subscription_id=?", tx.userID, id).Order("id DESC").Limit(1001).Find(&h).Error
	if len(h) > 1000 {
		return nil, entity.Fail("ANALYSIS_LIMIT", "more than 1000 actions")
	}
	return h, e
}
func (tx *Tx) SetSubscriptionAnalysis(a *entity.SubscriptionAnalysis, report any) error {
	raw, e := json.Marshal(report)
	if e != nil {
		return e
	}
	a.Payload = raw
	return tx.db.Model(a).Update("payload", raw).Error
}
func (r *Core) GetSubscriptionAnalysis(ctx context.Context, user, id int64) (json.RawMessage, error) {
	var u entity.User
	if e := r.db.WithContext(ctx).Where("id=? AND status='active'", user).First(&u).Error; e != nil {
		return nil, DBError(e)
	}
	var a entity.SubscriptionAnalysis
	e := r.db.WithContext(ctx).Where("user_id=? AND id=?", user, id).First(&a).Error
	if e != nil {
		return nil, DBError(e)
	}
	return json.RawMessage(a.Payload), nil
}
