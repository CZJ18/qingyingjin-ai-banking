package execution

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/aa"
	"github.com/qingyingjin/banking-service/internal/cards"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/planning"
	"github.com/qingyingjin/banking-service/internal/recipients"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/risk"
	"github.com/qingyingjin/banking-service/internal/service"
	"github.com/qingyingjin/banking-service/internal/subscription"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strconv"
	"time"
)

func (s *Service) Execute(ctx context.Context, a identity.Actor, m service.Meta, id, key string, in ExecuteInput) (out View, err error) {
	if err = permission(a, m, "execute"); err != nil {
		return out, s.failOperation(ctx, a, m, "EXECUTE", id, err)
	}
	if err = idempotency(key); err != nil {
		return out, s.failOperation(ctx, a, m, "EXECUTE", id, err)
	}
	owner, e := s.operationCounterparty(ctx, m.UserID, id)
	if e != nil {
		return out, s.failOperation(ctx, a, m, "EXECUTE", id, e)
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := lockOperationUsers(tx, m.UserID, owner); e != nil {
			return e
		}
		requestHash := entity.CanonicalJSONHash(marshal(struct {
			ID    string
			Input ExecuteInput
		}{id, in}))
		var idem entity.Idempotency
		ie := tx.Where("user_id = ? AND action = ? AND idem_key = ?", m.UserID, "gateway.execute", key).First(&idem).Error
		if ie == nil && idem.RequestHash != requestHash {
			return entity.Fail("IDEMPOTENCY_CONFLICT", "execution key reused with different intent or authorization")
		}
		if ie != nil && ie != gorm.ErrRecordNotFound {
			return ie
		}
		if ie == gorm.ErrRecordNotFound {
			idem = entity.Idempotency{UserID: m.UserID, Action: "gateway.execute", Key: key, RequestHash: requestHash}
			if e := tx.Create(&idem).Error; e != nil {
				return e
			}
		}
		op, e := owned(tx, m.UserID, id)
		if e != nil {
			return e
		}
		if !equal(in.IntentDigest, op.IntentDigest) || !equal(op.IntentDigest, entity.OperationDigest(&op)) || in.AuthorizationID == "" || in.AuthorizationID != op.AuthorizationID {
			return entity.Fail("AUTHORIZATION_MISMATCH", "authorization must match operation, amount, destination and owner")
		}
		var consent entity.UserAuthorization
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ? AND operation_id = ?", in.AuthorizationID, m.UserID, id).First(&consent).Error; e != nil {
			return entity.Fail("APPROVAL_REQUIRED", "persisted user authorization required")
		}
		if consent.Decision != "approve" || consent.TokenID == "" || !equal(consent.IntentDigest, op.IntentDigest) {
			return entity.Fail("APPROVAL_REQUIRED", "approved intent-bound user authorization required")
		}
		// Successful/failed attempts replay their persisted result; they cannot post again.
		if op.Status == "succeeded" || op.Status == "failed" || op.Status == "expired" {
			if e := audit(tx, m, actorName(a), &op, "execution_replay", "EXECUTE", op.ErrorCode); e != nil {
				return e
			}
			out, e = view(tx, op)
			return e
		}
		if op.Status != "authorized" || op.Version != in.ExpectedVersion || consent.ConsumedAt != 0 {
			return entity.Fail("STATE_CONFLICT", "operation state or version changed")
		}
		now := time.Now().UnixMilli()
		op.RiskLevel = "high"
		if op.ExpiresAt <= now || consent.ExpiresAt <= now {
			op.Status = "expired"
			op.ErrorCode = "APPROVAL_EXPIRED"
		} else if e := evaluate(tx, &op); e != nil {
			if entity.Code(e) == "DEPENDENCY_UNAVAILABLE" {
				return e
			}
			op.Status = "failed"
			op.ErrorCode = entity.Code(e)
			if e := riskEvent(tx, &op, "execution_policy", "deny", op.ErrorCode, map[string]any{"kind": op.Kind}); e != nil {
				return e
			}
		} else {
			op.Status = "executing"
			if e := tx.Save(&op).Error; e != nil {
				return e
			}
			var result any
			var code string
			// Isolate all domain writes, including direct budget-rule changes.
			// Domain failures can then be recorded without committing partial work.
			e := tx.Transaction(func(businessTx *gorm.DB) error {
				pc := risk.WithPermit(ctx, businessTx, op.ID, consent.ID, "execute")
				var err error
				result, code, err = s.perform(pc, businessTx, m, &op)
				if err != nil {
					return err
				}
				if code != "" {
					return nil
				} // Persist intentional failed legacy states.
				return postExpenseReserve(businessTx, &op)
			})
			if e != nil {
				var domain *entity.Error
				if !errors.As(e, &domain) {
					return e
				}
				code = domain.Code
				if err := riskEvent(tx, &op, "execution_policy", "deny", code, map[string]any{"kind": op.Kind}); err != nil {
					return err
				}
				result = map[string]any{}
			}
			op.Result = marshal(result)
			op.ErrorCode = code
			op.Status = "succeeded"
			if code != "" {
				op.Status = "failed"
			}
		}
		if e := ctx.Err(); e != nil {
			return e
		}
		consent.ConsumedAt = time.Now().UnixMilli()
		op.Version++
		if e := projectTerminal(tx, &op); e != nil {
			return e
		}
		if e := tx.Save(&consent).Error; e != nil {
			return e
		}
		if e := audit(tx, m, op.Agent, &op, op.Status, "EXECUTE", op.ErrorCode); e != nil {
			return e
		}
		if e := tx.Save(&op).Error; e != nil {
			return e
		}
		out, e = view(tx, op)
		return e
	})
	return out, s.failOperation(ctx, a, m, "EXECUTE", id, err)
}
func (s *Service) perform(ctx context.Context, tx *gorm.DB, m service.Meta, op *entity.OperationIntent) (any, string, error) {
	core := service.New(repository.New(tx), risk.TrustedGateway{})
	sub := subscription.New(repository.New(tx), risk.TrustedGateway{}, s.simulator)
	switch op.Kind {
	case "recipient_transfer":
		var p recipients.PaymentInput
		if e := strict(op.Payload, &p); e != nil {
			return nil, "", e
		}
		_, source, target, e := recipients.Resolve(tx, m.UserID, p)
		if e != nil {
			return nil, "", e
		}
		v, e := repository.PostRecipientPayment(ctx, tx, op, p, &source, &target)
		return v, "", e
	case "aa_payment":
		var p aa.PaymentInput
		if e := strict(op.Payload, &p); e != nil {
			return nil, "", e
		}
		portfolio, bill, share, source, target, e := aa.Payment(tx, m.UserID, p)
		if e != nil {
			return nil, "", e
		}
		result, e := repository.PostAAPayment(ctx, tx, op, p, &source, &target)
		if e != nil {
			return nil, "", e
		}
		if e = aa.MarkPaid(tx, p.OwnerID, portfolio, bill, share, op.ID); e != nil {
			return nil, "", e
		}
		return result, "", nil
	case "card_manage":
		var p cards.Input
		if e := strict(op.Payload, &p); e != nil {
			return nil, "", e
		}
		portfolio, e := cards.Read(tx, m.UserID)
		if e != nil {
			return nil, "", e
		}
		next, e := cards.Apply(portfolio, p, uuid.NewString(), time.Now().UnixMilli())
		if e != nil {
			return nil, "", e
		}
		if e = cards.Save(tx, m.UserID, next); e != nil {
			return nil, "", e
		}
		return map[string]any{"cards": next}, "", nil
	case "transfer", "savings_deposit", "savings_withdrawal":
		v, e := core.ApproveTransfer(ctx, m, op.ResourceID, service.ApprovalInput{Decision: "approve", ApprovalHash: op.ResourceDigest})
		if e != nil {
			return nil, "", e
		}
		return v, v.FailureCode, nil
	case "cancel_subscription":
		v, e := sub.Confirm(ctx, m, op.ResourceID, subscription.ConfirmInput{Decision: "approve", ConfirmationHash: op.ResourceDigest})
		return v, v.FailureCode, e
	case "open_account":
		var p service.AccountInput
		if e := strict(op.Payload, &p); e != nil {
			return nil, "", e
		}
		v, e := core.CreateAccount(ctx, m, "gateway:"+op.ID, p)
		return v, "", e
	case "import_transaction":
		var p service.TransactionInput
		if e := strict(op.Payload, &p); e != nil {
			return nil, "", e
		}
		v, e := core.ImportTransaction(ctx, m, "gateway:"+op.ID, p)
		return v, "", e
	case "create_budget":
		var p service.BudgetInput
		if e := strict(op.Payload, &p); e != nil {
			return nil, "", e
		}
		v, e := core.CreateBudget(ctx, m, "gateway:"+op.ID, p)
		return v, "", e
	case "create_savings_goal":
		var p service.GoalInput
		if e := strict(op.Payload, &p); e != nil {
			return nil, "", e
		}
		v, e := core.CreateGoal(ctx, m, "gateway:"+op.ID, p)
		return v, "", e
	case "account_status":
		var p StatusInput
		if e := strict(op.Payload, &p); e != nil {
			return nil, "", e
		}
		if e := core.SetAccountStatus(ctx, m, p.AccountID, p.Status); e != nil {
			return nil, "", e
		}
		v, e := account(tx, m.UserID, p.AccountID)
		return v, "", e
	case "subscription_manage":
		var p ManageInput
		if e := strict(op.Payload, &p); e != nil {
			return nil, "", e
		}
		v, e := sub.Manage(ctx, m, "gateway:"+op.ID, p.SubscriptionID, p.Input)
		return v, "", e
	case "budget_rule_update":
		var p BudgetRuleInput
		if e := strict(op.Payload, &p); e != nil {
			return nil, "", e
		}
		if e := (risk.TrustedGateway{}).SensitiveCheck(ctx, m.UserID, op.Kind, p); e != nil {
			return nil, "", e
		}
		r := tx.Model(&entity.BudgetRule{}).Where("id = ? AND budget_id = ? AND user_id = ?", p.RuleID, p.BudgetID, m.UserID).Update("enabled", p.Enabled)
		if r.Error != nil {
			return nil, "", r.Error
		}
		if r.RowsAffected == 0 {
			var saved entity.BudgetRule
			if e := tx.Where("id = ? AND budget_id = ? AND user_id = ?", p.RuleID, p.BudgetID, m.UserID).First(&saved).Error; e != nil {
				return nil, "", e
			}
			if saved.Enabled != p.Enabled {
				return nil, "", entity.Fail("STATE_CONFLICT", "budget rule changed")
			}
		}
		ev := planning.ReplanEvent{Base: entity.Base{UserID: m.UserID}, EventKey: "gateway:" + op.ID, Type: "necessary_expense_changed", ResourceID: strconv.FormatInt(p.BudgetID, 10), Reason: "user authorized budget rule update", Status: "pending", TraceID: m.TraceID, TaskID: m.TaskID}
		if e := tx.Create(&ev).Error; e != nil {
			return nil, "", e
		}
		v, e := budget(tx, m.UserID, p.BudgetID)
		return v, "", e
	}
	return nil, "", entity.Fail("INVALID_ARGUMENT", "unsupported operation")
}
