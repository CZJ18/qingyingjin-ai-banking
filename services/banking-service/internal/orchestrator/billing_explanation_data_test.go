package orchestrator

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestBillingExplanationContainsOnlyApprovedSummary(t *testing.T) {
	raw := json.RawMessage(`{"currency":"CNY","total_income":100000,"total_expense":24000,"transaction_count":3,"category_breakdown":[{"category":"dining","expense_minor":24000,"merchant":"PRIVATE-MERCHANT"}],"snapshot":{"transactions":[{"id":"PRIVATE-ROW","merchant":"PRIVATE-MERCHANT"}]},"account_id":"PRIVATE-ACCOUNT","report_id":"PRIVATE-REPORT"}`)
	got, err := billingExplanationData(raw)
	if err != nil || strings.Contains(string(got), "PRIVATE") || !strings.Contains(string(got), `"total_expense":24000`) {
		t.Fatal("billing disclosure boundary", string(got), err)
	}
	var result map[string]json.RawMessage
	if json.Unmarshal(got, &result) != nil || len(result) != 6 {
		t.Fatal("unexpected summary fields")
	}
	if !strings.Contains(string(result["analysis_limitations"]), "completeness_not_verified") {
		t.Fatal("model did not receive evidence limitations", string(got))
	}
	if _, err = billingExplanationData(json.RawMessage(`{"currency":"CNY"}`)); err == nil {
		t.Fatal("missing deterministic summary silently accepted")
	}
}
