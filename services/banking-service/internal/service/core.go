package service

import (
	"context"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/risk"
	"strconv"
	"strings"
	"time"
)

type Core struct {
	repo    *repository.Core
	gateway risk.Gateway
}
type Meta struct {
	UserID          int64
	TraceID, TaskID string
}

func New(repo *repository.Core, gateway risk.Gateway) *Core {
	return &Core{repo: repo, gateway: gateway}
}
func text(v string, max int) bool { return strings.TrimSpace(v) != "" && len(v) <= max }
func audit(m Meta, action string, id int64, outcome, code string) *entity.Audit {
	return &entity.Audit{ID: uuid.NewString(), UserID: m.UserID, Action: action, ResourceID: id, Outcome: outcome, ErrorCode: code, TraceID: m.TraceID, TaskID: m.TaskID}
}
func active(a *entity.BankAccount) error {
	if !a.IsSimulated {
		return entity.Fail("FORBIDDEN", "only simulated accounts supported")
	}
	if a.Status != "active" {
		return entity.Fail("ACCOUNT_FROZEN", "account is not active")
	}
	return nil
}
func (s *Core) Register(ctx context.Context, m Meta, name string) (*entity.User, error) {
	name = strings.TrimSpace(name)
	if !text(name, 128) {
		return nil, entity.Fail("INVALID_ARGUMENT", "display_name required, maximum 128 bytes")
	}
	return s.repo.Register(ctx, m.UserID, name)
}

type AccountInput struct {
	DisplayName           string `json:"display_name"`
	AccountType           string `json:"account_type,omitempty"`
	Currency              string `json:"currency,omitempty"`
	InitialBalanceMinor   int64  `json:"initial_balance_minor"`
	ConfirmSimulatedFunds bool   `json:"confirm_simulated_funds"`
}

func (s *Core) CreateAccount(ctx context.Context, m Meta, key string, in AccountInput) (entity.BankAccount, error) {
	var zero entity.BankAccount
	if err := risk.CheckSensitive(ctx, s.gateway, m.UserID, "open_account", in); err != nil {
		return zero, err
	}
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	if in.AccountType == "" {
		in.AccountType = "checking"
	}
	if !text(in.DisplayName, 128) || (in.AccountType != "checking" && in.AccountType != "savings") {
		return zero, entity.Fail("INVALID_ARGUMENT", "display_name and checking/savings account_type required")
	}
	money, err := entity.Money(in.InitialBalanceMinor, in.Currency)
	if err != nil {
		return zero, err
	}
	in.Currency = money.Currency
	if _, trusted := s.gateway.(risk.SensitiveGuard); !trusted && in.InitialBalanceMinor > 0 && !in.ConfirmSimulatedFunds {
		return zero, entity.Fail("APPROVAL_REQUIRED", "confirm_simulated_funds must be true for initial funding")
	}
	return repository.Atomic(s.repo, ctx, m.UserID, "account.create", key, in, func(tx *repository.Tx) (entity.BankAccount, error) {
		if err := tx.RequireUser(); err != nil {
			return zero, err
		}
		a := entity.BankAccount{Base: entity.Base{UserID: m.UserID}, DisplayName: in.DisplayName, AccountType: in.AccountType, MaskedAccountNumber: "SIM-****", Currency: in.Currency, Status: "active", Version: 1, IsSimulated: true}
		if err := tx.Insert(&a); err != nil {
			return zero, err
		}
		ev := audit(m, "account.create", a.ID, "succeeded", "")
		if in.InitialBalanceMinor > 0 {
			journal := uuid.NewString()
			t := entity.Transaction{Base: entity.Base{UserID: m.UserID}, BankAccountID: a.ID, Direction: "income", AmountMinor: in.InitialBalanceMinor, Currency: a.Currency, Category: "opening", MerchantName: "Simulation clearing", Description: "Explicitly confirmed simulated opening funds", OccurredAt: time.Now().UnixMilli(), Kind: "opening", EffectMode: "apply_simulated_effect", JournalID: &journal, TraceID: m.TraceID, TaskID: m.TaskID, AuditID: ev.ID}
			if err := tx.Insert(&t); err != nil {
				return zero, err
			}
			if err := s.authorizeFunding(ctx, tx, m, t.ID); err != nil {
				return zero, err
			}
			rows := pair(m.UserID, journal, a.Currency, &a.ID, nil, &t.ID, nil, in.InitialBalanceMinor)
			if err := tx.Post(rows, map[int64]*entity.BankAccount{a.ID: &a}); err != nil {
				return zero, err
			}
		}
		if err := tx.Audit(ev); err != nil {
			return zero, err
		}
		return a, nil
	})
}
func pair(user int64, journal, currency string, a, b, ta, tb *int64, delta int64) []entity.LedgerEntry {
	side1, side2 := "debit", "credit"
	if delta < 0 {
		side1, side2 = side2, side1
	}
	return []entity.LedgerEntry{{UserID: user, JournalID: journal, Sequence: 1, AccountID: a, TransactionID: ta, Currency: currency, DeltaMinor: delta, Side: side1}, {UserID: user, JournalID: journal, Sequence: 2, AccountID: b, TransactionID: tb, Currency: currency, DeltaMinor: -delta, Side: side2}}
}
func (s *Core) SetAccountStatus(ctx context.Context, m Meta, id int64, status string) error {
	if err := risk.CheckSensitive(ctx, s.gateway, m.UserID, "account_status", map[string]any{"account_id": strconv.FormatInt(id, 10), "status": status}); err != nil {
		return err
	}
	if status != "active" && status != "frozen" && status != "inactive" && status != "closed" {
		return entity.Fail("INVALID_ARGUMENT", "invalid account status")
	}
	return s.repo.Transaction(ctx, m.UserID, func(tx *repository.Tx) error {
		if err := tx.RequireUser(); err != nil {
			return err
		}
		if err := tx.SetStatus(id, status); err != nil {
			return err
		}
		return tx.Audit(audit(m, "account.status", id, "succeeded", ""))
	})
}

type TransactionInput struct {
	BankAccountID          int64  `json:"bank_account_id,string"`
	ExternalTransactionID  string `json:"external_transaction_id"`
	Direction              string `json:"direction"`
	AmountMinor            int64  `json:"amount_minor"`
	Currency               string `json:"currency,omitempty"`
	Category               string `json:"category"`
	MerchantName           string `json:"merchant_name"`
	Description            string `json:"description"`
	OccurredAt             int64  `json:"occurred_at"`
	EffectMode             string `json:"effect_mode"`
	ConfirmSimulatedEffect bool   `json:"confirm_simulated_effect"`
}

func (s *Core) ImportTransaction(ctx context.Context, m Meta, key string, in TransactionInput) (entity.Transaction, error) {
	var zero entity.Transaction
	if err := risk.CheckSensitive(ctx, s.gateway, m.UserID, "import_transaction", in); err != nil {
		return zero, err
	}
	money, err := entity.Money(in.AmountMinor, in.Currency)
	if err != nil {
		return zero, err
	}
	in.Currency = money.Currency
	if in.BankAccountID <= 0 || in.AmountMinor <= 0 || !text(in.ExternalTransactionID, 128) || !text(in.Category, 64) || len(in.MerchantName) > 128 || len(in.Description) > 512 || in.OccurredAt <= 0 || (in.Direction != "income" && in.Direction != "expense") || (in.EffectMode != "record_only" && in.EffectMode != "apply_simulated_effect") {
		return zero, entity.Fail("INVALID_ARGUMENT", "invalid transaction parameters")
	}
	if _, trusted := s.gateway.(risk.SensitiveGuard); !trusted && in.EffectMode == "apply_simulated_effect" && !in.ConfirmSimulatedEffect {
		return zero, entity.Fail("APPROVAL_REQUIRED", "confirm_simulated_effect required")
	}
	return repository.Atomic(s.repo, ctx, m.UserID, "transaction.import", key, in, func(tx *repository.Tx) (entity.Transaction, error) {
		if err := tx.RequireUser(); err != nil {
			return zero, err
		}
		accounts, err := tx.LockAccounts(in.BankAccountID)
		if err != nil {
			return zero, err
		}
		a := accounts[in.BankAccountID]
		if a.Currency != in.Currency {
			return zero, entity.Fail("INVALID_ARGUMENT", "account currency mismatch")
		}
		if a.SavingsGoalID != nil {
			return zero, entity.Fail("INVALID_ARGUMENT", "use savings transfer endpoints for goal account")
		}
		if in.EffectMode == "apply_simulated_effect" {
			if err := active(a); err != nil {
				return zero, err
			}
		}
		ev := audit(m, "transaction.import", 0, "succeeded", "")
		t := entity.Transaction{Base: entity.Base{UserID: m.UserID}, BankAccountID: a.ID, ExternalTransactionID: &in.ExternalTransactionID, Direction: in.Direction, AmountMinor: in.AmountMinor, Currency: in.Currency, Category: in.Category, MerchantName: in.MerchantName, Description: in.Description, OccurredAt: in.OccurredAt, Kind: "external", EffectMode: in.EffectMode, TraceID: m.TraceID, TaskID: m.TaskID, AuditID: ev.ID}
		if in.EffectMode == "apply_simulated_effect" {
			j := uuid.NewString()
			t.JournalID = &j
		}
		if err := tx.Insert(&t); err != nil {
			return zero, err
		}
		if t.JournalID != nil {
			if err := s.authorizeFunding(ctx, tx, m, t.ID); err != nil {
				return zero, err
			}
			delta := in.AmountMinor
			if in.Direction == "expense" {
				delta = -delta
			}
			if err := tx.Post(pair(m.UserID, *t.JournalID, t.Currency, &a.ID, nil, &t.ID, nil, delta), accounts); err != nil {
				return zero, err
			}
		}
		ev.ResourceID = t.ID
		if err := tx.Audit(ev); err != nil {
			return zero, err
		}
		return t, nil
	})
}

func (s *Core) authorizeFunding(ctx context.Context, tx *repository.Tx, m Meta, transactionID int64) error {
	if s.gateway == nil {
		return entity.Fail("RISK_REJECTED", "safety gateway is not configured")
	}
	consent := audit(m, "simulation.funding.approve", transactionID, "approved", "")
	if err := tx.Audit(consent); err != nil {
		return err
	}
	t, a, err := tx.FundingAuthorization(transactionID, consent.ID)
	if err != nil {
		return err
	}
	return s.gateway.CheckFunding(ctx, m.UserID, t, a)
}
