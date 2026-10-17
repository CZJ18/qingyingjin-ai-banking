package execution

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/aa"
	"github.com/qingyingjin/banking-service/internal/cards"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/recipients"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/risk"
	"github.com/qingyingjin/banking-service/internal/service"
	"github.com/qingyingjin/banking-service/internal/subscription"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strconv"
	"strings"
	"time"
)

func idempotency(key string) error {
	if len(key) < 1 || len(key) > 128 || strings.TrimSpace(key) != key {
		return entity.Fail("INVALID_ARGUMENT", "Idempotency-Key of 1..128 ASCII characters required")
	}
	for _, c := range key {
		if c < 33 || c > 126 {
			return entity.Fail("INVALID_ARGUMENT", "ASCII Idempotency-Key required")
		}
	}
	return nil
}
func account(tx *gorm.DB, user, id int64) (entity.BankAccount, error) {
	var v entity.BankAccount
	e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", id, user).First(&v).Error
	if e == gorm.ErrRecordNotFound {
		return v, entity.Fail("NOT_FOUND", "authorized account not found")
	}
	if e == nil && !v.IsSimulated {
		e = entity.Fail("FORBIDDEN", "simulation only")
	}
	return v, e
}
func subscriptionRow(tx *gorm.DB, user, id int64) (entity.Subscription, error) {
	var v entity.Subscription
	e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", id, user).First(&v).Error
	if e == gorm.ErrRecordNotFound {
		e = entity.Fail("NOT_FOUND", "authorized subscription not found")
	}
	return v, e
}
func budget(tx *gorm.DB, user, id int64) (entity.Budget, error) {
	var v entity.Budget
	e := tx.Where("id = ? AND user_id = ?", id, user).First(&v).Error
	if e == gorm.ErrRecordNotFound {
		e = entity.Fail("NOT_FOUND", "authorized budget not found")
	}
	if e != nil {
		return v, e
	}
	e = tx.Where("budget_id = ? AND user_id = ?", id, user).Order("id").Find(&v.Categories).Error
	if e == nil {
		e = tx.Where("budget_id = ? AND user_id = ?", id, user).Order("id").Find(&v.Rules).Error
	}
	return v, e
}
func (s *Service) Prepare(ctx context.Context, a identity.Actor, m service.Meta, key string, in PrepareInput) (out View, err error) {
	if (in.Kind == "card_manage" || in.Kind == "aa_payment" || in.Kind == "recipient_transfer") && a.Kind != "user" {
		return out, s.fail(ctx, a, m, "PREPARE", entity.Fail("FORBIDDEN", "this operation requires the human user"))
	}
	if a.Kind == "agent" && a.AgentType != "supervisor" && !(a.AgentType == "subscription" && in.Kind == "cancel_subscription") {
		return out, s.fail(ctx, a, m, "PREPARE", entity.Fail("FORBIDDEN", "signed specialist cannot prepare this operation"))
	}
	if err = permission(a, m, "prepare"); err != nil {
		return out, s.fail(ctx, a, m, "PREPARE", err)
	}
	if err = idempotency(key); err != nil {
		return out, s.fail(ctx, a, m, "PREPARE", err)
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := lockPrepareUsers(tx, m.UserID, in); e != nil {
			return e
		}
		v, e := s.prepareTx(ctx, tx, a, m, key, in)
		out = v
		return e
	})
	return out, s.fail(ctx, a, m, "PREPARE", err)
}
func (s *Service) prepareTx(ctx context.Context, tx *gorm.DB, a identity.Actor, m service.Meta, key string, in PrepareInput) (out View, err error) {
	skill := preparationSkill()
	if !kindValid(in.Kind) {
		return out, entity.Fail("INVALID_ARGUMENT", "unsupported operation kind")
	}
	if in.ExpiresInSeconds == 0 {
		in.ExpiresInSeconds = 900
	}
	if in.ExpiresInSeconds < 1 || in.ExpiresInSeconds > 900 {
		return out, entity.Fail("INVALID_ARGUMENT", "operation lifetime must be 1..900 seconds")
	}
	if entity.CanonicalJSONHash(in.Parameters) == "" {
		return out, entity.Fail("INVALID_ARGUMENT", "invalid parameters")
	}
	hash := entity.CanonicalJSONHash(marshal(in))
	var old entity.OperationIntent
	e := tx.Where("user_id = ? AND idempotency_key = ?", m.UserID, key).First(&old).Error
	if e == nil {
		if old.RequestHash != hash {
			return out, entity.Fail("IDEMPOTENCY_CONFLICT", "key reused with different operation")
		}
		if e = audit(tx, m, actorName(a), &old, "prepare_replay", "PREPARE", ""); e != nil {
			return out, e
		}
		return view(tx, old)
	}
	if e != gorm.ErrRecordNotFound {
		return out, e
	}
	op := entity.OperationIntent{SkillID: skill.ID, SkillVersion: skill.Version, ID: uuid.NewString(), UserID: m.UserID, Kind: in.Kind, Permission: "EXECUTE", Agent: actorName(a), Payload: in.Parameters, Snapshot: json.RawMessage(`{}`), Result: json.RawMessage(`{}`), Nonce: uuid.NewString(), Status: "pending_authorization", Version: 1, ExpiresAt: time.Now().Add(time.Duration(in.ExpiresInSeconds) * time.Second).UnixMilli(), Currency: "CNY", RiskLevel: "high", TraceID: m.TraceID, TaskID: m.TaskID, IdempotencyKey: key, RequestHash: hash}
	var typed any
	switch in.Kind {
	case "recipient_transfer":
		var p recipients.PaymentInput
		if e := strict(in.Parameters, &p); e != nil {
			return out, e
		}
		r, source, target, e := recipients.Resolve(tx, m.UserID, p)
		if e != nil {
			return out, e
		}
		typed = p
		op.SourceAccountID, op.TargetAccountID, op.AmountMinor = source.ID, target.ID, p.AmountMinor
		op.ResourceDigest = recipients.Fingerprint(r)
		op.Snapshot = marshal(map[string]any{"recipient": r, "source_account": source, "target_account": map[string]any{"id": strconv.FormatInt(target.ID, 10), "display_name": target.DisplayName, "masked_account_number": target.MaskedAccountNumber}})
	case "aa_payment":
		var p aa.PaymentInput
		if e := strict(in.Parameters, &p); e != nil {
			return out, e
		}
		_, bill, share, source, target, e := aa.Payment(tx, m.UserID, p)
		if e != nil {
			return out, e
		}
		typed = p
		op.SourceAccountID, op.TargetAccountID, op.AmountMinor = source.ID, target.ID, share.AmountMinor
		op.Snapshot = marshal(map[string]any{"bill_title": bill.Title, "participant_name": share.Name, "source_account": source, "target_account": map[string]any{"id": target.ID, "display_name": target.DisplayName, "masked_account_number": target.MaskedAccountNumber}})
	case "card_manage":
		var p cards.Input
		if e := strict(in.Parameters, &p); e != nil {
			return out, e
		}
		portfolio, e := cardSnapshot(tx, m.UserID, p)
		if e != nil {
			return out, e
		}
		typed = p
		op.Snapshot = marshal(map[string]any{"cards": portfolio})
		op.ResourceDigest = entity.CanonicalJSONHash(marshal(portfolio))
	case "transfer":
		var p service.TransferInput
		if e := strict(in.Parameters, &p); e != nil {
			return out, e
		}
		typed = p
		op.SourceAccountID = p.SourceAccountID
		op.TargetAccountID = p.TargetAccountID
		op.AmountMinor = p.AmountMinor
		if p.Currency != "" {
			op.Currency = p.Currency
		}
	case "savings_deposit", "savings_withdrawal":
		var p SavingsInput
		if e := strict(in.Parameters, &p); e != nil {
			return out, e
		}
		typed = p
		var g entity.SavingsGoal
		if e := tx.Where("id = ? AND user_id = ? AND status = ?", p.GoalID, m.UserID, "active").First(&g).Error; e != nil {
			if e == gorm.ErrRecordNotFound {
				e = entity.Fail("NOT_FOUND", "savings goal not found")
			}
			return out, e
		}
		op.SourceAccountID = p.Input.BankAccountID
		op.TargetAccountID = g.AccountID
		if in.Kind == "savings_withdrawal" {
			op.SourceAccountID, op.TargetAccountID = op.TargetAccountID, op.SourceAccountID
		}
		op.AmountMinor = p.Input.AmountMinor
		op.Currency = g.Currency
	case "cancel_subscription":
		var p subscription.IntentInput
		if e := strict(in.Parameters, &p); e != nil {
			return out, e
		}
		typed = p
		id, e := strconv.ParseInt(p.SubscriptionID, 10, 64)
		if e != nil || id <= 0 {
			return out, entity.Fail("INVALID_ARGUMENT", "subscription ID required")
		}
		v, e := subscriptionRow(tx, m.UserID, id)
		if e != nil {
			return out, e
		}
		op.SourceAccountID = v.BankAccountID
		op.AmountMinor = v.AmountMinor
		op.Currency = v.Currency
		op.Snapshot = marshal(v)
	case "open_account":
		var p service.AccountInput
		if e := strict(in.Parameters, &p); e != nil {
			return out, e
		}
		typed = p
		op.AmountMinor = p.InitialBalanceMinor
		if p.Currency != "" {
			op.Currency = p.Currency
		}
		if strings.TrimSpace(p.DisplayName) == "" || len(p.DisplayName) > 128 || (p.AccountType != "" && p.AccountType != "checking" && p.AccountType != "savings") {
			return out, entity.Fail("INVALID_ARGUMENT", "valid simulated account required")
		}
	case "import_transaction":
		var p service.TransactionInput
		if e := strict(in.Parameters, &p); e != nil {
			return out, e
		}
		typed = p
		op.SourceAccountID = p.BankAccountID
		op.AmountMinor = p.AmountMinor
		if p.Currency != "" {
			op.Currency = p.Currency
		}
		v, e := account(tx, m.UserID, p.BankAccountID)
		if e != nil {
			return out, e
		}
		op.Snapshot = marshal(v)
		if strings.TrimSpace(p.ExternalTransactionID) == "" || len(p.ExternalTransactionID) > 128 || strings.TrimSpace(p.Category) == "" || len(p.Category) > 64 || len(p.MerchantName) > 128 || len(p.Description) > 512 || p.OccurredAt <= 0 || p.Direction != "income" && p.Direction != "expense" || p.AmountMinor <= 0 || (p.EffectMode != "record_only" && p.EffectMode != "apply_simulated_effect") {
			return out, entity.Fail("INVALID_ARGUMENT", "valid transaction direction and effect required")
		}
	case "account_status":
		var p StatusInput
		if e := strict(in.Parameters, &p); e != nil {
			return out, e
		}
		typed = p
		v, e := account(tx, m.UserID, p.AccountID)
		if e != nil {
			return out, e
		}
		if p.ExpectedVersion != v.Version {
			return out, entity.Fail("STATE_CONFLICT", "account version changed")
		}
		if p.Status != "active" && p.Status != "frozen" && p.Status != "inactive" && p.Status != "closed" {
			return out, entity.Fail("INVALID_ARGUMENT", "invalid account status")
		}
		op.SourceAccountID = p.AccountID
		op.ResourceID = p.AccountID
		op.Snapshot = marshal(v)
	case "create_budget":
		var p service.BudgetInput
		if e := strict(in.Parameters, &p); e != nil {
			return out, e
		}
		typed = p
		op.AmountMinor = p.TotalMinor
		if p.Currency != "" {
			op.Currency = p.Currency
		}
		if p.TotalMinor <= 0 || len(p.Categories) == 0 || len(p.Categories) > 100 {
			return out, entity.Fail("INVALID_ARGUMENT", "valid budget required")
		}
		if month, e := time.Parse("2006-01", p.Month); e != nil || month.Year() < 2000 || month.Year() > 2100 {
			return out, entity.Fail("INVALID_ARGUMENT", "month required")
		}
		var sum int64
		seen := map[string]bool{}
		for _, c := range p.Categories {
			if strings.TrimSpace(c.Category) == "" || len(c.Category) > 64 || seen[c.Category] || c.AllocatedMinor < 0 || c.AllocatedMinor > p.TotalMinor || c.EssentialReserveMinor < 0 || c.EssentialReserveMinor > c.AllocatedMinor {
				return out, entity.Fail("INVALID_ARGUMENT", "invalid categories")
			}
			seen[c.Category] = true
			sum += c.AllocatedMinor
			if sum > p.TotalMinor {
				return out, entity.Fail("INVALID_ARGUMENT", "category allocation exceeds total")
			}
		}
	case "create_savings_goal":
		var p service.GoalInput
		if e := strict(in.Parameters, &p); e != nil {
			return out, e
		}
		typed = p
		op.AmountMinor = p.TargetMinor
		if p.Currency != "" {
			op.Currency = p.Currency
		}
		if p.TargetMinor <= 0 || p.MonthlyMinor <= 0 || p.MonthlyMinor > entity.MaxBalance || p.EndsAt <= p.StartsAt || p.StartsAt <= 0 || p.StartsAt > time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli() || p.EndsAt-p.StartsAt > int64(10*365*24*time.Hour/time.Millisecond) || strings.TrimSpace(p.Name) == "" || len(p.Name) > 128 {
			return out, entity.Fail("INVALID_ARGUMENT", "valid savings goal required")
		}
	case "budget_rule_update":
		var p BudgetRuleInput
		if e := strict(in.Parameters, &p); e != nil {
			return out, e
		}
		typed = p
		v, e := budget(tx, m.UserID, p.BudgetID)
		if e != nil {
			return out, e
		}
		if p.ExpectedDigest != entity.CanonicalJSONHash(marshal(v)) {
			return out, entity.Fail("STATE_CONFLICT", "budget digest changed")
		}
		found := false
		for _, r := range v.Rules {
			if r.ID == p.RuleID {
				found = true
			}
		}
		if !found {
			return out, entity.Fail("NOT_FOUND", "budget rule not found")
		}
		op.ResourceID = p.RuleID
		op.Snapshot = marshal(v)
		op.ResourceDigest = p.ExpectedDigest
	case "subscription_manage":
		var p ManageInput
		if e := strict(in.Parameters, &p); e != nil {
			return out, e
		}
		typed = p
		v, e := subscriptionRow(tx, m.UserID, p.SubscriptionID)
		if e != nil {
			return out, e
		}
		if v.Version != p.Input.ExpectedVersion {
			return out, entity.Fail("STATE_CONFLICT", "subscription changed")
		}
		op.ResourceID = v.ID
		op.SourceAccountID = v.BankAccountID
		op.AmountMinor = v.AmountMinor
		op.Currency = v.Currency
		op.Snapshot = marshal(v)
	}
	if _, e := entity.Money(op.AmountMinor, op.Currency); e != nil {
		return out, e
	}
	if op.SourceAccountID > 0 {
		if _, e := account(tx, m.UserID, op.SourceAccountID); e != nil {
			return out, e
		}
	}
	if op.TargetAccountID > 0 && in.Kind != "aa_payment" && in.Kind != "recipient_transfer" {
		if _, e := account(tx, m.UserID, op.TargetAccountID); e != nil {
			return out, e
		}
	}
	op.Payload = marshal(typed)
	op.IntentDigest = entity.OperationDigest(&op)
	if e := tx.Create(&op).Error; e != nil {
		return out, e
	}
	pc := risk.WithPermit(ctx, tx, op.ID, "", "prepare")
	core := service.New(repository.New(tx), risk.TrustedGateway{})
	sub := subscription.New(repository.New(tx), risk.TrustedGateway{}, s.simulator)
	switch p := typed.(type) {
	case service.TransferInput:
		v, e := core.CreateTransfer(pc, m, "gateway:"+op.ID, p)
		if e != nil {
			return out, e
		}
		op.ResourceID = v.ID
		op.ResourceDigest = v.ApprovalHash
		op.Snapshot = marshal(v)
	case SavingsInput:
		v, e := core.GoalTransfer(pc, m, p.GoalID, "gateway:"+op.ID, p.Input, in.Kind == "savings_withdrawal")
		if e != nil {
			return out, e
		}
		op.ResourceID = v.ID
		op.ResourceDigest = v.ApprovalHash
		op.Snapshot = marshal(v)
	case subscription.IntentInput:
		v, e := sub.CreateIntent(pc, m, "gateway:"+op.ID, p)
		if e != nil {
			return out, e
		}
		op.ResourceID = v.ID
		op.ResourceDigest = v.ConfirmationHash
	}
	op.IntentDigest = entity.OperationDigest(&op)
	if e := audit(tx, m, op.Agent, &op, op.Status, "PREPARE", ""); e != nil {
		return out, e
	}
	if e := tx.Save(&op).Error; e != nil {
		return out, e
	}
	return view(tx, op)
}
func (s *Service) PrepareSubscription(ctx context.Context, a identity.Actor, m service.Meta, key string, in subscription.IntentInput) (entity.SubscriptionActionIntent, error) {
	v, e := s.Prepare(ctx, a, m, key, PrepareInput{Kind: "cancel_subscription", Parameters: marshal(in)})
	var out entity.SubscriptionActionIntent
	if e != nil {
		return out, e
	}
	e = s.db.WithContext(ctx).Where("id = ? AND user_id = ?", v.Operation.ResourceID, m.UserID).First(&out).Error
	return out, e
}
