// Package planning produces advice only. It has no funds executor.
package planning

import (
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/entity"
)

type Cashflow struct {
	Name        string `json:"name"`
	Kind        string `json:"kind"`
	AmountMinor int64  `json:"amount_minor"`
	DueAt       int64  `json:"due_at"`
}
type Settings struct {
	Period                  string     `json:"period,omitempty"`
	AccountIDs              []int64    `json:"account_ids"`
	SavingsGoalIDs          []int64    `json:"savings_goal_ids"`
	LifeGoalIDs             []int64    `json:"life_goal_ids"`
	LivingRemainingMinor    *int64     `json:"living_remaining_minor,omitempty"`
	EssentialRemainingMinor *int64     `json:"essential_remaining_minor,omitempty"`
	SafetyReserveMinor      int64      `json:"safety_reserve_minor"`
	CashBufferMinor         int64      `json:"cash_buffer_minor"`
	BaselineIncomeMinor     int64      `json:"baseline_income_minor"`
	FutureIncomeMinor       *int64     `json:"future_income_minor,omitempty"`
	Cashflows               []Cashflow `json:"cashflows"`
}
type CreateInput struct {
	Name     string   `json:"name"`
	Settings Settings `json:"settings"`
}
type UpdateInput struct {
	ExpectedVersion int64    `json:"expected_version"`
	Settings        Settings `json:"settings"`
}
type IDInput struct {
	PlanID string `json:"plan_id"`
}
type DecisionInput struct {
	ExpectedVersion int64  `json:"expected_version"`
	DataDigest      string `json:"data_digest"`
	Option          string `json:"option"`
	ConfirmAdvice   bool   `json:"confirm_advice"`
}
type EventInput struct {
	Type       string `json:"type"`
	ResourceID string `json:"resource_id"`
	Reason     string `json:"reason"`
}
type GoalUpdateInput struct {
	ExpectedMonthlyMinor int64 `json:"expected_monthly_minor"`
	ExpectedEndsAt       int64 `json:"expected_ends_at"`
	ExpectedTargetMinor  int64 `json:"expected_target_minor"`
	TargetMinor          int64 `json:"target_minor"`
	MonthlyMinor         int64 `json:"monthly_minor"`
	EndsAt               int64 `json:"ends_at"`
}
type FinancialPlan struct {
	entity.Base
	Name           string          `json:"name"`
	CurrentVersion int64           `json:"current_version"`
	SettingsJSON   json.RawMessage `json:"settings" gorm:"column:settings_json"`
	Status         string          `json:"status"`
}

func (FinancialPlan) TableName() string { return "banking_financial_plans" }

type PlanVersion struct {
	entity.Base
	PlanID            int64           `json:"plan_id,string"`
	Version           int64           `json:"version"`
	PreviousVersion   int64           `json:"previous_version"`
	EventID           *int64          `json:"event_id,omitempty,string"`
	Reason            string          `json:"reason"`
	ImpactMinor       int64           `json:"impact_minor"`
	DataDigest        string          `json:"data_digest"`
	NeedsConfirmation bool            `json:"needs_confirmation"`
	SnapshotJSON      json.RawMessage `json:"snapshot" gorm:"column:snapshot_json"`
	ResultJSON        json.RawMessage `json:"result" gorm:"column:result_json"`
	TraceID           string          `json:"trace_id"`
	TaskID            string          `json:"task_id"`
	AuditID           string          `json:"audit_id"`
}

func (PlanVersion) TableName() string { return "banking_plan_versions" }

type PlanConstraint struct {
	entity.Base
	PlanVersionID int64  `json:"plan_version_id,string"`
	Kind          string `json:"kind"`
	Description   string `json:"description"`
	AmountMinor   int64  `json:"amount_minor"`
}

func (PlanConstraint) TableName() string { return "banking_plan_constraints" }

type ReplanEvent struct {
	entity.Base
	EventKey    string `json:"event_key"`
	RequestHash string `json:"-"`
	Type        string `json:"type"`
	ResourceID  string `json:"resource_id"`
	Reason      string `json:"reason"`
	Status      string `json:"status"`
	Attempts    int64  `json:"attempts"`
	RetryAt     int64  `json:"retry_at"`
	ErrorCode   string `json:"error_code"`
	TraceID     string `json:"trace_id"`
	TaskID      string `json:"task_id"`
}

func (ReplanEvent) TableName() string { return "banking_replan_events" }

type PlanDecision struct {
	entity.Base
	PlanID        int64  `json:"plan_id,string"`
	PlanVersionID int64  `json:"plan_version_id,string"`
	Option        string `json:"option"`
	DataDigest    string `json:"data_digest"`
	AuditID       string `json:"audit_id"`
}

func (PlanDecision) TableName() string { return "banking_plan_decisions" }

type View struct {
	Plan                      FinancialPlan    `json:"plan"`
	Version                   PlanVersion      `json:"version"`
	Result                    Result           `json:"result"`
	Constraints               []PlanConstraint `json:"constraints"`
	Decisions                 []PlanDecision   `json:"decisions"`
	FinancialExecutionEnabled bool             `json:"financial_execution_enabled"`
}
type GoalFunds struct {
	ID                       int64 `json:"id,string"`
	TargetMinor              int64 `json:"target_minor"`
	CurrentMinor             int64 `json:"current_minor"`
	MonthlyMinor             int64 `json:"monthly_minor"`
	DepositedThisPeriodMinor int64 `json:"deposited_this_period_minor"`
	Eligible                 bool  `json:"eligible"`
}
type Source struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	Version int64  `json:"version"`
}
type Snapshot struct {
	Period                 string      `json:"period"`
	Settings               Settings    `json:"settings"`
	BalanceMinor           int64       `json:"balance_minor"`
	LifeFloorMinor         int64       `json:"life_floor_minor"`
	EssentialFloorMinor    int64       `json:"essential_floor_minor"`
	SubscriptionDueMinor   int64       `json:"subscription_due_minor"`
	CancelledForecastMinor int64       `json:"cancelled_forecast_minor"`
	Goals                  []GoalFunds `json:"goals"`
	Sources                []Source    `json:"sources"`
	Blockers               []string    `json:"blockers"`
	ReviewNotes            []string    `json:"review_notes"`
}
type Allocation struct {
	GoalID      int64 `json:"goal_id,string"`
	AmountMinor int64 `json:"amount_minor"`
	UnmetMinor  int64 `json:"unmet_minor"`
	ProgressBPS int64 `json:"progress_bps"`
}
type Option struct {
	Feasible      bool         `json:"feasible"`
	Name          string       `json:"name"`
	SavingsMinor  int64        `json:"savings_minor"`
	RetainedMinor int64        `json:"retained_minor"`
	Allocations   []Allocation `json:"allocations"`
	Explanation   string       `json:"explanation"`
}
type Result struct {
	ReserveShortfallMinor      int64    `json:"reserve_shortfall_minor"`
	Currency                   string   `json:"currency"`
	Period                     string   `json:"period"`
	BalanceMinor               int64    `json:"balance_minor"`
	NecessaryReserveMinor      int64    `json:"necessary_reserve_minor"`
	LivingReserveMinor         int64    `json:"living_reserve_minor"`
	AdditionalObligationsMinor int64    `json:"additional_obligations_minor"`
	CashflowBufferMinor        int64    `json:"cashflow_buffer_minor"`
	SafetyReserveMinor         int64    `json:"safety_reserve_minor"`
	IncomeRiskReserveMinor     int64    `json:"income_risk_reserve_minor"`
	DisposableMinor            int64    `json:"disposable_minor"`
	SavingsCapMinor            int64    `json:"savings_cap_minor"`
	SuggestedSavingsMinor      int64    `json:"suggested_savings_minor"`
	TargetDemandMinor          int64    `json:"target_demand_minor"`
	FutureIncomeMinor          int64    `json:"future_income_minor"`
	FutureAvoidedSpendMinor    int64    `json:"future_avoided_spend_minor"`
	ConflictMinor              int64    `json:"conflict_minor"`
	Options                    []Option `json:"options"`
	Blockers                   []string `json:"blockers"`
	ReviewNotes                []string `json:"review_notes"`
	FinancialExecutionEnabled  bool     `json:"financial_execution_enabled"`
}
