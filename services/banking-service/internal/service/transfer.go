package service

import (
	"context"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/risk"
	"time"
)

type TransferInput struct {
	SourceAccountID int64  `json:"source_account_id,string"`
	TargetAccountID int64  `json:"target_account_id,string"`
	AmountMinor     int64  `json:"amount_minor"`
	Currency        string `json:"currency,omitempty"`
	Description     string `json:"description"`
}
type ApprovalInput struct {
	Decision     string `json:"decision"`
	ApprovalHash string `json:"approval_hash"`
}

func (s *Core) CreateTransfer(ctx context.Context, m Meta, key string, in TransferInput) (entity.TransferIntent, error) {
	return s.createTransfer(ctx, m, key, in, "transfer", nil)
}
func (s *Core) createTransfer(ctx context.Context, m Meta, key string, in TransferInput, purpose string, goalID *int64) (entity.TransferIntent, error) {
	var zero entity.TransferIntent
	money, err := entity.Money(in.AmountMinor, in.Currency)
	if err != nil {
		return zero, err
	}
	in.Currency = money.Currency
	if in.AmountMinor <= 0 || in.SourceAccountID <= 0 || in.TargetAccountID <= 0 || in.SourceAccountID == in.TargetAccountID || len(in.Description) > 512 {
		return zero, entity.Fail("INVALID_ARGUMENT", "positive amount and distinct source/target required")
	}
	payload := struct {
		TransferInput
		Purpose string
		GoalID  *int64
	}{in, purpose, goalID}
	return repository.Atomic(s.repo, ctx, m.UserID, "transfer.create", key, payload, func(tx *repository.Tx) (entity.TransferIntent, error) {
		if err := tx.RequireUser(); err != nil {
			return zero, err
		}
		accounts, err := tx.LockAccounts(in.SourceAccountID, in.TargetAccountID)
		if err != nil {
			return zero, err
		}
		source, target := accounts[in.SourceAccountID], accounts[in.TargetAccountID]
		if source.Currency != in.Currency || target.Currency != in.Currency {
			return zero, entity.Fail("INVALID_ARGUMENT", "account currency mismatch")
		}
		if purpose == "transfer" && (source.SavingsGoalID != nil || target.SavingsGoalID != nil) {
			return zero, entity.Fail("INVALID_ARGUMENT", "use goal deposit/withdrawal endpoints")
		}
		if purpose != "transfer" {
			savings, other := target, source
			if purpose == "savings_withdrawal" {
				savings, other = source, target
			}
			if savings.SavingsGoalID == nil || goalID == nil || *savings.SavingsGoalID != *goalID || other.SavingsGoalID != nil {
				return zero, entity.Fail("INVALID_ARGUMENT", "invalid goal funding accounts")
			}
		}
		ev := audit(m, "transfer.propose", 0, "succeeded", "")
		v := entity.TransferIntent{Base: entity.Base{UserID: m.UserID}, SourceAccountID: in.SourceAccountID, TargetAccountID: in.TargetAccountID, AmountMinor: in.AmountMinor, Currency: in.Currency, Description: in.Description, Purpose: purpose, SavingsGoalID: goalID, Status: "pending_approval", ExpiresAt: time.Now().Add(15 * time.Minute).UnixMilli(), AuditID: ev.ID, TraceID: m.TraceID, TaskID: m.TaskID}
		if err := risk.CheckTransferProposal(ctx, s.gateway, m.UserID, &v); err != nil {
			return zero, err
		}
		if err := tx.Insert(&v); err != nil {
			return zero, err
		}
		v.ApprovalHash = entity.TransferDigest(&v)
		if err := tx.SaveTransfer(&v); err != nil {
			return zero, err
		}
		ev.ResourceID = v.ID
		if err := tx.Audit(ev); err != nil {
			return zero, err
		}
		return v, nil
	})
}
func (s *Core) ApproveTransfer(ctx context.Context, m Meta, id int64, in ApprovalInput) (*entity.TransferIntent, error) {
	if (in.Decision != "approve" && in.Decision != "reject") || len(in.ApprovalHash) != 64 {
		return nil, entity.Fail("INVALID_ARGUMENT", "decision approve/reject and approval_hash required")
	}
	var result *entity.TransferIntent
	err := s.repo.Transaction(ctx, m.UserID, func(tx *repository.Tx) error {
		if err := tx.RequireUser(); err != nil {
			return err
		}
		v, err := tx.LockTransfer(id)
		if err != nil {
			return err
		}
		result = v
		if in.ApprovalHash != v.ApprovalHash || v.ApprovalHash != entity.TransferDigest(v) {
			return entity.Fail("IDEMPOTENCY_CONFLICT", "approval does not match immutable transfer")
		}
		if v.Status != "pending_approval" {
			a, err := tx.Approval(v.ID)
			if err != nil {
				return err
			}
			if a.Decision != in.Decision {
				return entity.Fail("IDEMPOTENCY_CONFLICT", "transfer already has a different decision")
			}
			return nil
		}
		a := entity.Approval{TransferID: v.ID, UserID: m.UserID, Decision: in.Decision, ApprovalHash: in.ApprovalHash, ExpiresAt: v.ExpiresAt}
		if err := tx.Insert(&a); err != nil {
			return err
		}
		if in.Decision == "reject" {
			v.Status = "rejected"
			ev := audit(m, "transfer.reject", id, "rejected", "")
			v.AuditID = ev.ID
			if err := tx.Audit(ev); err != nil {
				return err
			}
			return tx.SaveTransfer(v)
		}
		// Read persisted authorization before invoking the module8 safety gateway.
		saved, err := tx.Approval(v.ID)
		if err != nil {
			return err
		}
		if s.gateway == nil {
			return entity.Fail("RISK_REJECTED", "safety gateway is not configured")
		}
		if err = s.gateway.Check(ctx, m.UserID, v, saved); err != nil {
			return failTransfer(tx, m, v, err)
		}
		accounts, err := tx.LockAccounts(v.SourceAccountID, v.TargetAccountID)
		if err != nil {
			return err
		}
		source, target := accounts[v.SourceAccountID], accounts[v.TargetAccountID]
		if err = active(source); err != nil {
			return failTransfer(tx, m, v, err)
		}
		if err = active(target); err != nil {
			return failTransfer(tx, m, v, err)
		}
		if source.Currency != v.Currency || target.Currency != v.Currency {
			return failTransfer(tx, m, v, entity.Fail("INVALID_ARGUMENT", "currency mismatch"))
		}
		if _, err = entity.CheckedBalance(source.BalanceMinor, -v.AmountMinor); err != nil {
			return failTransfer(tx, m, v, err)
		}
		if _, err = entity.CheckedBalance(target.BalanceMinor, v.AmountMinor); err != nil {
			return failTransfer(tx, m, v, err)
		}
		journal := uuid.NewString()
		ev := audit(m, "transfer.execute", id, "succeeded", "")
		now := time.Now().UnixMilli()
		debit := entity.Transaction{Base: entity.Base{UserID: m.UserID}, BankAccountID: source.ID, Direction: "expense", AmountMinor: v.AmountMinor, Currency: v.Currency, Category: v.Purpose, Description: v.Description, OccurredAt: now, Kind: "internal_transfer", EffectMode: "apply_simulated_effect", JournalID: &journal, TransferID: &v.ID, TraceID: m.TraceID, TaskID: m.TaskID, AuditID: ev.ID}
		credit := debit
		credit.ID = 0
		credit.BankAccountID = target.ID
		credit.Direction = "income"
		if err = tx.Insert(&debit); err != nil {
			return err
		}
		if err = tx.Insert(&credit); err != nil {
			return err
		}
		if err = tx.Post(pair(m.UserID, journal, v.Currency, &source.ID, &target.ID, &debit.ID, &credit.ID, -v.AmountMinor), accounts); err != nil {
			return err
		}
		if v.SavingsGoalID != nil {
			delta := v.AmountMinor
			if v.Purpose == "savings_withdrawal" {
				delta = -delta
			}
			entry := entity.SavingsLedger{UserID: m.UserID, GoalID: *v.SavingsGoalID, TransferID: v.ID, JournalID: journal, DeltaMinor: delta, Currency: v.Currency}
			if err = tx.Insert(&entry); err != nil {
				return err
			}
		}
		if err = tx.ConsumeApproval(v.ID, now); err != nil {
			return err
		}
		if err = tx.Audit(ev); err != nil {
			return err
		}
		v.Status = "succeeded"
		v.JournalID = journal
		v.AuditID = ev.ID
		return tx.SaveTransfer(v)
	})
	return result, repository.DBError(err)
}
func failTransfer(tx *repository.Tx, m Meta, v *entity.TransferIntent, err error) error {
	// A gateway/validation business refusal commits only failure metadata. SQL
	// failures return an error and roll back all authorization and money changes.
	var code = entity.Code(err)
	if code == "DEPENDENCY_UNAVAILABLE" {
		return err
	}
	v.Status = "failed"
	v.FailureCode = code
	ev := audit(m, "transfer.execute", v.ID, "failed", code)
	v.AuditID = ev.ID
	if err := tx.Audit(ev); err != nil {
		return err
	}
	return tx.SaveTransfer(v)
}
