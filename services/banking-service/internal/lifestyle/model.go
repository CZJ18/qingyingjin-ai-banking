// Package lifestyle produces suggestions only; it has no financial write API.
package lifestyle

import (
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/entity"
)

type Spec struct {
	SchemaVersion                string            `json:"schema_version"`
	Currency                     string            `json:"currency"`
	PeriodMonths                 int64             `json:"period_months"`
	ExpenseReductionMonthlyMinor int64             `json:"expense_reduction_monthly_minor"`
	SavingsMonthlyMinor          int64             `json:"savings_monthly_minor"`
	HealthBackground             string            `json:"health_background"`
	AdjustableCategories         []string          `json:"adjustable_categories"`
	ProtectedCategories          []string          `json:"protected_categories"`
	ProhibitedOperations         []string          `json:"prohibited_operations"`
	Evidence                     map[string]string `json:"evidence"`
}
type GoalInput struct {
	Text            string `json:"text"`
	Spec            Spec   `json:"spec"`
	ExpectedVersion int64  `json:"expected_version"`
}
type Goal struct {
	entity.Base
	CurrentVersion int64 `json:"current_version"`
}

func (Goal) TableName() string { return "banking_lifestyle_goals" }

type GoalVersion struct {
	entity.Base
	GoalID   int64           `json:"goal_id,string"`
	Version  int64           `json:"version"`
	Text     string          `json:"text"`
	SpecJSON json.RawMessage `json:"spec" gorm:"column:spec_json"`
	TraceID  string          `json:"trace_id"`
}

func (GoalVersion) TableName() string { return "banking_lifestyle_goal_versions" }

type GoalView struct {
	Goal     Goal          `json:"goal"`
	Versions []GoalVersion `json:"versions"`
}
type CompileInput struct {
	ExpectedVersion int64  `json:"expected_version"`
	BudgetID        int64  `json:"budget_id,string"`
	BillingReportID string `json:"billing_report_id"`
	FinancialPlanID int64  `json:"financial_plan_id,string"`
}
type CompileToolInput struct {
	GoalID int64 `json:"goal_id,string"`
	CompileInput
}
type Category struct {
	Name                  string `json:"category"`
	AllocatedMinor        int64  `json:"allocated_minor"`
	EssentialReserveMinor int64  `json:"essential_reserve_minor"`
	ObservedMinor         int64  `json:"observed_minor"`
}
type Snapshot struct {
	Goal                   Spec         `json:"goal"`
	BudgetMinor            int64        `json:"budget_minor"`
	Categories             []Category   `json:"categories"`
	CompleteMonth          bool         `json:"complete_month"`
	SavingsCeilingMinor    *int64       `json:"savings_ceiling_minor"`
	EvidenceTransactionIDs []string     `json:"evidence_transaction_ids"`
	Sources                CompileInput `json:"sources"`
	SourceDigest           string       `json:"source_digest"`
}
type Allocation struct {
	HistoricalMinor int64  `json:"historical_minor"`
	Category        string `json:"category"`
	OriginalMinor   int64  `json:"original_minor"`
	ProposedMinor   int64  `json:"proposed_minor"`
	Protected       bool   `json:"protected"`
}
type Option struct {
	Name                  string       `json:"name"`
	BudgetMinor           int64        `json:"budget_minor"`
	ExpenseReductionMinor int64        `json:"expense_reduction_minor"`
	SavingsMonthlyMinor   int64        `json:"savings_monthly_minor"`
	Categories            []Allocation `json:"categories"`
	Explanation           string       `json:"explanation"`
}
type Result struct {
	SchemaVersion             string   `json:"schema_version"`
	RuleVersion               string   `json:"rule_version"`
	Status                    string   `json:"status"`
	Options                   []Option `json:"options"`
	MissingInformation        []string `json:"missing_information"`
	Conflicts                 []string `json:"conflicts"`
	ReductionGapMinor         int64    `json:"reduction_gap_minor"`
	SavingsGapMinor           int64    `json:"savings_gap_minor"`
	RequiresConfirmation      bool     `json:"requires_confirmation"`
	FinancialExecutionEnabled bool     `json:"financial_execution_enabled"`
}
type Plan struct {
	entity.Base
	GoalID       int64           `json:"goal_id,string"`
	GoalVersion  int64           `json:"goal_version"`
	InputDigest  string          `json:"input_digest"`
	SnapshotJSON json.RawMessage `json:"snapshot" gorm:"column:snapshot_json"`
	ResultJSON   json.RawMessage `json:"result" gorm:"column:result_json"`
	ExpiresAt    int64           `json:"expires_at"`
	AuditID      string          `json:"audit_id"`
	TraceID      string          `json:"trace_id"`
}

func (Plan) TableName() string { return "banking_lifestyle_plans" }

type Recommendation struct {
	entity.Base
	GoalID              int64           `json:"goal_id,string"`
	PlanID              int64           `json:"plan_id,string"`
	Type                string          `json:"type"`
	ExpectedImpactMinor int64           `json:"expected_impact_minor"`
	ImpactPeriod        string          `json:"impact_period"`
	Explanation         string          `json:"explanation"`
	Confidence          string          `json:"confidence"`
	Status              string          `json:"status"`
	Version             int64           `json:"version"`
	ExpiresAt           int64           `json:"expires_at"`
	RuleVersion         string          `json:"rule_version"`
	OptionJSON          json.RawMessage `json:"option" gorm:"column:option_json"`
}

func (Recommendation) TableName() string { return "banking_lifestyle_recommendations" }

type PlanView struct {
	Plan            Plan             `json:"plan"`
	Recommendations []Recommendation `json:"recommendations"`
}
type DecisionInput struct {
	ExpectedVersion int64  `json:"expected_version"`
	Decision        string `json:"decision"`
	PlanDigest      string `json:"plan_digest"`
}
type Decision struct {
	entity.Base
	RecommendationID int64  `json:"recommendation_id,string"`
	Decision         string `json:"decision"`
	RequestKey       string `json:"-"`
	InputDigest      string `json:"-"`
	AuditID          string `json:"audit_id"`
	TraceID          string `json:"trace_id"`
}

func (Decision) TableName() string { return "banking_lifestyle_decisions" }

type IDInput struct {
	GoalID int64 `json:"goal_id,string"`
	PlanID int64 `json:"plan_id,string"`
}
