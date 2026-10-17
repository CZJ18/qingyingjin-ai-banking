package recipients

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/aa"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/phoneid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type Service struct{ db *gorm.DB }

func New(db *gorm.DB) *Service { return &Service{db} }

type record struct {
	ID                      int64
	DisplayName             string
	PhoneNumber             sql.NullString
	PhoneVerifiedAt         int64
	PhoneVerificationMethod string
	TransferDirectory       sql.NullString
	AccountName             string
	AccountMaskedNumber     string
}

const userFields = "id,display_name,phone_number,phone_verified_at,phone_verification_method,transfer_directory"

func settings(raw sql.NullString) (Settings, error) {
	v := Settings{Version: 1}
	if !raw.Valid {
		return v, nil
	}
	if len(raw.String) > 4096 || json.Unmarshal([]byte(raw.String), &v) != nil || v.Version < 1 || v.AccountID < 0 || v.Enabled && v.AccountID <= 0 {
		return Settings{}, entity.Fail("DEPENDENCY_UNAVAILABLE", "invalid recipient directory state")
	}
	return v, nil
}
func row(db *gorm.DB, user int64) (record, error) {
	var r record
	e := db.Table("banking_users").Select(userFields).Where("id=? AND status=?", user, "active").Take(&r).Error
	if e == gorm.ErrRecordNotFound {
		return r, entity.Fail("NOT_FOUND", "recipient not found")
	}
	return r, e
}
func (s *Service) Settings(ctx context.Context, user int64) (Settings, error) {
	r, e := row(s.db.WithContext(ctx), user)
	if e != nil {
		return Settings{}, e
	}
	return settings(r.TransferDirectory)
}
func usable(db *gorm.DB, user, id int64) (entity.BankAccount, error) {
	var a entity.BankAccount
	e := db.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=?", id, user).First(&a).Error
	if e == gorm.ErrRecordNotFound {
		return a, entity.Fail("NOT_FOUND", "authorized account not found")
	}
	if e != nil {
		return a, e
	}
	if !a.IsSimulated || a.Currency != "CNY" || a.SavingsGoalID != nil {
		return a, entity.Fail("INVALID_ARGUMENT", "simulated CNY ordinary account required")
	}
	if a.Status != "active" {
		return a, entity.Fail("ACCOUNT_FROZEN", "transfer account is not active")
	}
	return a, nil
}
func (s *Service) Configure(ctx context.Context, user int64, in SettingsInput) (out Settings, err error) {
	if in.ExpectedVersion < 1 {
		return out, entity.Fail("INVALID_ARGUMENT", "settings version required")
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := aa.LockUsers(tx, user); e != nil {
			return e
		}
		r, e := row(tx, user)
		if e != nil {
			return e
		}
		old, e := settings(r.TransferDirectory)
		if e != nil {
			return e
		}
		if old.Version != in.ExpectedVersion {
			return entity.Fail("VERSION_CONFLICT", "receiving settings changed")
		}
		id := int64(0)
		if in.Enabled {
			if !r.PhoneNumber.Valid || r.PhoneVerifiedAt <= 0 {
				return entity.Fail("INVALID_ARGUMENT", "verify the registered phone before enabling receiving")
			}
			if normalized, e := phoneid.Normalize(r.PhoneNumber.String); e != nil || normalized != r.PhoneNumber.String {
				return entity.Fail("INVALID_ARGUMENT", "valid verified phone required")
			}
			if _, e := usable(tx, user, in.AccountID); e != nil {
				return e
			}
			id = in.AccountID
		}
		out = Settings{Enabled: in.Enabled, AccountID: id, Version: old.Version + 1}
		raw, _ := json.Marshal(out)
		return tx.Model(&entity.User{}).Where("id=?", user).UpdateColumn("transfer_directory", string(raw)).Error
	})
	return
}
func public(r record, v Settings) Recipient {
	return Recipient{ID: r.ID, DisplayName: r.DisplayName, PhoneMasked: phoneid.Mask(r.PhoneNumber.String), AccountName: r.AccountName, AccountMaskedNumber: r.AccountMaskedNumber, Version: v.Version, PhoneVerificationMethod: r.PhoneVerificationMethod}
}
func (s *Service) Search(ctx context.Context, user int64, in SearchInput) (SearchResult, error) {
	out := SearchResult{Items: []Recipient{}}
	q, e := NormalizeQuery(in)
	if e != nil {
		return out, e
	}
	db := s.db.WithContext(ctx).Table("banking_users AS u")
	join := "JOIN banking_bank_accounts a ON a.user_id=u.id AND CAST(a.id AS CHAR)=JSON_UNQUOTE(JSON_EXTRACT(u.transfer_directory,'$.account_id'))"
	enabled := "JSON_EXTRACT(u.transfer_directory,'$.enabled')=true"
	if s.db.Dialector.Name() == "postgres" {
		join = "JOIN banking_bank_accounts a ON a.user_id=u.id AND a.id::text=(u.transfer_directory::jsonb->>'account_id')"
		enabled = "(u.transfer_directory::jsonb->>'enabled')='true'"
	}
	field := "u.display_name"
	if in.Kind == "phone" {
		field = "u.phone_number"
	}
	var rows []record
	e = db.Joins(join).Where("u.id<>? AND u.status=? AND u.phone_verified_at>0 AND u.transfer_directory IS NOT NULL AND "+enabled+" AND "+field+"=? AND a.status=? AND a.currency=? AND a.is_simulated=? AND a.savings_goal_id IS NULL", user, "active", q, "active", "CNY", true).Select("u.id,u.display_name,u.phone_number,u.phone_verified_at,u.phone_verification_method,u.transfer_directory,a.display_name AS account_name,a.masked_account_number AS account_masked_number").Order("u.id").Limit(21).Scan(&rows).Error
	if e != nil {
		return out, e
	}
	out.Limited = len(rows) > 20
	for i, r := range rows {
		if i == 20 {
			break
		}
		v, e := settings(r.TransferDirectory)
		if e != nil {
			return out, e
		}
		if !v.Enabled {
			return out, entity.Fail("DEPENDENCY_UNAVAILABLE", "inconsistent directory entry")
		}
		item := public(r, v)
		if item.PhoneMasked == "" {
			return out, entity.Fail("DEPENDENCY_UNAVAILABLE", "invalid recipient phone")
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}

// Resolve must run after the caller holds both sorted user locks in its transaction.
func Resolve(tx *gorm.DB, user int64, in PaymentInput) (Recipient, entity.BankAccount, entity.BankAccount, error) {
	var zero Recipient
	var a, b entity.BankAccount
	if e := ValidatePayment(user, in); e != nil {
		return zero, a, b, e
	}
	r, e := row(tx, in.RecipientID)
	if e != nil {
		return zero, a, b, e
	}
	v, e := settings(r.TransferDirectory)
	if e != nil {
		return zero, a, b, e
	}
	if !v.Enabled || !r.PhoneNumber.Valid || r.PhoneVerifiedAt <= 0 {
		return zero, a, b, entity.Fail("STATE_CONFLICT", "recipient no longer accepts directory transfers")
	}
	if v.Version != in.RecipientVersion {
		return zero, a, b, entity.Fail("VERSION_CONFLICT", "recipient settings changed; search and confirm again")
	}
	a, e = usable(tx, user, in.SourceAccountID)
	if e != nil {
		return zero, a, b, e
	}
	b, e = usable(tx, in.RecipientID, v.AccountID)
	if e != nil {
		return zero, a, b, e
	}
	r.AccountName = b.DisplayName
	r.AccountMaskedNumber = b.MaskedAccountNumber
	return public(r, v), a, b, nil
}
