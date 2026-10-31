package orchestrator

import (
	"context"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/service"
	"testing"
)

func TestGatewayMissingInputRemainsAPlaceholder(t *testing.T) {
	if err := ValidateParameters("require_gateway", json.RawMessage(`{}`), false); err != nil {
		t.Fatal(err)
	}
	if err := ValidateParameters("require_gateway", json.RawMessage(`{}`), true); entity.Code(err) != "INPUT_REQUIRED" {
		t.Fatal(err)
	}
}

func TestGatewaySuggestionCreatesNoFinancialIntentOrTransport(t *testing.T) {
	call := Call{Meta: service.Meta{UserID: 42}, TaskID: "suggestion-task", AgentType: "supervisor", SkillID: "operation_preparation", SkillVersion: 1, Mode: "intent", Tool: "require_gateway", Confirmed: true, Parameters: json.RawMessage(`{"kind":"card_manage","parameters":{"action":"lock","card_id":"synthetic"}}`)}
	x := &HTTPExecutor{} // No HTTP client, Coze runner, auth issuer, database or gateway.
	out, err := x.Run(context.Background(), call)
	if err != nil || out.FinancialExecutionEnabled {
		t.Fatal(out, err)
	}
	var result map[string]any
	if json.Unmarshal(out.Result, &result) != nil || result["server_validated"] != false || result["authorization_granted"] != false || result["requires_user_preparation"] != true {
		t.Fatal(string(out.Result))
	}
	if err := ValidateOutput(call, out); err != nil {
		t.Fatal(err)
	}
	call.Confirmed = false
	if _, err := x.Run(context.Background(), call); entity.Code(err) != "CONFIRMATION_REQUIRED" {
		t.Fatal(err)
	}
}

func TestGatewayAcceptsBoundedOperationSuggestions(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"card_manage","parameters":{"action":"lock","card_id":"2"}}`,
		`{"kind":"occasion_plan","parameters":{"name":"家人","birthday":"2026-11-01","account_id":"1","reserve_minor":100000,"items":["flowers","cake"]}}`,
		`{"kind":"investment_order","parameters":{"action":"buy","product_id":"sim-cash-v1","account_id":"1","amount_minor":10000,"assessment_id":"reviewed-assessment"},"expires_in_seconds":900}`,
		`{"kind":"transfer","parameters":{"source_account_id":"1","target_account_id":"2","amount_minor":100,"currency":"CNY","description":"聚餐"}}`,
	} {
		if err := ValidateParameters("require_gateway", json.RawMessage(raw), true); err != nil {
			t.Errorf("valid unprivileged proposal rejected: %v", err)
		}
	}
}

func TestGatewayRejectsInventedToolsAndAuthorizationInSuggestions(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"execute_sql","parameters":{}}`,
		`{"kind":"card_manage","parameters":{},"confirmed":true}`,
		`{"kind":"card_manage","parameters":{"authorization_id":"invented"}}`,
		`{"kind":"card_manage","parameters":{"details":{"user_id":"7"}}}`,
		`{"kind":"occasion_plan","parameters":{"reserve_minor":1.2}}`,
		`{"kind":"occasion_plan","parameters":{"reserve_minor":true}}`,
		`{"kind":"card_manage","parameters":{},"expires_in_seconds":0}`,
		`{"kind":"card_manage","parameters":{},"expires_in_seconds":901}`,
	} {
		if err := ValidateParameters("require_gateway", json.RawMessage(raw), true); err == nil {
			t.Errorf("unsafe suggestion accepted: %s", raw)
		}
	}
}

func TestSupervisorCannotChangeSelectedProposalSourceAccount(t *testing.T) {
	b := Bindings{AccountID: "1"}
	for _, raw := range []string{
		`{"kind":"scheduled_transfer","parameters":{"source_account_id":"99"}}`,
		`{"kind":"occasion_plan","parameters":{"account_id":"99"}}`,
		`{"kind":"investment_order","parameters":{"account_id":"99"}}`,
		`{"kind":"card_manage","parameters":{"bank_account_id":"99"}}`,
	} {
		tasks := []TaskSpec{{AgentType: "supervisor", Tool: "require_gateway", Parameters: json.RawMessage(raw)}}
		if err := validateAccountBindings(tasks, b); err == nil {
			t.Errorf("model expanded selected account: %s", raw)
		}
	}
}
