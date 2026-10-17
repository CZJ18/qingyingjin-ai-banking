package repository

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	legacy "github.com/coze-dev/coze-studio/backend/domain/banking/entity"
	legacyrepo "github.com/coze-dev/coze-studio/backend/domain/banking/repository"
	mysql "github.com/go-sql-driver/mysql"
	"github.com/qingyingjin/banking-service/internal/entity"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"sort"
	"strings"
	"time"
)

type Core struct {
	db     *gorm.DB
	legacy legacyrepo.BankAccountRepository
}
type Tx struct {
	db     *gorm.DB
	userID int64
}
type readOnlyIDs struct{}

func (readOnlyIDs) GenID(context.Context) (int64, error) {
	return 0, errors.New("legacy writes disabled")
}
func (readOnlyIDs) GenMultiIDs(context.Context, int) ([]int64, error) {
	return nil, errors.New("legacy writes disabled")
}
func New(db *gorm.DB) *Core {
	return &Core{db: db, legacy: legacyrepo.NewBankingRepository(db, readOnlyIDs{})}
}

func DBError(err error) error {
	var state interface{ SQLState() string }
	if errors.As(err, &state) {
		switch state.SQLState() {
		case "55000":
			if strings.Contains(err.Error(), "BANKING_STORAGE_FROZEN") {
				return entity.Fail("STORAGE_FROZEN", "financial storage is frozen for a verified migration")
			}
		case "23505":
			return entity.Fail("IDEMPOTENCY_CONFLICT", "unique business key already exists")
		case "40001", "40P01":
			return entity.Fail("VERSION_CONFLICT", "concurrent transaction changed; query current state before retry")
		}
	}
	var duplicate *mysql.MySQLError
	if errors.As(err, &duplicate) && duplicate.Number == 1644 && duplicate.Message == "BANKING_STORAGE_FROZEN" {
		return entity.Fail("STORAGE_FROZEN", "financial storage is frozen for a verified migration")
	}
	if errors.As(err, &duplicate) && duplicate.Number == 1062 {
		return entity.Fail("IDEMPOTENCY_CONFLICT", "unique business key already exists")
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return entity.Fail("NOT_FOUND", "resource not found")
	}
	return err
}
func (r *Core) Transaction(ctx context.Context, userID int64, fn func(*Tx) error) error {
	if userID <= 0 {
		return entity.Fail("INVALID_ARGUMENT", "positive user required")
	}
	return r.db.WithContext(ctx).Transaction(func(db *gorm.DB) error { return fn(&Tx{db: db, userID: userID}) })
}
func Atomic[T any](r *Core, ctx context.Context, userID int64, action, key string, input any, fn func(*Tx) (T, error)) (T, error) {
	var result T
	if len(key) == 0 || len(key) > 128 {
		return result, entity.Fail("INVALID_ARGUMENT", "Idempotency-Key required, maximum 128 ASCII characters")
	}
	for _, c := range key {
		if c < 33 || c > 126 {
			return result, entity.Fail("INVALID_ARGUMENT", "invalid Idempotency-Key")
		}
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return result, err
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	err = r.Transaction(ctx, userID, func(tx *Tx) error {
		entry := entity.Idempotency{UserID: userID, Action: action, Key: key, RequestHash: hash}
		if err := tx.db.Clauses(clause.OnConflict{DoNothing: true}).Create(&entry).Error; err != nil {
			return err
		}
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id=? AND action=? AND idem_key=?", userID, action, key).First(&entry).Error; err != nil {
			return err
		}
		if entry.RequestHash != hash {
			return entity.Fail("IDEMPOTENCY_CONFLICT", "key is already bound to different request parameters")
		}
		if len(entry.Response) > 0 {
			return json.Unmarshal(entry.Response, &result)
		}
		result, err = fn(tx)
		if err != nil {
			return err
		}
		raw, err = json.Marshal(result)
		if err != nil {
			return err
		}
		return tx.db.Model(&entity.Idempotency{}).Where("user_id=? AND action=? AND idem_key=?", userID, action, key).Update("response", raw).Error
	})
	return result, DBError(err)
}
func (tx *Tx) RequireUser() error {
	var u entity.User
	if err := tx.db.Where("id=? AND status='active'", tx.userID).First(&u).Error; err != nil {
		return DBError(err)
	}
	return nil
}
func (tx *Tx) Insert(value any) error { return tx.db.Create(value).Error }
func (tx *Tx) Audit(a *entity.Audit) error {
	if a.UserID != tx.userID {
		return entity.Fail("FORBIDDEN", "audit owner mismatch")
	}
	return tx.Insert(a)
}

func (tx *Tx) FundingAuthorization(transactionID int64, auditID string) (*entity.Transaction, *entity.Audit, error) {
	var t entity.Transaction
	var a entity.Audit
	if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id=? AND id=?", tx.userID, transactionID).First(&t).Error; err != nil {
		return nil, nil, DBError(err)
	}
	if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id=? AND id=?", tx.userID, auditID).First(&a).Error; err != nil {
		return nil, nil, DBError(err)
	}
	return &t, &a, nil
}
func (tx *Tx) LockAccounts(ids ...int64) (map[int64]*entity.BankAccount, error) {
	ids = append([]int64(nil), ids...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	rows := map[int64]*entity.BankAccount{}
	for _, id := range ids {
		if _, ok := rows[id]; ok {
			continue
		}
		var a entity.BankAccount
		if err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id=? AND id=?", tx.userID, id).First(&a).Error; err != nil {
			return nil, DBError(err)
		}
		rows[id] = &a
	}
	return rows, nil
}
func (tx *Tx) LockTransfer(id int64) (*entity.TransferIntent, error) {
	var v entity.TransferIntent
	err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id=? AND id=?", tx.userID, id).First(&v).Error
	return &v, DBError(err)
}
func (tx *Tx) SaveTransfer(v *entity.TransferIntent) error {
	return tx.db.Model(&entity.TransferIntent{}).Where("user_id=? AND id=?", tx.userID, v.ID).Select("status", "approval_hash", "journal_id", "failure_code", "audit_id", "updated_at").Updates(v).Error
}
func (tx *Tx) Approval(id int64) (*entity.Approval, error) {
	var a entity.Approval
	err := tx.db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id=? AND transfer_id=?", tx.userID, id).First(&a).Error
	return &a, DBError(err)
}
func (tx *Tx) ConsumeApproval(id int64, at int64) error {
	result := tx.db.Model(&entity.Approval{}).Where("user_id=? AND transfer_id=? AND consumed_at=0", tx.userID, id).Update("consumed_at", at)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return entity.Fail("APPROVAL_REQUIRED", "approval already consumed")
	}
	return nil
}
func (tx *Tx) BindGoal(accountID, goalID int64) error {
	return tx.db.Model(&entity.BankAccount{}).Where("user_id=? AND id=?", tx.userID, accountID).Update("savings_goal_id", goalID).Error
}
func (tx *Tx) SetStatus(id int64, status string) error {
	rows, err := tx.LockAccounts(id)
	if err != nil {
		return err
	}
	a := rows[id]
	if a.SavingsGoalID != nil {
		return entity.Fail("INVALID_ARGUMENT", "goal account status is managed by savings module")
	}
	if a.Status == "closed" && status != "closed" {
		return entity.Fail("ACCOUNT_CLOSED", "closed account cannot be reopened")
	}
	if status == "closed" && a.BalanceMinor != 0 {
		return entity.Fail("INVALID_ARGUMENT", "only a zero balance account may close")
	}
	return tx.db.Model(&entity.BankAccount{}).Where("user_id=? AND id=?", tx.userID, id).Updates(map[string]any{"status": status, "version": gorm.Expr("version+1"), "updated_at": time.Now().UnixMilli()}).Error
}

// Post is the sole balance-writing path. Callers supply transaction-scoped
// locked accounts; each movement requires a validated balanced journal.
func (tx *Tx) Post(rows []entity.LedgerEntry, accounts map[int64]*entity.BankAccount) error {
	if err := entity.ValidateLedger(rows); err != nil {
		return err
	}
	next := map[int64]int64{}
	for _, leg := range rows {
		if leg.UserID != tx.userID {
			return entity.Fail("FORBIDDEN", "journal owner mismatch")
		}
		if leg.AccountID == nil {
			continue
		}
		a, ok := accounts[*leg.AccountID]
		if !ok || a.UserID != tx.userID {
			return entity.Fail("NOT_FOUND", "locked account missing")
		}
		if a.Currency != leg.Currency {
			return entity.Fail("INVALID_ARGUMENT", "journal currency mismatch")
		}
		current := a.BalanceMinor
		if n, exists := next[a.ID]; exists {
			current = n
		}
		n, err := entity.CheckedBalance(current, leg.DeltaMinor)
		if err != nil {
			return err
		}
		next[a.ID] = n
	}
	if err := tx.db.Create(&rows).Error; err != nil {
		return err
	}
	ids := make([]int64, 0, len(next))
	for id := range next {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		a := accounts[id]
		res := tx.db.Model(&entity.BankAccount{}).Where("user_id=? AND id=? AND version=?", tx.userID, id, a.Version).Updates(map[string]any{"balance_minor": next[id], "version": a.Version + 1, "updated_at": time.Now().UnixMilli()})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return entity.Fail("VERSION_CONFLICT", "account version changed")
		}
		a.BalanceMinor = next[id]
		a.Version++
	}
	return nil
}
func Owned[T any](r *Core, ctx context.Context, user, id int64) (*T, error) {
	var v T
	err := r.db.WithContext(ctx).Where("user_id=? AND id=?", user, id).First(&v).Error
	return &v, DBError(err)
}
func List[T any](r *Core, ctx context.Context, user int64, filters map[string]any, options *legacy.ListOptions) ([]T, error) {
	o := legacy.NormalizeListOptions(options)
	rows := make([]T, 0)
	q := r.db.WithContext(ctx).Where("user_id=?", user)
	if len(filters) > 0 {
		q = q.Where(filters)
	}
	err := q.Order("id DESC").Limit(o.Limit).Offset(o.Offset).Find(&rows).Error
	return rows, DBError(err)
}
func (r *Core) Account(ctx context.Context, user, id int64) (*entity.BankAccount, error) {
	// Reuse the existing public tenant-scoped Banking repository read. Its
	// unrestricted UpdateBankAccount is deliberately excluded from this adapter.
	old, err := r.legacy.GetBankAccount(ctx, user, id)
	if err != nil {
		return nil, err
	}
	if old == nil {
		return nil, entity.Fail("NOT_FOUND", "account not found")
	}
	return Owned[entity.BankAccount](r, ctx, user, id)
}
func (r *Core) User(ctx context.Context, id int64) (*entity.User, error) {
	var u entity.User
	err := r.db.WithContext(ctx).First(&u, id).Error
	return &u, DBError(err)
}
func (r *Core) Register(ctx context.Context, id int64, name string) (*entity.User, error) {
	u := entity.User{ID: id, DisplayName: name, Status: "active"}
	err := r.db.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&u).Error
	if err != nil {
		return nil, err
	}
	return r.User(ctx, id)
}

type TransactionFilter struct {
	AccountID                     int64
	Direction, Category, Merchant string
	From, To                      int64
	Limit, Offset                 int
}

func (r *Core) Transactions(ctx context.Context, user int64, f TransactionFilter) ([]entity.Transaction, int64, error) {
	q := r.db.WithContext(ctx).Model(&entity.Transaction{}).Where("user_id=?", user)
	if f.AccountID > 0 {
		q = q.Where("bank_account_id=?", f.AccountID)
	}
	if f.Direction != "" {
		q = q.Where("direction=?", f.Direction)
	}
	if f.Category != "" {
		q = q.Where("category=?", f.Category)
	}
	if f.Merchant != "" {
		q = q.Where("merchant_name=?", f.Merchant)
	}
	if f.From > 0 {
		q = q.Where("occurred_at>=?", f.From)
	}
	if f.To > 0 {
		q = q.Where("occurred_at<?", f.To)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	rows := make([]entity.Transaction, 0)
	o := legacy.NormalizeListOptions(&legacy.ListOptions{Limit: f.Limit, Offset: f.Offset})
	err := q.Order("occurred_at DESC,id DESC").Limit(o.Limit).Offset(o.Offset).Find(&rows).Error
	return rows, total, err
}
func (r *Core) Balance(ctx context.Context, user, id int64) (entity.AccountBalance, error) {
	var result entity.AccountBalance
	err := r.Transaction(ctx, user, func(tx *Tx) error {
		rows, err := tx.LockAccounts(id)
		if err != nil {
			return err
		}
		a := rows[id]
		var sum int64
		if err := tx.db.Model(&entity.LedgerEntry{}).Select("COALESCE(SUM(delta_minor),0)").Where("user_id=? AND account_id=?", user, id).Scan(&sum).Error; err != nil {
			return err
		}
		result = entity.AccountBalance{AccountID: id, Currency: a.Currency, BalanceMinor: a.BalanceMinor, LedgerBalanceMinor: sum, Version: a.Version, Reconciled: sum == a.BalanceMinor}
		return nil
	})
	return result, err
}

type CategorySpend struct {
	Category   string `json:"category"`
	SpentMinor int64  `json:"spent_minor"`
}

func (r *Core) Spending(ctx context.Context, user int64, from, to int64, currency string) ([]CategorySpend, error) {
	rows := make([]CategorySpend, 0)
	err := r.db.WithContext(ctx).Model(&entity.Transaction{}).Select("category,SUM(amount_minor) AS spent_minor").Where("user_id=? AND occurred_at>=? AND occurred_at<? AND currency=? AND direction='expense' AND kind='external'", user, from, to, currency).Group("category").Scan(&rows).Error
	return rows, err
}
