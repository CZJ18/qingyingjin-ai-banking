package goal

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"reflect"
)

var taskSpecs = map[string]struct{ mode, permission string }{
	"query_accounts": {"read_only", "accounts:read"}, "analyze_bills": {"analysis", "transactions:read"}, "check_subscriptions": {"analysis", "subscriptions:read"}, "calculate_budget": {"analysis", "budgets:read"}, "generate_savings_plan": {"analysis", "savings:plan"}, "propose_financial_operation": {"proposal", "operations:propose"},
}

func conflict(code, reason string, available *int64) Conflict {
	return Conflict{Code: code, Reason: reason, RequiresConfirmation: true, Alternatives: []Alternative{{Description: "保留必要生活支出，降低月度储蓄额；须更新目标后重新编译", MonthlySavingsMinor: available, RequiresConfirmation: true}, {Description: "延长周期或暂缓储蓄，补充可靠收入信息；须用户确认", RequiresConfirmation: true}}}
}
func Compile(parsed Parsed, fc FinancialContext, budget json.RawMessage) (Plan, error) {
	p := Plan{SchemaVersion: "1.0", Status: "pending_confirmation", Tasks: []Task{}, MissingInformation: []string{}, Conflicts: []Conflict{}, Constraints: append([]Constraint{}, parsed.Constraints...), Confirmations: []string{"此计划尚未授权任何操作；金融操作必须经模块8独立确认与风控"}, FinancialContext: fc, BudgetSnapshot: budget}
	p.Constraints = append(p.Constraints, Constraint{Kind: "safety", Description: "正常饮食、住房、医疗等必要生活支出不得为储蓄自动削减", Evidence: "server_policy"}, Constraint{Kind: "safety", Description: "不根据体重变化或限制饮食实施金融奖惩；编译阶段禁止资金执行", Evidence: "server_policy"})
	merge := func(key string, dest **int64, source *int64) {
		if *dest == nil {
			*dest = source
		} else if source != nil && **dest != *source {
			p.Conflicts = append(p.Conflicts, conflict("CONTEXT_CONFLICT", key+" 与用户已确认信息不一致", nil))
		}
	}
	merge("monthly_income_minor", &p.FinancialContext.MonthlyIncomeMinor, parsed.MonthlyIncomeMinor)
	merge("spending_budget_minor", &p.FinancialContext.SpendingBudgetMinor, parsed.SpendingBudgetMinor)
	merge("essential_expenses_minor", &p.FinancialContext.EssentialExpensesMinor, parsed.EssentialExpensesMinor)
	if len(budget) > 0 {
		var usage struct {
			Budget struct {
				TotalMinor int64 `json:"total_minor"`
				Categories []struct {
					EssentialReserveMinor int64 `json:"essential_reserve_minor"`
				} `json:"categories"`
			} `json:"budget"`
		}
		if json.Unmarshal(budget, &usage) != nil {
			return p, entity.Fail("INVALID_TASK_PLAN", "invalid budget snapshot")
		}
		reserve := int64(0)
		for _, c := range usage.Budget.Categories {
			reserve += c.EssentialReserveMinor
		}
		if p.FinancialContext.EssentialExpensesMinor != nil && *p.FinancialContext.EssentialExpensesMinor < reserve {
			p.Conflicts = append(p.Conflicts, conflict("ESSENTIAL_RESERVE_CONFLICT", "目标上下文小于已有预算必要支出预留；必须明确重新确认，编译器不会削减预留", nil))
		}
		if p.FinancialContext.SpendingBudgetMinor != nil && *p.FinancialContext.SpendingBudgetMinor != usage.Budget.TotalMinor {
			p.Conflicts = append(p.Conflicts, conflict("BUDGET_CONTEXT_CONFLICT", "目标消费预算与选定持久化预算不一致；须用户先确认预算变更", nil))
		}
	}
	addMissing := func(k string) {
		for _, v := range p.MissingInformation {
			if v == k {
				return
			}
		}
		p.MissingInformation = append(p.MissingInformation, k)
	}
	if parsed.GoalType == "unclear" || parsed.GoalType == "other" {
		addMissing("明确健康生活或储蓄目标类型")
	}
	if parsed.PeriodMonths == nil {
		addMissing("period_months")
	}
	monthly, target := parsed.SavingsMonthlyMinor, parsed.SavingsTargetMinor
	if target != nil && (*target < 0 || *target > 1_000_000_000_000) {
		return p, entity.Fail("MODEL_OUTPUT_INVALID", "target out of range")
	}
	if monthly == nil && target != nil && parsed.PeriodMonths != nil && *parsed.PeriodMonths >= 1 && *parsed.PeriodMonths <= 120 {
		v := (*target + *parsed.PeriodMonths - 1) / *parsed.PeriodMonths
		monthly = &v
		p.Confirmations = append(p.Confirmations, "月储蓄建议由总目标/周期向上取整到分计算，须明确确认；原始解析目标不变")
	}
	if monthly != nil && parsed.PeriodMonths != nil && *monthly >= 0 && *monthly <= 1_000_000_000_000 && *parsed.PeriodMonths >= 1 && *parsed.PeriodMonths <= 120 {
		v := *monthly * *parsed.PeriodMonths
		if v > 1_000_000_000_000 {
			p.Conflicts = append(p.Conflicts, conflict("GOAL_AMOUNT_LIMIT", "周期储蓄总额超过模拟金融金额上限；请分阶段规划", nil))
		} else if target == nil {
			target = &v
		}
		if parsed.SavingsMonthlyMinor != nil && target != nil && v != *target {
			p.Conflicts = append(p.Conflicts, conflict("SAVINGS_TARGET_CONFLICT", "明确的月储蓄、周期和总目标金额不一致；须重新确认", nil))
		}
	}
	if monthly == nil {
		addMissing("savings_monthly_minor")
	}
	resolved := map[string]bool{"period_months": parsed.PeriodMonths != nil, "savings_monthly_minor": monthly != nil, "savings_target_minor": target != nil, "monthly_income_minor": p.FinancialContext.MonthlyIncomeMinor != nil, "spending_budget_minor": p.FinancialContext.SpendingBudgetMinor != nil, "essential_expenses_minor": p.FinancialContext.EssentialExpensesMinor != nil, "goal_type": parsed.GoalType == "healthy_lifestyle" || parsed.GoalType == "savings"}
	for _, v := range parsed.MissingInformation {
		if !resolved[v] {
			addMissing(v)
		}
	}
	for key, v := range map[string]*int64{"monthly_income_minor": p.FinancialContext.MonthlyIncomeMinor, "spending_budget_minor": p.FinancialContext.SpendingBudgetMinor, "essential_expenses_minor": p.FinancialContext.EssentialExpensesMinor} {
		if v == nil {
			addMissing(key)
		} else if *v < 0 || *v > 1_000_000_000_000 {
			return p, entity.Fail("INVALID_ARGUMENT", "financial context out of range")
		}
	}
	if parsed.PeriodMonths != nil && (*parsed.PeriodMonths < 1 || *parsed.PeriodMonths > 120) {
		return p, entity.Fail("MODEL_OUTPUT_INVALID", "period out of range")
	}
	if parsed.SavingsMonthlyMinor != nil && (*parsed.SavingsMonthlyMinor < 0 || *parsed.SavingsMonthlyMinor > 1_000_000_000_000) {
		return p, entity.Fail("MODEL_OUTPUT_INVALID", "savings out of range")
	}
	f := p.FinancialContext
	if f.MonthlyIncomeMinor != nil && f.SpendingBudgetMinor != nil && f.EssentialExpensesMinor != nil {
		expense := *f.SpendingBudgetMinor
		if *f.EssentialExpensesMinor > expense {
			expense = *f.EssentialExpensesMinor
			p.Conflicts = append(p.Conflicts, conflict("ESSENTIAL_RESERVE_CONFLICT", "消费预算小于必要生活开支；不得削减必要开支", nil))
		}
		available := *f.MonthlyIncomeMinor - expense
		if available < 0 {
			available = 0
		}
		if *f.MonthlyIncomeMinor < expense || (monthly != nil && *monthly > available) {
			p.Conflicts = append(p.Conflicts, conflict("INSUFFICIENT_MONTHLY_CAPACITY", "可靠月收入扣除完整生活预算后不足以满足储蓄目标", &available))
		}
	}
	for _, c := range parsed.Constraints {
		if c.Description == "拒绝根据体重变化、限制饮食生成金融奖惩" {
			p.Conflicts = append(p.Conflicts, conflict("UNSAFE_HEALTH_INCENTIVE", "不能编译体重或限制饮食关联的金融奖惩", nil))
		}
	}
	if len(p.MissingInformation) > 0 {
		p.Status = "awaiting_clarification"
	}
	if len(p.Conflicts) > 0 {
		p.Status = "conflict"
	}
	for _, kind := range []string{"query_accounts", "analyze_bills", "check_subscriptions", "calculate_budget", "generate_savings_plan", "propose_financial_operation"} {
		if kind == "propose_financial_operation" {
			forbidden := false
			for _, op := range parsed.ProhibitedOperations {
				if op == "transfer" || op == "all_financial_operations" {
					forbidden = true
				}
			}
			if forbidden {
				p.Confirmations = append(p.Confirmations, "用户禁止金融操作，未生成金融操作建议任务")
				continue
			}
		}
		params := map[string]any{}
		if kind == "generate_savings_plan" {
			params = map[string]any{"monthly_minor": monthly, "target_minor": target, "period_months": parsed.PeriodMonths, "currency": "CNY"}
		}
		if kind == "propose_financial_operation" {
			params["requires_confirmation"] = true
		}
		deps := []string{}
		if len(p.Tasks) > 0 {
			deps = append(deps, p.Tasks[len(p.Tasks)-1].TaskID)
		}
		state := "planned"
		if p.Status != "pending_confirmation" && (kind == "calculate_budget" || kind == "generate_savings_plan" || kind == "propose_financial_operation") {
			state = "blocked"
		}
		spec := taskSpecs[kind]
		p.Tasks = append(p.Tasks, Task{TaskID: uuid.NewString(), TaskType: kind, Dependencies: deps, Parameters: params, RequiredPermissions: []string{spec.permission}, ExecutionMode: spec.mode, Status: state})
	}
	return p, ValidatePlan(p)
}
func ValidatePlan(p Plan) error {
	raw, e := json.Marshal(p)
	if e != nil {
		return entity.Fail("INVALID_TASK_PLAN", "plan is not serializable")
	}
	if e = schemaValidate("task-plan", raw); e != nil {
		return entity.Fail("INVALID_TASK_PLAN", "task plan schema failed")
	}
	seen := map[string]bool{}
	for _, t := range p.Tasks {
		spec, ok := taskSpecs[t.TaskType]
		if !ok || seen[t.TaskID] || t.ExecutionMode != spec.mode || !reflect.DeepEqual(t.RequiredPermissions, []string{spec.permission}) {
			return entity.Fail("INVALID_TASK_PLAN", "invalid task type, ID, mode or permissions")
		}
		for _, dep := range t.Dependencies {
			if !seen[dep] {
				return entity.Fail("INVALID_TASK_PLAN", "dependencies must refer to earlier tasks")
			}
		}
		seen[t.TaskID] = true
		size := 0
		switch t.TaskType {
		case "generate_savings_plan":
			size = 4
			for _, k := range []string{"monthly_minor", "target_minor", "period_months", "currency"} {
				if _, ok := t.Parameters[k]; !ok {
					return entity.Fail("INVALID_TASK_PLAN", "invalid savings parameters")
				}
			}
			if t.Parameters["currency"] != "CNY" {
				return entity.Fail("INVALID_TASK_PLAN", "currency must be CNY")
			}
		case "propose_financial_operation":
			size = 1
			if t.Parameters["requires_confirmation"] != true {
				return entity.Fail("INVALID_TASK_PLAN", "confirmation required")
			}
		}
		if len(t.Parameters) != size {
			return entity.Fail("INVALID_TASK_PLAN", "unexpected task parameters")
		}
		if p.Status != "pending_confirmation" && (t.ExecutionMode == "proposal" || t.TaskType == "generate_savings_plan" || t.TaskType == "calculate_budget") && t.Status != "blocked" {
			return entity.Fail("INVALID_TASK_PLAN", "unresolved plan must block dependent tasks")
		}
	}
	return nil
}
