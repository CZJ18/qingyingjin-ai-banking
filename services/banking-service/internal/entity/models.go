package entity

// Core models are stored only in the independent Banking schema. The account
// and transaction column names preserve the public Coze Banking read contract.
type Base struct {
	ID        int64 `json:"id,string" gorm:"primaryKey;autoIncrement"`
	UserID    int64 `json:"user_id,string"`
	CreatedAt int64 `json:"created_at" gorm:"autoCreateTime:milli"`
	UpdatedAt int64 `json:"updated_at" gorm:"autoUpdateTime:milli"`
}
type User struct {
	ID          int64  `json:"id,string" gorm:"primaryKey;autoIncrement:false"`
	DisplayName string `json:"display_name"`
	Status      string `json:"status"`
	CreatedAt   int64  `json:"created_at" gorm:"autoCreateTime:milli"`
	UpdatedAt   int64  `json:"updated_at" gorm:"autoUpdateTime:milli"`
}

func (User) TableName() string { return "banking_users" }

type BankAccount struct {
	Base
	ExternalAccountID   *string `json:"external_account_id,omitempty"`
	DisplayName         string  `json:"display_name"`
	AccountType         string  `json:"account_type"`
	MaskedAccountNumber string  `json:"masked_account_number"`
	Currency            string  `json:"currency"`
	BalanceMinor        int64   `json:"balance_minor"`
	Status              string  `json:"status"`
	Version             int64   `json:"version"`
	IsSimulated         bool    `json:"is_simulated"`
	SavingsGoalID       *int64  `json:"savings_goal_id,omitempty,string"`
}

func (BankAccount) TableName() string { return "banking_bank_accounts" }

// AccountBalance is a read-only projection, not a second source of funds.
type AccountBalance struct {
	AccountID          int64  `json:"account_id,string"`
	Currency           string `json:"currency"`
	BalanceMinor       int64  `json:"balance_minor"`
	LedgerBalanceMinor int64  `json:"ledger_balance_minor"`
	Version            int64  `json:"version"`
	Reconciled         bool   `json:"reconciled"`
}
type Transaction struct {
	Base
	BankAccountID         int64   `json:"bank_account_id,string"`
	ExternalTransactionID *string `json:"external_transaction_id,omitempty"`
	Direction             string  `json:"direction"`
	AmountMinor           int64   `json:"amount_minor"`
	Currency              string  `json:"currency"`
	Category              string  `json:"category"`
	MerchantName          string  `json:"merchant_name"`
	Description           string  `json:"description"`
	OccurredAt            int64   `json:"occurred_at"`
	SourceMetadata        []byte  `json:"-"`
	Kind                  string  `json:"kind"`
	EffectMode            string  `json:"effect_mode"`
	JournalID             *string `json:"journal_id,omitempty"`
	TransferID            *int64  `json:"transfer_id,omitempty,string"`
	TraceID               string  `json:"trace_id"`
	TaskID                string  `json:"task_id,omitempty"`
	AuditID               string  `json:"audit_id"`
}

func (Transaction) TableName() string { return "banking_transactions" }

type LedgerEntry struct {
	ID            int64  `json:"id,string" gorm:"primaryKey;autoIncrement"`
	UserID        int64  `json:"user_id,string"`
	JournalID     string `json:"journal_id"`
	Sequence      int    `json:"sequence"`
	AccountID     *int64 `json:"account_id,omitempty,string"`
	TransactionID *int64 `json:"transaction_id,omitempty,string"`
	Currency      string `json:"currency"`
	DeltaMinor    int64  `json:"delta_minor"`
	Side          string `json:"side"`
	CreatedAt     int64  `json:"created_at" gorm:"autoCreateTime:milli"`
}

func (LedgerEntry) TableName() string { return "banking_ledger_entries" }

type Audit struct {
	ID         string `json:"id,string" gorm:"primaryKey"`
	UserID     int64  `json:"user_id,string"`
	Action     string `json:"action"`
	ResourceID int64  `json:"resource_id,string"`
	Outcome    string `json:"outcome"`
	TraceID    string `json:"trace_id"`
	TaskID     string `json:"task_id,omitempty"`
	ErrorCode  string `json:"error_code,omitempty"`
	CreatedAt  int64  `json:"created_at" gorm:"autoCreateTime:milli"`
}

func (Audit) TableName() string { return "banking_core_audits" }

type Idempotency struct {
	UserID      int64  `gorm:"primaryKey"`
	Action      string `gorm:"primaryKey"`
	Key         string `gorm:"column:idem_key;primaryKey"`
	RequestHash string
	Response    []byte
	CreatedAt   int64 `gorm:"autoCreateTime:milli"`
}

func (Idempotency) TableName() string { return "banking_core_idempotency" }

type TransferIntent struct {
	Base
	SourceAccountID int64  `json:"source_account_id,string"`
	TargetAccountID int64  `json:"target_account_id,string"`
	AmountMinor     int64  `json:"amount_minor"`
	Currency        string `json:"currency"`
	Description     string `json:"description"`
	Purpose         string `json:"purpose"`
	SavingsGoalID   *int64 `json:"savings_goal_id,omitempty,string"`
	Status          string `json:"status"`
	ApprovalHash    string `json:"approval_hash"`
	ExpiresAt       int64  `json:"expires_at"`
	JournalID       string `json:"journal_id,omitempty"`
	FailureCode     string `json:"failure_code,omitempty"`
	AuditID         string `json:"audit_id"`
	TraceID         string `json:"trace_id"`
	TaskID          string `json:"task_id,omitempty"`
}

func (TransferIntent) TableName() string { return "banking_transfer_intents" }

type Approval struct {
	TransferID   int64  `json:"transfer_id,string" gorm:"primaryKey;autoIncrement:false"`
	UserID       int64  `json:"user_id,string"`
	Decision     string `json:"decision"`
	ApprovalHash string `json:"approval_hash"`
	ExpiresAt    int64  `json:"expires_at"`
	ConsumedAt   int64  `json:"consumed_at,omitempty"`
	CreatedAt    int64  `json:"created_at" gorm:"autoCreateTime:milli"`
}

func (Approval) TableName() string { return "banking_transfer_approvals" }

type Budget struct {
	Base
	Month      string           `json:"month"`
	Currency   string           `json:"currency"`
	TotalMinor int64            `json:"total_minor"`
	Categories []BudgetCategory `json:"categories" gorm:"-"`
	Rules      []BudgetRule     `json:"rules" gorm:"-"`
}

func (Budget) TableName() string { return "banking_budgets" }

type BudgetCategory struct {
	ID                    int64  `json:"id,string" gorm:"primaryKey;autoIncrement"`
	UserID                int64  `json:"user_id,string"`
	BudgetID              int64  `json:"budget_id,string"`
	Category              string `json:"category"`
	AllocatedMinor        int64  `json:"allocated_minor"`
	EssentialReserveMinor int64  `json:"essential_reserve_minor"`
}

func (BudgetCategory) TableName() string { return "banking_budget_categories" }

type BudgetRule struct {
	ID       int64  `json:"id,string" gorm:"primaryKey;autoIncrement"`
	UserID   int64  `json:"user_id,string"`
	BudgetID int64  `json:"budget_id,string"`
	Kind     string `json:"kind"`
	Enabled  bool   `json:"enabled"`
}

func (BudgetRule) TableName() string { return "banking_budget_rules" }

type SavingsGoal struct {
	Base
	Name                string       `json:"name"`
	TargetMinor         int64        `json:"target_minor"`
	Currency            string       `json:"currency"`
	StartsAt            int64        `json:"starts_at"`
	EndsAt              int64        `json:"ends_at"`
	AccountID           int64        `json:"account_id,string"`
	Status              string       `json:"status"`
	Plan                *SavingsPlan `json:"plan,omitempty" gorm:"-"`
	CurrentMinor        int64        `json:"current_minor" gorm:"-"`
	ProgressBasisPoints int64        `json:"progress_basis_points" gorm:"-"`
}

func (SavingsGoal) TableName() string { return "banking_savings_goals" }

type SavingsPlan struct {
	ID           int64  `json:"id,string" gorm:"primaryKey;autoIncrement"`
	UserID       int64  `json:"user_id,string"`
	GoalID       int64  `json:"goal_id,string"`
	MonthlyMinor int64  `json:"monthly_minor"`
	Cadence      string `json:"cadence"`
	Status       string `json:"status"`
}

func (SavingsPlan) TableName() string { return "banking_savings_plans" }

type SavingsLedger struct {
	ID         int64  `json:"id,string" gorm:"primaryKey;autoIncrement"`
	UserID     int64  `json:"user_id,string"`
	GoalID     int64  `json:"goal_id,string"`
	TransferID int64  `json:"transfer_id,string"`
	JournalID  string `json:"journal_id"`
	DeltaMinor int64  `json:"delta_minor"`
	Currency   string `json:"currency"`
	CreatedAt  int64  `json:"created_at" gorm:"autoCreateTime:milli"`
}

func (SavingsLedger) TableName() string { return "banking_savings_ledgers" }
