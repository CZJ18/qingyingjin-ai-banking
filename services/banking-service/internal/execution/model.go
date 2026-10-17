package execution

import (
	"bytes"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/service"
	"github.com/qingyingjin/banking-service/internal/subscription"
	"io"
)

type PrepareInput struct {
	Kind             string          `json:"kind"`
	Parameters       json.RawMessage `json:"parameters"`
	ExpiresInSeconds int64           `json:"expires_in_seconds,omitempty"`
}
type AuthorizeInput struct {
	Decision          string `json:"decision"`
	IntentDigest      string `json:"intent_digest"`
	ConfirmationNonce string `json:"confirmation_nonce"`
}
type ExecuteInput struct {
	AuthorizationID string `json:"authorization_id"`
	IntentDigest    string `json:"intent_digest"`
	ExpectedVersion int64  `json:"expected_version"`
}
type View struct {
	Operation      entity.OperationIntent     `json:"operation"`
	Authorizations []entity.UserAuthorization `json:"authorizations"`
	RiskEvents     []entity.RiskEvent         `json:"risk_events"`
}
type SavingsInput struct {
	GoalID int64                     `json:"goal_id,string"`
	Input  service.GoalTransferInput `json:"input"`
}
type StatusInput struct {
	AccountID       int64  `json:"account_id,string"`
	ExpectedVersion int64  `json:"expected_version"`
	Status          string `json:"status"`
}
type BudgetRuleInput struct {
	BudgetID       int64  `json:"budget_id,string"`
	RuleID         int64  `json:"rule_id,string"`
	ExpectedDigest string `json:"expected_digest"`
	Enabled        bool   `json:"enabled"`
}
type ManageInput struct {
	SubscriptionID int64                    `json:"subscription_id,string"`
	Input          subscription.ManageInput `json:"input"`
}

func strict(raw []byte, out any) error {
	if len(raw) == 0 || len(raw) > 1<<20 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return entity.Fail("INVALID_ARGUMENT", "parameters must be a JSON object")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if e := d.Decode(out); e != nil {
		return entity.Fail("INVALID_ARGUMENT", "invalid typed operation parameters")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return entity.Fail("INVALID_ARGUMENT", "one JSON object required")
	}
	return nil
}
func marshal(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
func kindValid(s string) bool {
	switch s {
	case "recipient_transfer", "aa_payment", "card_manage", "transfer", "savings_deposit", "savings_withdrawal", "cancel_subscription", "budget_rule_update", "account_status", "open_account", "import_transaction", "create_budget", "create_savings_goal", "subscription_manage":
		return true
	}
	return false
}
