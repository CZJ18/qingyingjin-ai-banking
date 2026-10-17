package execution

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/planning"
	"github.com/qingyingjin/banking-service/internal/service"
	"gorm.io/gorm"
	"strconv"
	"time"
)

func riskEvent(tx *gorm.DB, op *entity.OperationIntent, rule, decision, code string, evidence any) error {
	return tx.Create(&entity.RiskEvent{ID: uuid.NewString(), UserID: op.UserID, OperationID: op.ID, RuleName: rule, RiskLevel: op.RiskLevel, Decision: decision, ErrorCode: code, Evidence: marshal(evidence), TraceID: op.TraceID}).Error
}
func protected(tx *gorm.DB, user, accountID int64) (int64, error) {
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 1, 0)
	floor := int64(0)
	var budgets []entity.Budget
	if e := tx.Where("user_id = ? AND month = ? AND currency = ?", user, start.Format("2006-01"), "CNY").Find(&budgets).Error; e != nil {
		return 0, e
	}
	for _, b := range budgets {
		var cats []entity.BudgetCategory
		if e := tx.Where("user_id = ? AND budget_id = ?", user, b.ID).Find(&cats).Error; e != nil {
			return 0, e
		}
		var remaining int64
		for _, c := range cats {
			var spent int64
			e := tx.Model(&entity.Transaction{}).Select("COALESCE(SUM(amount_minor),0)").Where("user_id = ? AND direction = ? AND category = ? AND occurred_at >= ? AND occurred_at < ? AND kind <> ?", user, "expense", c.Category, start.UnixMilli(), end.UnixMilli(), "internal_transfer").Scan(&spent).Error
			if e != nil {
				return 0, e
			}
			n := c.EssentialReserveMinor - spent
			if n > 0 {
				if n > entity.MaxBalance-remaining {
					return 0, entity.Fail("RISK_REJECTED", "reserve overflow")
				}
				remaining += n
			}
		}
		if remaining > floor {
			floor = remaining
		}
	}
	var plans []planning.FinancialPlan
	if e := tx.Where("user_id = ? AND status = ?", user, "active").Find(&plans).Error; e != nil {
		return 0, e
	}
	for _, p := range plans {
		var settings planning.Settings
		if json.Unmarshal(p.SettingsJSON, &settings) != nil {
			return 0, entity.Fail("RISK_REJECTED", "invalid persisted cashflow settings")
		}
		applies := false
		for _, id := range settings.AccountIDs {
			if id == accountID {
				applies = true
			}
		}
		if !applies {
			continue
		}
		remaining, e := planning.ExecutionReserve(tx.Statement.Context, tx, user, settings, now)
		if e != nil {
			return 0, e
		}
		if remaining > floor {
			floor = remaining
		}
	}
	// Forecast savings and future income are never included in actual funds.
	return floor, nil
}
func evaluate(tx *gorm.DB, op *entity.OperationIntent) error {
	if op.IntentDigest == "" || op.IntentDigest != entity.OperationDigest(op) {
		return entity.Fail("AUTHORIZATION_MISMATCH", "stored intent changed")
	}
	for _, name := range []string{"simulation_only", "bound_authorization", "resource_ownership", "necessary_reserve", "amount_limit", "resource_version"} {
		var r entity.RiskRule
		if e := tx.Where("name = ?", name).First(&r).Error; e != nil {
			return entity.Fail("RISK_REJECTED", "required risk policy unavailable")
		}
		if !r.Enabled || r.Version != 1 {
			return entity.Fail("RISK_REJECTED", "required risk policy disabled or incompatible")
		}
		if name == "amount_limit" && (r.LimitMinor <= 0 || r.LimitMinor > entity.MaxBalance || op.AmountMinor > r.LimitMinor) {
			return entity.Fail("RISK_REJECTED", "operation amount exceeds deterministic limit")
		}
	}
	if _, e := entity.Money(op.AmountMinor, op.Currency); e != nil {
		return e
	}
	switch op.Kind {
	case "recipient_transfer":
		return recipientPolicy(tx, op)
	case "aa_payment":
		return aaPolicy(tx, op)
	case "card_manage":
		if e := cardPolicy(tx, op); e != nil {
			return e
		}
	case "transfer", "savings_deposit", "savings_withdrawal":
		source, e := account(tx, op.UserID, op.SourceAccountID)
		if e != nil {
			return e
		}
		target, e := account(tx, op.UserID, op.TargetAccountID)
		if e != nil {
			return e
		}
		if source.Status != "active" || target.Status != "active" {
			return entity.Fail("ACCOUNT_FROZEN", "source and destination must be active")
		}
		if source.Currency != op.Currency || target.Currency != op.Currency || source.ID == target.ID || op.AmountMinor <= 0 {
			return entity.Fail("INVALID_ARGUMENT", "invalid transfer amount or destination")
		}
		if source.BalanceMinor < op.AmountMinor {
			return entity.Fail("INSUFFICIENT_FUNDS", "available balance insufficient")
		}
		reserve, e := protected(tx, op.UserID, source.ID)
		if e != nil {
			return e
		}
		if source.BalanceMinor-op.AmountMinor < reserve {
			return entity.Fail("NECESSARY_RESERVE", "transfer would consume protected living or safety funds")
		}
		return riskEvent(tx, op, "necessary_reserve", "allow", "", map[string]any{"account_id": strconv.FormatInt(source.ID, 10), "balance_minor": source.BalanceMinor, "protected_minor": reserve, "amount_minor": op.AmountMinor})
	case "import_transaction":
		var in service.TransactionInput
		if e := strict(op.Payload, &in); e != nil {
			return e
		}
		a, e := account(tx, op.UserID, in.BankAccountID)
		if e != nil {
			return e
		}
		if a.Status != "active" {
			return entity.Fail("ACCOUNT_FROZEN", "account must be active")
		}
		if in.Direction == "expense" && in.EffectMode == "apply_simulated_effect" {
			if a.BalanceMinor < in.AmountMinor {
				return entity.Fail("INSUFFICIENT_FUNDS", "available balance insufficient")
			}
			// Reserve validation runs after provisional posting, inside a savepoint.
			// This counts the approved necessary expense toward its category,
			// while retaining unrelated categories and current planning constraints.
		}
	case "account_status":
		var in StatusInput
		if e := strict(op.Payload, &in); e != nil {
			return e
		}
		a, e := account(tx, op.UserID, in.AccountID)
		if e != nil {
			return e
		}
		if a.Version != in.ExpectedVersion {
			return entity.Fail("STATE_CONFLICT", "account changed since confirmation")
		}
	case "budget_rule_update":
		var in BudgetRuleInput
		if e := strict(op.Payload, &in); e != nil {
			return e
		}
		b, e := budget(tx, op.UserID, in.BudgetID)
		if e != nil {
			return e
		}
		if entity.CanonicalJSONHash(marshal(b)) != op.ResourceDigest {
			return entity.Fail("STATE_CONFLICT", "budget rules changed since confirmation")
		}
	case "cancel_subscription", "subscription_manage":
		var saved entity.Subscription
		if json.Unmarshal(op.Snapshot, &saved) != nil {
			return entity.Fail("RISK_REJECTED", "invalid subscription snapshot")
		}
		current, e := subscriptionRow(tx, op.UserID, saved.ID)
		if e != nil {
			return e
		}
		if current.Version != saved.Version || current.AmountMinor != saved.AmountMinor || current.BankAccountID != saved.BankAccountID || !current.IsSimulated {
			return entity.Fail("STATE_CONFLICT", "subscription changed since confirmation")
		}
	}
	return riskEvent(tx, op, "resource_version", "allow", "", map[string]any{"resource_id": strconv.FormatInt(op.ResourceID, 10)})
}

// postExpenseReserve validates real persisted data before committing any business
// writes. A rejection rolls back both the imported transaction and its journal.
func postExpenseReserve(tx *gorm.DB, op *entity.OperationIntent) error {
	if op.Kind != "import_transaction" {
		return nil
	}
	var in service.TransactionInput
	if err := strict(op.Payload, &in); err != nil {
		return err
	}
	if in.Direction != "expense" || in.EffectMode != "apply_simulated_effect" {
		return nil
	}
	a, err := account(tx, op.UserID, in.BankAccountID)
	if err != nil {
		return err
	}
	reserve, err := protected(tx, op.UserID, a.ID)
	if err != nil {
		return err
	}
	if a.BalanceMinor < reserve {
		return entity.Fail("NECESSARY_RESERVE", "expense would consume protected funds")
	}
	return riskEvent(tx, op, "necessary_reserve", "allow", "", map[string]any{"account_id": strconv.FormatInt(a.ID, 10), "balance_minor": a.BalanceMinor, "protected_minor": reserve, "amount_minor": in.AmountMinor})
}
