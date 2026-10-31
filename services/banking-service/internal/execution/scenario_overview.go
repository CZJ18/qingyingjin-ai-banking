package execution

import (
	"context"
	"github.com/qingyingjin/banking-service/internal/birthdays"
	"github.com/qingyingjin/banking-service/internal/cards"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/recipients"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/scenariooverview"
	"github.com/qingyingjin/banking-service/internal/service"
	"gorm.io/gorm"
	"sort"
	"strconv"
)

// ScenarioOverview only reads existing records. It creates neither intents,
// grants, reservations, approvals, audits nor fee bindings. Existing user-facing
// directory/card/scenario permissions remain unchanged.
func (s *Service) ScenarioOverview(ctx context.Context, a identity.Actor, m service.Meta, in scenariooverview.Input) (out scenariooverview.Output, err error) {
	if err = in.Validate(); err != nil {
		return out, err
	}
	if a.Kind != "user" && (a.Kind != "agent" || a.AgentType != "supervisor" || a.TaskID == "" || a.TaskID != m.TaskID) {
		return out, entity.Fail("FORBIDDEN", "signed task-bound Supervisor or user required")
	}
	if err = permission(a, m, "read"); err != nil {
		return out, err
	}
	if !s.Configured() {
		return out, entity.Fail("DEPENDENCY_UNAVAILABLE", "scenario read repository unavailable")
	}
	id, _ := scenariooverview.ID(in.AccountID)
	out = scenariooverview.Output{Scenario: in.Scenario, AccountID: in.AccountID, UserID: strconv.FormatInt(m.UserID, 10), IsSimulated: true, Items: []scenariooverview.Item{}, Limitations: []string{"仅限所选隔离模拟账户；读取结果不代表真实银行、商户或供应商授权。", "规划建议不能直接执行；仍须用户准备、独立授权及执行。"}}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var owner struct {
			ID     int64
			Status string
		}
		if e := tx.Model(&entity.User{}).Select("id,status").Where("id=? AND status=?", m.UserID, "active").Take(&owner).Error; e != nil {
			return repository.DBError(e)
		}
		var account entity.BankAccount
		if e := tx.Select("id,user_id,status,currency,is_simulated,balance_minor").Where("id=? AND user_id=?", id, m.UserID).Take(&account).Error; e != nil {
			return repository.DBError(e)
		}
		if !account.IsSimulated || account.Currency != "CNY" {
			return entity.Fail("FORBIDDEN", "selected isolated simulated CNY account required")
		}
		switch in.Scenario {
		case "transfer":
			balance := account.BalanceMinor
			out.Items = append(out.Items, scenariooverview.Item{ID: in.AccountID, Kind: "source_account", Status: account.Status, Currency: "CNY", AmountMinor: &balance})
			settings, e := recipients.New(tx).Settings(ctx, m.UserID)
			if e != nil {
				return e
			}
			ids := map[string]bool{}
			for _, value := range settings.Remarks {
				if value != out.UserID {
					ids[value] = true
				}
			}
			ordered := make([]string, 0, len(ids))
			for value := range ids {
				ordered = append(ordered, value)
			}
			sort.Strings(ordered)
			for _, value := range ordered {
				out.Items = append(out.Items, scenariooverview.Item{ID: value, Kind: "saved_recipient_reference", Status: "requires_explicit_selection"})
			}
			out.Limitations = append(out.Limitations, "收款人仅为操作者此前保存的引用；不含姓名、手机号或当前目录权限。必须在既有收款人页面明确查找、选择并复核。", "当前余额仅为模拟账本余额，不是银行余额；不推断转账金额或收款人。")
		case "cards":
			rows, e := cards.Read(tx, m.UserID)
			if e != nil {
				return e
			}
			for _, v := range rows {
				if v.AccountID != id {
					continue
				}
				limit, available := v.CreditLimitMinor, v.CreditAvailableMinor
				out.Items = append(out.Items, scenariooverview.Item{ID: v.ID, Kind: "card", Status: v.Status, Currency: v.Currency, CreditLimitMinor: &limit, CreditAvailableMinor: &available, TransactionLimitMinor: v.TransactionLimitMinor})
			}
			out.Limitations = append(out.Limitations, "仅返回所选账户的模拟卡片状态与限额；不返回姓名、卡号或验证码。信用额度不是可用现金。")
		case "investment":
			for _, v := range investmentProducts() {
				minimum, lock := v.MinimumMinor, v.LockDays
				out.Items = append(out.Items, scenariooverview.Item{ID: v.ID, Kind: "investment_product", Status: "catalog_only", Label: v.Name, Currency: "CNY", MinimumMinor: &minimum, RiskLevel: v.RiskLevel, LockDays: &lock})
			}
			out.Limitations = append(out.Limitations, "目录是明确模拟的教学产品，不含真实净值、收益预测或投资推荐；风险问卷必须由本人填写。")
		case "subscriptions":
			var rows []entity.Subscription
			if e := tx.Select("id,user_id,bank_account_id,status,amount_minor,currency,version,billing_interval,next_billing_at").Where("user_id=? AND bank_account_id=?", m.UserID, id).Order("id").Limit(51).Find(&rows).Error; e != nil {
				return e
			}
			if len(rows) > 50 {
				out.Truncated = true
				rows = rows[:50]
			}
			for _, v := range rows {
				amount := v.AmountMinor
				out.Items = append(out.Items, scenariooverview.Item{ID: strconv.FormatInt(v.ID, 10), Kind: "subscription", Status: v.Status, AmountMinor: &amount, Currency: v.Currency, Version: v.Version, BillingInterval: v.BillingInterval, NextBillingAt: v.NextBillingAt})
			}
			out.Limitations = append(out.Limitations, "订阅识别是既有记录中的候选模式，需人工核实商户合约；本地取消不等于真实商户取消。")
		case "birthday":
			view, e := birthdays.New(tx).Read(ctx, m.UserID)
			if e != nil {
				return e
			}
			upcoming := map[string]birthdays.Upcoming{}
			for _, v := range view.Upcoming {
				upcoming[v.ID] = v
			}
			for _, v := range view.Entries {
				status := "recorded"
				if !view.Enabled {
					status = "disabled"
				}
				item := scenariooverview.Item{ID: v.ID, Kind: "registered_birthday", Status: status, MonthDay: v.MonthDay, Version: view.Version}
				if next, ok := upcoming[v.ID]; ok {
					days := next.DaysUntil
					item.NextDate = next.NextDate
					item.DaysUntil = &days
				}
				out.Items = append(out.Items, item)
			}
			out.Limitations = append(out.Limitations, "仅使用本人明确登记的生日日期，不读取通讯录、不推断亲属或伴侣身份；姓名留在本机页面复核。生日信息属于用户日历，并非所选账户交易。")
		}
		if len(out.Items) > 50 {
			out.Truncated = true
			out.Items = out.Items[:50]
		}
		if out.Truncated {
			out.Limitations = append(out.Limitations, "结果已截断为前五十项；不代表全部记录。")
		}
		return out.ValidateFor(in, m.UserID)
	})
	return out, repository.DBError(err)
}
