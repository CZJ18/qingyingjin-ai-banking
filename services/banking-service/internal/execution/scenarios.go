package execution

import (
	"context"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/service"
	"gorm.io/gorm"
	"strconv"
	"strings"
	"time"
)

func scenarioAsset(tx *gorm.DB, user int64, key string) (entity.BankAccount, error) {
	var a entity.BankAccount
	e := tx.Where("user_id=? AND external_account_id=?", user, key).First(&a).Error
	if e == gorm.ErrRecordNotFound {
		return a, nil
	}
	if e == nil {
		kind := "investment"
		if strings.HasPrefix(key, "occasion:") {
			kind = "occasion"
		}
		if !a.IsSimulated || a.Currency != "CNY" || a.AccountType != kind {
			return a, entity.Fail("DEPENDENCY_UNAVAILABLE", "incompatible reserved asset account")
		}
	}
	return a, e
}
func newScenarioAsset(tx *gorm.DB, user int64, key, name, kind string) (entity.BankAccount, error) {
	a, e := scenarioAsset(tx, user, key)
	if e != nil || a.ID != 0 {
		return a, e
	}
	a = entity.BankAccount{Base: entity.Base{UserID: user}, ExternalAccountID: &key, DisplayName: name, AccountType: kind, MaskedAccountNumber: "SIM-****", Currency: "CNY", Status: "active", Version: 1, IsSimulated: true}
	e = tx.Create(&a).Error
	return a, e
}
func cashAccount(tx *gorm.DB, user, id int64) (entity.BankAccount, error) {
	a, e := account(tx, user, id)
	if e != nil {
		return a, e
	}
	if a.AccountType != "checking" || a.Status != "active" || a.Currency != "CNY" {
		return a, entity.Fail("INVALID_ARGUMENT", "active simulated checking account required")
	}
	return a, nil
}

// prepareScenario only reads and binds typed inputs. It never creates assets,
// funds, profiles, reservations, orders or delegated grants.
func prepareScenario(tx *gorm.DB, op *entity.OperationIntent, raw json.RawMessage, preparing bool) (any, error) {
	switch op.Kind {
	case "occasion_release":
		var p OccasionReleaseInput
		if e := strict(raw, &p); e != nil {
			return nil, e
		}
		if _, e := cashAccount(tx, op.UserID, p.AccountID); e != nil {
			return nil, e
		}
		var plan entity.OperationIntent
		if e := tx.Where("id=? AND user_id=? AND kind='occasion_plan' AND status='succeeded'", p.PlanID, op.UserID).First(&plan).Error; e != nil {
			return nil, entity.Fail("NOT_FOUND", "owned occasion required")
		}
		var orders []entity.OperationIntent
		if e := tx.Where("user_id=? AND kind='occasion_order' AND status IN ?", op.UserID, []string{"pending_authorization", "authorized", "executing"}).Find(&orders).Error; e != nil {
			return nil, e
		}
		for _, o := range orders {
			var in OccasionOrderInput
			if strict(o.Payload, &in) != nil {
				return nil, entity.Fail("DEPENDENCY_UNAVAILABLE", "invalid order")
			}
			if in.PlanID == p.PlanID {
				return nil, entity.Fail("STATE_CONFLICT", "cancel pending order before releasing reserved funds")
			}
		}
		asset, e := scenarioAsset(tx, op.UserID, "occasion:"+p.PlanID)
		if e != nil {
			return nil, e
		}
		if asset.ID == 0 || asset.BalanceMinor <= 0 {
			return nil, entity.Fail("INSUFFICIENT_FUNDS", "no remaining occasion reserve")
		}
		op.SourceAccountID, op.TargetAccountID, op.AmountMinor = asset.ID, p.AccountID, asset.BalanceMinor
		op.Snapshot = marshal(map[string]any{"plan_id": p.PlanID, "remaining_minor": asset.BalanceMinor, "is_simulated": true})
		return p, nil
	case "scheduled_transfer":
		var p ScheduledTransferInput
		if e := strict(raw, &p); e != nil {
			return nil, e
		}
		if p.AmountMinor <= 0 || p.AmountMinor > entity.MaxBalance || p.SourceAccountID == p.TargetAccountID || len(p.Description) > 128 {
			return nil, entity.Fail("INVALID_ARGUMENT", "distinct accounts and positive transfer amount required")
		}
		if preparing {
			if e := validateSchedule(p.ExecuteAt, time.Now()); e != nil {
				return nil, e
			}
			op.ExpiresAt = p.ExecuteAt + int64(15*time.Minute/time.Millisecond)
		}
		for _, id := range []int64{p.SourceAccountID, p.TargetAccountID} {
			if _, e := cashAccount(tx, op.UserID, id); e != nil {
				return nil, e
			}
		}
		op.SourceAccountID, op.TargetAccountID, op.AmountMinor = p.SourceAccountID, p.TargetAccountID, p.AmountMinor
		op.Snapshot = marshal(map[string]any{"execute_at": p.ExecuteAt, "authorization_until": op.ExpiresAt, "simulation": true, "requires_funds_at_execution": true})
		return p, nil
	case "investment_assessment":
		var p InvestmentAssessmentInput
		if e := strict(raw, &p); e != nil {
			return nil, e
		}
		level, e := assessRisk(p.Answers)
		if e != nil {
			return nil, e
		}
		op.Snapshot = marshal(map[string]any{"risk_level": level, "is_simulated": true, "scope": "educational_suitability_only"})
		return p, nil
	case "investment_order":
		var p InvestmentOrderInput
		if e := strict(raw, &p); e != nil {
			return nil, e
		}
		product, e := investmentProduct(p.ProductID)
		if e != nil {
			return nil, e
		}
		if p.AmountMinor <= 0 || p.AmountMinor > entity.MaxBalance || (p.Action != "buy" && p.Action != "redeem") {
			return nil, entity.Fail("INVALID_ARGUMENT", "positive buy/redeem principal required")
		}
		cash, e := cashAccount(tx, op.UserID, p.AccountID)
		if e != nil {
			return nil, e
		}
		asset, e := scenarioAsset(tx, op.UserID, "investment:"+p.ProductID)
		if e != nil {
			return nil, e
		}
		op.SourceAccountID, op.TargetAccountID = cash.ID, asset.ID
		if p.Action == "buy" {
			if p.AmountMinor < product.MinimumMinor {
				return nil, entity.Fail("INVALID_ARGUMENT", "below simulated product minimum")
			}
			var assessment entity.OperationIntent
			if e := tx.Where("id=? AND user_id=? AND kind='investment_assessment' AND status='succeeded'", p.AssessmentID, op.UserID).First(&assessment).Error; e != nil {
				return nil, entity.Fail("RISK_ASSESSMENT_REQUIRED", "owned completed assessment required")
			}
			var answers InvestmentAssessmentInput
			if e := strict(assessment.Payload, &answers); e != nil {
				return nil, e
			}
			level, e := assessRisk(answers.Answers)
			if e != nil || level < product.RiskLevel || time.Now().UnixMilli()-assessment.CreatedAt > int64(180*24*time.Hour/time.Millisecond) {
				return nil, entity.Fail("RISK_REJECTED", "assessment expired or product exceeds risk capacity")
			}
		} else {
			if asset.ID == 0 || asset.BalanceMinor < p.AmountMinor {
				return nil, entity.Fail("INSUFFICIENT_FUNDS", "owned simulated principal insufficient")
			}
			op.SourceAccountID, op.TargetAccountID = asset.ID, cash.ID
		}
		op.AmountMinor = p.AmountMinor
		op.Snapshot = marshal(map[string]any{"product": product, "assessment_id": p.AssessmentID, "price_basis": "simulation_principal_only", "profit_minor": 0})
		return p, nil
	case "occasion_plan":
		var p OccasionPlanInput
		if e := strict(raw, &p); e != nil {
			return nil, e
		}
		cost, e := occasionCost(p.Items)
		if e != nil {
			return nil, e
		}
		birthday, due, e := occasionDue(p.Birthday, p.OrderTime)
		if e != nil {
			return nil, e
		}
		now := time.Now().In(birthday.Location())
		today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
		if birthday.Before(today) || birthday.After(now.Add(366*24*time.Hour)) || strings.TrimSpace(p.Name) == "" || len(p.Name) > 96 || p.ReserveMinor < cost || p.ReserveMinor > entity.MaxBalance {
			return nil, entity.Fail("INVALID_ARGUMENT", "future birthday, name and funded approved catalog budget required")
		}
		if _, e := cashAccount(tx, op.UserID, p.AccountID); e != nil {
			return nil, e
		}
		op.SourceAccountID, op.AmountMinor = p.AccountID, p.ReserveMinor
		op.Snapshot = marshal(map[string]any{"birthday": p.Birthday, "items": p.Items, "catalog_cost_minor": cost, "order_due": due.UnixMilli(), "merchant_contacted": false})
		return p, nil
	case "occasion_order":
		var p OccasionOrderInput
		if e := strict(raw, &p); e != nil {
			return nil, e
		}
		var plan entity.OperationIntent
		if e := tx.Where("id=? AND user_id=? AND kind='occasion_plan' AND status='succeeded'", p.PlanID, op.UserID).First(&plan).Error; e != nil {
			return nil, entity.Fail("NOT_FOUND", "owned funded occasion plan required")
		}
		var config OccasionPlanInput
		if e := strict(plan.Payload, &config); e != nil {
			return nil, e
		}
		cost, e := occasionCost(config.Items)
		if e != nil {
			return nil, e
		}
		var snapshot struct {
			OrderDue int64 `json:"order_due"`
		}
		if json.Unmarshal(plan.Snapshot, &snapshot) != nil {
			return nil, entity.Fail("DEPENDENCY_UNAVAILABLE", "invalid occasion snapshot")
		}
		if p.ExecuteAt != 0 {
			if p.ExecuteAt != snapshot.OrderDue {
				return nil, entity.Fail("INVALID_ARGUMENT", "scheduled order must use approved birthday-minus-two-days time")
			}
			if preparing {
				if e := validateSchedule(p.ExecuteAt, time.Now()); e != nil {
					return nil, e
				}
				op.ExpiresAt = p.ExecuteAt + int64(15*time.Minute/time.Millisecond)
			}
		}
		var orders []entity.OperationIntent
		if e := tx.Where("user_id=? AND kind='occasion_order' AND status IN ?", op.UserID, []string{"pending_authorization", "authorized", "executing", "succeeded"}).Find(&orders).Error; e != nil {
			return nil, e
		}
		for _, o := range orders {
			var in OccasionOrderInput
			if strict(o.Payload, &in) != nil {
				return nil, entity.Fail("DEPENDENCY_UNAVAILABLE", "invalid saved occasion order")
			}
			if o.ID != op.ID && in.PlanID == p.PlanID {
				return nil, entity.Fail("STATE_CONFLICT", "occasion already has a live or completed order")
			}
		}
		asset, e := scenarioAsset(tx, op.UserID, "occasion:"+plan.ID)
		if e != nil {
			return nil, e
		}
		if asset.ID == 0 {
			return nil, entity.Fail("NOT_FOUND", "occasion reserve account missing")
		}
		op.SourceAccountID, op.AmountMinor = asset.ID, cost
		op.Snapshot = marshal(map[string]any{"plan_id": plan.ID, "items": config.Items, "catalog_cost_minor": cost, "merchant_contacted": false, "order_due": p.ExecuteAt})
		return p, nil
	}
	return nil, entity.Fail("INVALID_ARGUMENT", "unsupported scenario")
}

func scenarioPolicy(tx *gorm.DB, op *entity.OperationIntent) error {
	current := *op
	if _, e := prepareScenario(tx, &current, op.Payload, false); e != nil {
		return e
	}
	if current.SourceAccountID != op.SourceAccountID || op.TargetAccountID != 0 && current.TargetAccountID != op.TargetAccountID || current.AmountMinor != op.AmountMinor || entity.CanonicalJSONHash(current.Snapshot) != entity.CanonicalJSONHash(op.Snapshot) {
		return entity.Fail("AUTHORIZATION_MISMATCH", "scenario no longer matches exact authorized scope")
	}
	if op.AmountMinor == 0 {
		return nil
	}
	source, e := account(tx, op.UserID, op.SourceAccountID)
	if e != nil {
		return e
	}
	if source.Status != "active" || source.Currency != "CNY" || source.BalanceMinor < op.AmountMinor {
		return entity.Fail("INSUFFICIENT_FUNDS", "active simulated funds insufficient")
	}
	if source.AccountType == "checking" {
		floor, e := protected(tx, op.UserID, source.ID)
		if e != nil {
			return e
		}
		if source.BalanceMinor-op.AmountMinor < floor {
			return entity.Fail("NECESSARY_RESERVE", "scenario would consume protected funds")
		}
	}
	return nil
}

func performScenario(ctx context.Context, tx *gorm.DB, m service.Meta, op *entity.OperationIntent) (any, error) {
	switch op.Kind {
	case "occasion_release":
		var p OccasionReleaseInput
		if e := strict(op.Payload, &p); e != nil {
			return nil, e
		}
		source, e := account(tx, m.UserID, op.SourceAccountID)
		if e != nil {
			return nil, e
		}
		target, e := cashAccount(tx, m.UserID, p.AccountID)
		if e != nil {
			return nil, e
		}
		return repository.PostScenarioFunds(ctx, tx, op, p, &source, &target)
	case "investment_assessment":
		var p InvestmentAssessmentInput
		if e := strict(op.Payload, &p); e != nil {
			return nil, e
		}
		level, e := assessRisk(p.Answers)
		return map[string]any{"risk_level": level, "assessment_id": op.ID, "is_simulated": true, "formal_bank_assessment": false}, e
	case "scheduled_transfer":
		var p ScheduledTransferInput
		if e := strict(op.Payload, &p); e != nil {
			return nil, e
		}
		source, e := account(tx, m.UserID, op.SourceAccountID)
		if e != nil {
			return nil, e
		}
		target, e := account(tx, m.UserID, op.TargetAccountID)
		if e != nil {
			return nil, e
		}
		return repository.PostScenarioFunds(ctx, tx, op, p, &source, &target)
	case "investment_order":
		var p InvestmentOrderInput
		if e := strict(op.Payload, &p); e != nil {
			return nil, e
		}
		product, _ := investmentProduct(p.ProductID)
		asset, e := newScenarioAsset(tx, m.UserID, "investment:"+p.ProductID, product.Name+"持仓", "investment")
		if e != nil {
			return nil, e
		}
		cash, e := account(tx, m.UserID, p.AccountID)
		if e != nil {
			return nil, e
		}
		source, target := &cash, &asset
		if p.Action == "redeem" {
			source, target = &asset, &cash
		}
		posted, e := repository.PostScenarioFunds(ctx, tx, op, p, source, target)
		if e != nil {
			return nil, e
		}
		return map[string]any{"journal": posted, "product_id": p.ProductID, "principal_minor": asset.BalanceMinor, "profit_minor": 0, "is_simulated": true, "pricing": "principal_only_no_yield"}, nil
	case "occasion_plan":
		var p OccasionPlanInput
		if e := strict(op.Payload, &p); e != nil {
			return nil, e
		}
		asset, e := newScenarioAsset(tx, m.UserID, "occasion:"+op.ID, p.Name+"庆祝预留", "occasion")
		if e != nil {
			return nil, e
		}
		source, e := account(tx, m.UserID, p.AccountID)
		if e != nil {
			return nil, e
		}
		posted, e := repository.PostScenarioFunds(ctx, tx, op, p, &source, &asset)
		if e != nil {
			return nil, e
		}
		return map[string]any{"plan_id": op.ID, "reserve_account_id": strconv.FormatInt(asset.ID, 10), "reserved_minor": asset.BalanceMinor, "journal": posted, "merchant_contacted": false}, nil
	case "occasion_order":
		var p OccasionOrderInput
		if e := strict(op.Payload, &p); e != nil {
			return nil, e
		}
		source, e := account(tx, m.UserID, op.SourceAccountID)
		if e != nil {
			return nil, e
		}
		return repository.PostScenarioFunds(ctx, tx, op, p, &source, nil)
	}
	return nil, entity.Fail("INVALID_ARGUMENT", "unsupported scenario")
}
