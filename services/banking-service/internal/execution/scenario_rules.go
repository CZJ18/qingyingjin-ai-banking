package execution

import (
	"github.com/qingyingjin/banking-service/internal/entity"
	"time"
)

type ScheduledTransferInput struct {
	SourceAccountID int64  `json:"source_account_id,string"`
	TargetAccountID int64  `json:"target_account_id,string"`
	AmountMinor     int64  `json:"amount_minor"`
	ExecuteAt       int64  `json:"execute_at"`
	Description     string `json:"description"`
}
type InvestmentAssessmentInput struct {
	Answers []int `json:"answers"`
}
type InvestmentOrderInput struct {
	Action       string `json:"action"`
	ProductID    string `json:"product_id"`
	AccountID    int64  `json:"account_id,string"`
	AmountMinor  int64  `json:"amount_minor"`
	AssessmentID string `json:"assessment_id"`
}
type OccasionPlanInput struct {
	Name         string   `json:"name"`
	Birthday     string   `json:"birthday"`
	OrderTime    string   `json:"order_time,omitempty"`
	AccountID    int64    `json:"account_id,string"`
	ReserveMinor int64    `json:"reserve_minor"`
	Items        []string `json:"items"`
}

// The calendar date remains exactly two days before the birthday in Shanghai.
// Omitted order_time preserves existing plans' 09:00 behavior.
func occasionDue(date, clock string) (birthday, due time.Time, err error) {
	loc, e := time.LoadLocation("Asia/Shanghai")
	if e != nil {
		return birthday, due, entity.Fail("DEPENDENCY_UNAVAILABLE", "Shanghai timezone unavailable")
	}
	birthday, e = time.ParseInLocation("2006-01-02", date, loc)
	if e != nil || birthday.Format("2006-01-02") != date {
		return birthday, due, entity.Fail("INVALID_ARGUMENT", "exact birthday date required")
	}
	if clock == "" {
		clock = "09:00"
	}
	parsed, e := time.Parse("15:04", clock)
	if e != nil || len(clock) != 5 || parsed.Format("15:04") != clock {
		return birthday, due, entity.Fail("INVALID_ARGUMENT", "order_time must be HH:MM in Shanghai time")
	}
	day := birthday.AddDate(0, 0, -2)
	due = time.Date(day.Year(), day.Month(), day.Day(), parsed.Hour(), parsed.Minute(), 0, 0, loc)
	return birthday, due, nil
}

type OccasionOrderInput struct {
	PlanID    string `json:"plan_id"`
	ExecuteAt int64  `json:"execute_at"`
}
type OccasionReleaseInput struct {
	PlanID    string `json:"plan_id"`
	AccountID int64  `json:"account_id,string"`
}
type InvestmentProduct struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	RiskLevel    int    `json:"risk_level"`
	MinimumMinor int64  `json:"minimum_minor"`
	LockDays     int    `json:"lock_days"`
	Description  string `json:"description"`
	IsSimulated  bool   `json:"is_simulated"`
}

func investmentProducts() []InvestmentProduct {
	return []InvestmentProduct{
		{"sim-cash-v1", "模拟现金管理", 1, 100, 0, "演示现金管理；按本金申赎，无模拟收益，不代表保本产品。", true},
		{"sim-bond-v1", "模拟债券组合", 2, 10000, 0, "演示中等风险产品适配；真实债券可能亏损，演示不生成净值或收益。", true},
		{"sim-equity-v1", "模拟权益组合", 3, 10000, 0, "演示高风险产品适配；真实权益波动较大，演示仅按本金记账。", true},
	}
}
func investmentProduct(id string) (InvestmentProduct, error) {
	for _, p := range investmentProducts() {
		if p.ID == id {
			return p, nil
		}
	}
	return InvestmentProduct{}, entity.Fail("INVALID_ARGUMENT", "unknown simulated investment product")
}
func assessRisk(answers []int) (int, error) {
	if len(answers) != 3 {
		return 0, entity.Fail("INVALID_ARGUMENT", "three explicit risk answers required")
	}
	level := 3
	for _, a := range answers {
		if a < 1 || a > 3 {
			return 0, entity.Fail("INVALID_ARGUMENT", "risk answers must be 1..3")
		}
		if a < level {
			level = a
		}
	}
	return level, nil
}
func validateSchedule(due int64, now time.Time) error {
	if due <= now.UnixMilli() || due > now.Add(30*24*time.Hour).UnixMilli() {
		return entity.Fail("INVALID_ARGUMENT", "explicit future execution within 30 days required")
	}
	return nil
}
func occasionCost(ids []string) (int64, error) {
	if len(ids) == 0 || len(ids) > 2 {
		return 0, entity.Fail("INVALID_ARGUMENT", "one or two approved simulated items required")
	}
	seen := map[string]bool{}
	var sum int64
	for _, id := range ids {
		if seen[id] || (id != "flowers" && id != "cake") {
			return 0, entity.Fail("INVALID_ARGUMENT", "distinct approved simulated catalog items required")
		}
		seen[id] = true
		sum += 20000
	}
	return sum, nil
}
func scheduledDue(op entity.OperationIntent) (int64, error) {
	switch op.Kind {
	case "scheduled_transfer":
		var p ScheduledTransferInput
		if e := strict(op.Payload, &p); e != nil {
			return 0, e
		}
		return p.ExecuteAt, nil
	case "occasion_order":
		var p OccasionOrderInput
		if e := strict(op.Payload, &p); e != nil {
			return 0, e
		}
		return p.ExecuteAt, nil
	}
	return 0, nil
}
func scenarioKind(kind string) bool {
	switch kind {
	case "scheduled_transfer", "investment_assessment", "investment_order", "occasion_plan", "occasion_order", "occasion_release":
		return true
	}
	return false
}
