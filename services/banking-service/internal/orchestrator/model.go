package orchestrator

import (
	"context"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/service"
)

type TaskSpec struct {
	SkillID       string          `json:"skill_id,omitempty"`
	SkillVersion  int64           `json:"skill_version,omitempty"`
	Blocked       bool            `json:"blocked,omitempty"`
	Key           string          `json:"key"`
	AgentType     string          `json:"agent_type"`
	Tool          string          `json:"tool"`
	Parameters    json.RawMessage `json:"parameters"`
	Dependencies  []string        `json:"dependencies"`
	Priority      int             `json:"priority"`
	ExecutionMode string          `json:"execution_mode,omitempty"`
}
type Bindings struct {
	From                 string `json:"from,omitempty"`
	To                   string `json:"to,omitempty"`
	AccountID            string `json:"account_id,omitempty"`
	FinancialPlanID      string `json:"financial_plan_id,omitempty"`
	LifestyleGoalID      string `json:"lifestyle_goal_id,omitempty"`
	LifestylePlanID      string `json:"lifestyle_plan_id,omitempty"`
	LifestyleGoalVersion int64  `json:"lifestyle_goal_version,omitempty"`
	BudgetID             string `json:"budget_id,omitempty"`
	BillingReportID      string `json:"billing_report_id,omitempty"`
}
type SubmitInput struct {
	Instruction string     `json:"instruction,omitempty"`
	TaskPlanID  string     `json:"task_plan_id,omitempty"`
	Tasks       []TaskSpec `json:"tasks,omitempty"`
	Bindings    Bindings   `json:"bindings"`
	Strategy    string     `json:"strategy,omitempty"`
	MaxAttempts int        `json:"max_attempts,omitempty"`
	TimeoutMS   int64      `json:"timeout_ms,omitempty"`
	UseCoze     bool       `json:"use_coze"`
}
type ConfirmInput struct {
	TaskKey         string `json:"task_key"`
	ExpectedVersion int64  `json:"expected_version"`
	Decision        string `json:"decision"`
}
type InputsInput struct {
	TaskKey         string          `json:"task_key"`
	ExpectedVersion int64           `json:"expected_version"`
	Parameters      json.RawMessage `json:"parameters"`
}
type Job struct {
	ID             string `json:"id" gorm:"primaryKey"`
	UserID         int64  `json:"user_id,string"`
	Status         string `json:"status"`
	Strategy       string `json:"strategy"`
	Version        int64  `json:"version"`
	MaxAttempts    int    `json:"max_attempts"`
	TimeoutMS      int64  `json:"timeout_ms"`
	UseCoze        bool   `json:"use_coze"`
	SourcePlanID   string `json:"source_plan_id"`
	RequestHash    string `json:"-"`
	IdempotencyKey string `json:"-"`
	LeaseToken     string `json:"-"`
	LeaseUntil     int64  `json:"lease_until"`
	TraceID        string `json:"trace_id"`
	AuditID        string `json:"audit_id"`
	CreatedAt      int64  `json:"created_at" gorm:"autoCreateTime:milli"`
	UpdatedAt      int64  `json:"updated_at" gorm:"autoUpdateTime:milli"`
}

func (Job) TableName() string { return "banking_agent_jobs" }

type AgentTask struct {
	SkillID          string          `json:"skill_id"`
	SkillVersion     int64           `json:"skill_version"`
	ProtectedBlocked bool            `json:"protected_blocked"`
	ID               string          `json:"task_id" gorm:"primaryKey"`
	UserID           int64           `json:"user_id,string"`
	JobID            string          `json:"job_id"`
	Key              string          `json:"key" gorm:"column:task_key"`
	AgentType        string          `json:"agent_type"`
	Tool             string          `json:"tool"`
	Mode             string          `json:"execution_mode"`
	Priority         int             `json:"priority"`
	Status           string          `json:"status"`
	Parameters       json.RawMessage `json:"parameters" gorm:"column:parameters_json"`
	Output           json.RawMessage `json:"output" gorm:"column:output_json"`
	Attempts         int             `json:"attempts"`
	RetryAt          int64           `json:"retry_at"`
	Confirmation     string          `json:"confirmation"`
	ErrorCode        string          `json:"error_code"`
	CreatedAt        int64           `json:"created_at" gorm:"autoCreateTime:milli"`
	UpdatedAt        int64           `json:"updated_at" gorm:"autoUpdateTime:milli"`
}

func (AgentTask) TableName() string { return "banking_agent_tasks" }

type AgentDependency struct {
	ID        int64  `json:"id,string" gorm:"primaryKey;autoIncrement"`
	UserID    int64  `json:"user_id,string"`
	JobID     string `json:"job_id"`
	TaskID    string `json:"task_id"`
	DependsOn string `json:"depends_on"`
}

func (AgentDependency) TableName() string { return "banking_agent_dependencies" }

type AgentExecution struct {
	ID                string          `json:"id" gorm:"primaryKey"`
	UserID            int64           `json:"user_id,string"`
	JobID             string          `json:"job_id"`
	TaskID            string          `json:"task_id"`
	AgentType         string          `json:"agent_type"`
	Attempt           int             `json:"attempt"`
	Status            string          `json:"status"`
	StartedAt         int64           `json:"started_at"`
	FinishedAt        int64           `json:"finished_at"`
	DurationMS        int64           `json:"duration_ms"`
	ErrorCode         string          `json:"error_code"`
	WorkflowExecuteID string          `json:"workflow_execute_id"`
	Input             json.RawMessage `json:"input" gorm:"column:input_json"`
	Output            json.RawMessage `json:"output" gorm:"column:output_json"`
	LeaseToken        string          `json:"-"`
}

func (AgentExecution) TableName() string { return "banking_agent_executions" }

type ToolInvocation struct {
	ID            string `json:"id" gorm:"primaryKey"`
	UserID        int64  `json:"user_id,string"`
	JobID         string `json:"job_id"`
	TaskID        string `json:"task_id"`
	ExecutionID   string `json:"execution_id"`
	Tool          string `json:"tool"`
	InvocationKey string `json:"invocation_key"`
	Status        string `json:"status"`
	StartedAt     int64  `json:"started_at"`
	FinishedAt    int64  `json:"finished_at"`
	DurationMS    int64  `json:"duration_ms"`
	ErrorCode     string `json:"error_code"`
	AuditID       string `json:"audit_id"`
}

func (ToolInvocation) TableName() string { return "banking_tool_invocations" }

type ExecutionTrace struct {
	SkillID      string `json:"skill_id"`
	SkillVersion int64  `json:"skill_version"`
	ID           int64  `json:"id,string" gorm:"primaryKey;autoIncrement"`
	UserID       int64  `json:"user_id,string"`
	JobID        string `json:"job_id"`
	TaskID       string `json:"task_id"`
	Event        string `json:"event"`
	Status       string `json:"status"`
	ErrorCode    string `json:"error_code"`
	AuditID      string `json:"audit_id"`
	TraceID      string `json:"trace_id"`
	CreatedAt    int64  `json:"created_at" gorm:"autoCreateTime:milli"`
}

func (ExecutionTrace) TableName() string { return "banking_execution_traces" }

type Evidence struct {
	Type    string `json:"type"`
	ID      string `json:"id"`
	AuditID string `json:"audit_id"`
}
type TaskError struct {
	Code      string `json:"code"`
	Retryable bool   `json:"retryable"`
}
type NextAction struct {
	Type                 string `json:"type"`
	RequiresConfirmation bool   `json:"requires_confirmation"`
}
type AgentOutput struct {
	SkillID                   string          `json:"skill_id,omitempty"`
	SkillVersion              int64           `json:"skill_version,omitempty"`
	RuleVersion               string          `json:"rule_version,omitempty"`
	TaskID                    string          `json:"task_id"`
	AgentType                 string          `json:"agent_type"`
	Status                    string          `json:"status"`
	Result                    json.RawMessage `json:"result"`
	Evidence                  []Evidence      `json:"evidence"`
	Errors                    []TaskError     `json:"errors"`
	NextActions               []NextAction    `json:"next_actions"`
	Explanation               string          `json:"explanation"`
	ExplanationEvidenceIDs    []string        `json:"explanation_evidence_ids,omitempty"`
	ExplanationFindingIDs     []string        `json:"explanation_finding_ids,omitempty"`
	WorkflowExecuteID         string          `json:"workflow_execute_id"`
	FinancialExecutionEnabled bool            `json:"financial_execution_enabled"`
}
type View struct {
	Job                       Job               `json:"job"`
	Tasks                     []AgentTask       `json:"tasks"`
	Dependencies              []AgentDependency `json:"dependencies"`
	FinancialExecutionEnabled bool              `json:"financial_execution_enabled"`
}
type Result struct {
	JobID                     string        `json:"job_id"`
	Status                    string        `json:"status"`
	Outputs                   []AgentOutput `json:"outputs"`
	FinancialExecutionEnabled bool          `json:"financial_execution_enabled"`
}
type TraceView struct {
	Events      []ExecutionTrace `json:"events"`
	Executions  []AgentExecution `json:"executions"`
	Invocations []ToolInvocation `json:"invocations"`
}
type Call struct {
	SkillID       string
	SkillVersion  int64
	Mode          string
	LeaseToken    string
	Meta          service.Meta
	JobID         string
	TaskID        string
	AgentType     string
	Tool          string
	Parameters    json.RawMessage
	Dependencies  []AgentOutput
	UseCoze       bool
	Confirmed     bool
	InvocationKey string
}
type Runner interface {
	Run(context.Context, Call) (AgentOutput, error)
}
type InstructionPlanner interface {
	Plan(context.Context, service.Meta, string, Bindings) ([]TaskSpec, error)
}
type ProposalGateway interface {
	Propose(context.Context, Call) (json.RawMessage, error)
}
