package goal

import (
	"encoding/json"
	"testing"
)

func number(v int64) *int64 { return &v }
func fixture() Parsed {
	return Parsed{GoalType: "healthy_lifestyle", PeriodMonths: number(3), SavingsMonthlyMinor: number(20000), Currency: "CNY", ProhibitedOperations: []string{}, Constraints: []Constraint{}, MissingInformation: []string{}, Evidence: map[string]string{"period_months": "三个月", "savings_monthly_minor": "每月储蓄200元"}}
}
func TestCompiler(t *testing.T) {
	for _, tc := range []struct {
		name                        string
		income, spending, essential *int64
		status                      string
	}{
		{"missing budget", nil, nil, nil, "awaiting_clarification"},
		{"feasible", number(500000), number(400000), number(200000), "pending_confirmation"},
		{"conflict", number(410000), number(400000), number(200000), "conflict"},
		{"essential reserve", number(410000), number(100000), number(420000), "conflict"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Compile(fixture(), FinancialContext{MonthlyIncomeMinor: tc.income, SpendingBudgetMinor: tc.spending, EssentialExpensesMinor: tc.essential}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if p.Status != tc.status {
				t.Fatalf("%s: %+v", p.Status, p)
			}
			if p.FinancialExecutionEnabled {
				t.Fatal("execution enabled")
			}
			if err = ValidatePlan(p); err != nil {
				t.Fatal(err)
			}
			if tc.status == "conflict" && len(p.Conflicts) == 0 {
				t.Fatal("no conflicts")
			}
			for _, task := range p.Tasks {
				if task.ExecutionMode == "execute" {
					t.Fatal("execution task")
				}
			}
		})
	}
}
func TestInvalidFinancialTasks(t *testing.T) {
	p, _ := Compile(fixture(), FinancialContext{}, nil)
	p.Tasks[0].TaskType = "execute_transfer"
	if ValidatePlan(p) == nil {
		t.Fatal("allowed transfer")
	}
	p, _ = Compile(fixture(), FinancialContext{}, nil)
	p.Tasks[0].Dependencies = []string{p.Tasks[len(p.Tasks)-1].TaskID}
	if ValidatePlan(p) == nil {
		t.Fatal("allowed cycle")
	}
	p, _ = Compile(fixture(), FinancialContext{}, nil)
	p.Tasks[0].RequiredPermissions = []string{"funds:write"}
	if ValidatePlan(p) == nil {
		t.Fatal("allowed funds permission")
	}
	p, _ = Compile(fixture(), FinancialContext{}, nil)
	p.Tasks[0].Parameters["amount_minor"] = 20000
	if ValidatePlan(p) == nil {
		t.Fatal("arbitrary parameters")
	}
}
func TestModelSchema(t *testing.T) {
	raw, _ := json.Marshal(fixture())
	if _, err := DecodeParsed(raw, "未来三个月每月储蓄200元"); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{}`, `{"task_type":"transfer"}`, `null`, `[]`, `{"currency":"CNY","period_months":1.5}`} {
		if _, err := DecodeParsed([]byte(raw), "hello"); err == nil {
			t.Fatal("accepted", raw)
		}
	}
	p := fixture()
	p.SavingsMonthlyMinor = number(30000)
	raw, _ = json.Marshal(p)
	if _, err := DecodeParsed(raw, "未来三个月每月储蓄200元"); err == nil {
		t.Fatal("hallucinated amount")
	}
	p = fixture()
	p.Evidence["period_months"] = "不存在的文本"
	raw, _ = json.Marshal(p)
	if _, err := DecodeParsed(raw, "三个月每月储蓄200元"); err == nil {
		t.Fatal("hallucinated evidence")
	}
	for _, text := range []string{"每月储蓄500.123元", "每月储蓄1,123元", "每月储蓄1e123元", "每月储蓄-123元"} {
		if amountIn(text, 12300) {
			t.Fatal("partial amount accepted", text)
		}
	}
	p = fixture()
	p.Evidence["period_months"] = "十三个月"
	raw, _ = json.Marshal(p)
	if _, err := DecodeParsed(raw, "十三个月每月储蓄200元"); err == nil {
		t.Fatal("partial Chinese period accepted")
	}
}

func TestTotalSavingsTarget(t *testing.T) {
	parsed := fixture()
	parsed.SavingsMonthlyMinor = nil
	parsed.SavingsTargetMinor = number(60001)
	plan, err := Compile(parsed, FinancialContext{MonthlyIncomeMinor: number(500000), SpendingBudgetMinor: number(400000), EssentialExpensesMinor: number(200000)}, nil)
	if err != nil || plan.Status != "pending_confirmation" {
		t.Fatal(plan, err)
	}
	for _, task := range plan.Tasks {
		if task.TaskType == "generate_savings_plan" {
			if *task.Parameters["monthly_minor"].(*int64) != 20001 || *task.Parameters["target_minor"].(*int64) != 60001 {
				t.Fatal("incorrect cents rounding")
			}
		}
	}
	if parsed.SavingsMonthlyMinor != nil {
		t.Fatal("changed user's original goal")
	}
	parsed.SavingsMonthlyMinor = number(20000)
	plan, err = Compile(parsed, FinancialContext{}, nil)
	if err != nil || plan.Status != "conflict" {
		t.Fatal(plan, err)
	}
}

func TestKnownContextResolvesModelClarifications(t *testing.T) {
	parsed := fixture()
	parsed.MissingInformation = []string{"monthly_income_minor", "spending_budget_minor", "essential_expenses_minor", "savings_target_minor"}
	plan, err := Compile(parsed, FinancialContext{MonthlyIncomeMinor: number(500000), SpendingBudgetMinor: number(400000), EssentialExpensesMinor: number(200000)}, nil)
	if err != nil || plan.Status != "pending_confirmation" || len(plan.MissingInformation) != 0 {
		t.Fatal("trusted context should resolve missing facts from text", plan, err)
	}
}
