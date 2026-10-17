// Package billing provides financial analysis only; it has no funds executor.
package billing

import (
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/entity"
)

var Tools = []string{"get_transactions", "get_account_summary", "analyze_spending", "compare_budget", "detect_anomalies", "generate_billing_report"}

type Input struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Timezone  string `json:"timezone,omitempty"`
	AccountID *int64 `json:"account_id,omitempty,string"`
	Limit     int    `json:"limit,omitempty"`
	Offset    int    `json:"offset,omitempty"`
}
type Period struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Timezone string `json:"timezone"`
}
type Evidence struct {
	TransactionID    int64  `json:"transaction_id,string"`
	AccountID        int64  `json:"account_id,string"`
	OccurredAt       int64  `json:"occurred_at"`
	AmountMinor      int64  `json:"amount_minor"`
	Direction        string `json:"direction"`
	OriginalCategory string `json:"original_category"`
	Category         string `json:"category"`
	Merchant         string `json:"merchant"`
	EffectMode       string `json:"effect_mode"`
}
type Category struct {
	Category       string   `json:"category"`
	ExpenseMinor   int64    `json:"expense_minor"`
	TransactionIDs []string `json:"transaction_ids"`
}
type Bucket struct {
	Label          string   `json:"label"`
	IncomeMinor    int64    `json:"income_minor"`
	ExpenseMinor   int64    `json:"expense_minor"`
	TransactionIDs []string `json:"transaction_ids"`
}
type Anomaly struct {
	Type           string     `json:"type"`
	Rule           string     `json:"rule"`
	ReviewRequired bool       `json:"review_required"`
	Evidence       []Evidence `json:"evidence"`
	ConfirmedLoss  bool       `json:"confirmed_loss"`
}
type BudgetCategory struct {
	Category                       string `json:"category"`
	AllocatedMinor                 int64  `json:"allocated_minor"`
	SpentMinor                     int64  `json:"spent_minor"`
	RemainingMinor                 int64  `json:"remaining_minor"`
	EssentialReserveRemainingMinor int64  `json:"essential_reserve_remaining_minor"`
}
type BudgetStatus struct {
	TransactionIDs                 []string         `json:"transaction_ids"`
	BudgetID                       int64            `json:"budget_id,string"`
	Month                          string           `json:"month"`
	Scope                          string           `json:"scope"`
	TotalMinor                     int64            `json:"total_minor"`
	SpentMinor                     int64            `json:"spent_minor"`
	RemainingMinor                 int64            `json:"remaining_minor"`
	EssentialReserveRemainingMinor int64            `json:"essential_reserve_remaining_minor"`
	AvailableAfterReserveMinor     int64            `json:"available_after_reserve_minor"`
	Categories                     []BudgetCategory `json:"categories"`
}
type Candidate struct {
	Kind                 string   `json:"kind"`
	CandidateAmountMinor int64    `json:"candidate_amount_minor"`
	TransactionIDs       []string `json:"transaction_ids"`
	RequiresConfirmation bool     `json:"requires_confirmation"`
}
type Savings struct {
	ConfirmedMinor   int64       `json:"confirmed_minor"`
	ReviewCandidates []Candidate `json:"review_candidates"`
}
type Trend struct {
	PreviousPeriod         Period   `json:"previous_period"`
	PreviousExpenseMinor   int64    `json:"previous_expense_minor"`
	CurrentExpenseMinor    int64    `json:"current_expense_minor"`
	DeltaMinor             int64    `json:"delta_minor"`
	ChangeBasisPoints      *int64   `json:"change_basis_points"`
	PreviousTransactionIDs []string `json:"previous_transaction_ids"`
	Status                 string   `json:"status"`
}
type Finding struct {
	ID             string   `json:"finding_id"`
	Description    string   `json:"description"`
	TransactionIDs []string `json:"transaction_ids"`
}
type Report struct {
	ID                        string               `json:"report_id"`
	UserID                    int64                `json:"user_id,string"`
	Tool                      string               `json:"tool"`
	AnalysisPeriod            Period               `json:"analysis_period"`
	Currency                  string               `json:"currency"`
	TotalIncome               int64                `json:"total_income"`
	TotalExpense              int64                `json:"total_expense"`
	CategoryBreakdown         []Category           `json:"category_breakdown"`
	BudgetStatus              []BudgetStatus       `json:"budget_status"`
	Anomalies                 []Anomaly            `json:"anomalies"`
	PotentialSavings          Savings              `json:"potential_savings"`
	EvidenceTransactionIDs    []string             `json:"evidence_transaction_ids"`
	Daily                     []Bucket             `json:"daily"`
	Weekly                    []Bucket             `json:"weekly"`
	Monthly                   []Bucket             `json:"monthly"`
	Trend                     Trend                `json:"trend"`
	Transactions              []Evidence           `json:"transactions"`
	TransactionCount          int64                `json:"transaction_count"`
	Accounts                  []entity.BankAccount `json:"accounts"`
	Findings                  []Finding            `json:"findings"`
	FinancialExecutionEnabled bool                 `json:"financial_execution_enabled"`
	SnapshotAt                int64                `json:"snapshot_at"`
	AuditID                   string               `json:"audit_id"`
	TraceID                   string               `json:"trace_id"`
	TaskID                    string               `json:"task_id"`
}
type StoredReport struct {
	ID         string `gorm:"primaryKey"`
	UserID     int64
	ReportJSON json.RawMessage
	AuditID    string
	TraceID    string
	TaskID     string
	CreatedAt  int64 `gorm:"autoCreateTime:milli"`
}

func (StoredReport) TableName() string { return "banking_billing_reports" }

type Snapshot struct {
	Accounts     []entity.BankAccount
	Transactions []entity.Transaction
	Budgets      []entity.Budget
	At           int64
}
