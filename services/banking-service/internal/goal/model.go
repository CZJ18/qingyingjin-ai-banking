// Package goal compiles plans only. It has no Banking execution dependency.
package goal

import (
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/entity"
)

type FinancialContext struct {
	MonthlyIncomeMinor     *int64 `json:"monthly_income_minor"`
	SpendingBudgetMinor    *int64 `json:"spending_budget_minor"`
	EssentialExpensesMinor *int64 `json:"essential_expenses_minor"`
	BudgetID               *int64 `json:"budget_id,omitempty,string"`
}
type Input struct {
	Text            string           `json:"text"`
	Context         FinancialContext `json:"financial_context"`
	ExpectedVersion int64            `json:"expected_version,omitempty"`
}
type Parsed struct {
	GoalType               string            `json:"goal_type"`
	PeriodMonths           *int64            `json:"period_months"`
	SavingsMonthlyMinor    *int64            `json:"savings_monthly_minor"`
	SavingsTargetMinor     *int64            `json:"savings_target_minor"`
	MonthlyIncomeMinor     *int64            `json:"monthly_income_minor"`
	SpendingBudgetMinor    *int64            `json:"spending_budget_minor"`
	EssentialExpensesMinor *int64            `json:"essential_expenses_minor"`
	Currency               string            `json:"currency"`
	ProhibitedOperations   []string          `json:"prohibited_operations"`
	Constraints            []Constraint      `json:"constraints"`
	MissingInformation     []string          `json:"missing_information"`
	Evidence               map[string]string `json:"evidence"`
}
type Constraint struct {
	Kind        string `json:"kind"`
	Description string `json:"description"`
	Evidence    string `json:"evidence"`
}
type Goal struct {
	entity.Base
	CurrentVersion int64  `json:"current_version"`
	Status         string `json:"status"`
}

func (Goal) TableName() string { return "banking_life_goals" }

type GoalVersion struct {
	SkillID      string `json:"skill_id"`
	SkillVersion int64  `json:"skill_version"`
	entity.Base
	GoalID      int64           `json:"goal_id,string"`
	Version     int64           `json:"version"`
	Text        string          `json:"text"`
	ContextJSON json.RawMessage `json:"financial_context" gorm:"column:context_json"`
	ParsedJSON  json.RawMessage `json:"parsed" gorm:"column:parsed_json"`
	ParseStatus string          `json:"parse_status"`
	ParseID     string          `json:"parse_id"`
	LeaseUntil  int64           `json:"lease_until"`
	WorkflowID  string          `json:"workflow_id"`
	ExecuteID   string          `json:"execute_id"`
	ErrorCode   string          `json:"error_code"`
	TraceID     string          `json:"trace_id"`
	AuditID     string          `json:"audit_id"`
}

func (GoalVersion) TableName() string { return "banking_goal_versions" }

type GoalConstraint struct {
	entity.Base
	GoalVersionID int64  `json:"goal_version_id,string"`
	Kind          string `json:"kind"`
	Description   string `json:"description"`
	Evidence      string `json:"evidence"`
}

func (GoalConstraint) TableName() string { return "banking_goal_constraints" }

type Task struct {
	TaskID              string         `json:"task_id"`
	TaskType            string         `json:"task_type"`
	Dependencies        []string       `json:"dependencies"`
	Parameters          map[string]any `json:"parameters"`
	RequiredPermissions []string       `json:"required_permissions"`
	ExecutionMode       string         `json:"execution_mode"`
	Status              string         `json:"status"`
}
type Conflict struct {
	Code                 string        `json:"code"`
	Reason               string        `json:"reason"`
	Alternatives         []Alternative `json:"alternatives"`
	RequiresConfirmation bool          `json:"requires_confirmation"`
}
type Alternative struct {
	Description          string `json:"description"`
	MonthlySavingsMinor  *int64 `json:"monthly_savings_minor"`
	RequiresConfirmation bool   `json:"requires_confirmation"`
}
type Plan struct {
	SchemaVersion             string           `json:"schema_version"`
	Status                    string           `json:"status"`
	FinancialExecutionEnabled bool             `json:"financial_execution_enabled"`
	Tasks                     []Task           `json:"tasks"`
	MissingInformation        []string         `json:"missing_information"`
	Conflicts                 []Conflict       `json:"conflicts"`
	Constraints               []Constraint     `json:"constraints"`
	Confirmations             []string         `json:"confirmations"`
	FinancialContext          FinancialContext `json:"financial_context"`
	BudgetSnapshot            json.RawMessage  `json:"budget_snapshot"`
}
type TaskPlan struct {
	entity.Base
	GoalID        int64           `json:"goal_id,string"`
	GoalVersionID int64           `json:"goal_version_id,string"`
	Version       int64           `json:"version"`
	Status        string          `json:"status"`
	PlanJSON      json.RawMessage `json:"plan" gorm:"column:plan_json"`
	TraceID       string          `json:"trace_id"`
	AuditID       string          `json:"audit_id"`
}

func (TaskPlan) TableName() string { return "banking_task_plans" }

type TaskDependency struct {
	entity.Base
	PlanID    int64  `json:"plan_id,string"`
	TaskID    string `json:"task_id"`
	DependsOn string `json:"depends_on"`
}

func (TaskDependency) TableName() string { return "banking_task_dependencies" }

type View struct {
	Goal    *Goal        `json:"goal"`
	Version *GoalVersion `json:"version"`
	Plan    *TaskPlan    `json:"task_plan"`
}
