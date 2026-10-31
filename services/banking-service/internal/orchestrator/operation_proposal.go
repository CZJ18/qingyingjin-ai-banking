package orchestrator

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"

	"github.com/qingyingjin/banking-service/internal/entity"
)

// A suggestion is not an OperationIntent. Only a human request to the existing
// prepare endpoint can validate financial ownership/risk and create an intent.
type operationSuggestion struct {
	Kind             string          `json:"kind"`
	Parameters       json.RawMessage `json:"parameters"`
	ExpiresInSeconds json.RawMessage `json:"expires_in_seconds,omitempty"`
}

func suggestedKind(kind string) bool {
	switch kind {
	case "transfer", "cancel_subscription", "savings_deposit", "savings_withdrawal", "create_savings_goal", "budget_rule_update",
		"card_manage", "recipient_transfer", "aa_payment", "scheduled_transfer", "investment_assessment", "investment_order", "occasion_plan", "occasion_order", "occasion_release":
		return true
	}
	return false
}

func parseOperationSuggestion(raw json.RawMessage) (out operationSuggestion, err error) {
	if strictJSON(raw, &out) != nil || !suggestedKind(out.Kind) {
		return out, entity.Fail("INVALID_ARGUMENT", "an existing typed operation suggestion is required")
	}
	params := bytes.TrimSpace(out.Parameters)
	if len(params) == 0 || params[0] != '{' || len(params) > 65536 {
		return out, entity.Fail("INVALID_ARGUMENT", "bounded operation object required")
	}
	if len(out.ExpiresInSeconds) > 0 {
		var seconds int64
		if json.Unmarshal(out.ExpiresInSeconds, &seconds) != nil || seconds < 1 || seconds > 900 {
			return out, entity.Fail("INVALID_ARGUMENT", "suggestion lifetime must be1..900 seconds")
		}
	}
	var value any
	dec := json.NewDecoder(bytes.NewReader(params))
	dec.UseNumber()
	if dec.Decode(&value) != nil || !safeSuggestionValue(value, 0) {
		return out, entity.Fail("INVALID_ARGUMENT", "suggestions cannot contain identity, authorization, secrets or noninteger values")
	}
	return out, nil
}

func safeSuggestionValue(value any, depth int) bool {
	if depth > 20 {
		return false
	}
	switch v := value.(type) {
	case map[string]any:
		for key, item := range v {
			switch strings.ReplaceAll(strings.ToLower(key), "-", "_") {
			case "confirmed", "authorization_id", "authorization", "authorizations", "confirmation_nonce", "nonce", "signature", "user_id", "owner_id", "owner_engine", "agent_type", "lease_token", "token", "bearer", "service_key", "api_key", "password", "secret", "access_token", "refresh_token", "agent_token", "x_banking_agent_key":
				return false
			}
			if strings.HasSuffix(key, "_minor") {
				n, ok := item.(json.Number)
				if !ok {
					return false
				}
				number, err := strconv.ParseInt(string(n), 10, 64)
				if err != nil || number < 0 || number > entity.MaxBalance {
					return false
				}
			}
			if !safeSuggestionValue(item, depth+1) {
				return false
			}
		}
	case []any:
		for _, item := range v {
			if !safeSuggestionValue(item, depth+1) {
				return false
			}
		}
	case json.Number:
		if _, err := strconv.ParseInt(string(v), 10, 64); err != nil {
			return false
		}
	case nil, string, bool:
	default:
		return false
	}
	return true
}

func suggestionResult(raw json.RawMessage) (json.RawMessage, error) {
	proposal, err := parseOperationSuggestion(raw)
	if err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		Proposal   operationSuggestion `json:"operation_proposal"`
		Human      bool                `json:"requires_user_preparation"`
		Validated  bool                `json:"server_validated"`
		Authorized bool                `json:"authorization_granted"`
	}{Proposal: proposal, Human: true})
}
