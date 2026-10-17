package api

import (
	"context"
	"github.com/getkin/kin-openapi/openapi3"
	"testing"
)

func TestDocumentsAreValidAndPluginHasNoFinancialPermissions(t *testing.T) {
	for name, raw := range map[string][]byte{"main": OpenAPI, "plugin": PluginOpenAPI} {
		doc, err := openapi3.NewLoader().LoadFromData(raw)
		if err != nil {
			t.Fatal(err)
		}
		if err := doc.Validate(context.Background()); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if name == "plugin" {
			for path := range doc.Paths {
				if path == "/api/v1/execution/operations/{id}/execute" || path == "/api/v1/execution/operations/{id}/authorize" {
					t.Fatal("plugin exposes human authorization or execution")
				}
			}
			prepareRef := doc.Components.Schemas["ExecutionPrepareInput"]
			if prepareRef == nil || prepareRef.Value == nil {
				t.Fatal("plugin missing execution prepare contract")
			}
			prepare := prepareRef.Value
			for _, field := range []string{"user_id", "token_id", "authorization_id", "jwt", "permission"} {
				if prepare.Properties[field] != nil {
					t.Fatal("model can supply authority", field)
				}
			}
			if len(doc.Paths) != 24 || doc.Paths["/tools/v1/integration/context"] == nil {
				t.Fatal("plugin has unexpected routes")
			}
			allowed := map[string]bool{"get_transactions": true, "get_account_summary": true, "analyze_spending": true, "compare_budget": true, "detect_anomalies": true, "generate_billing_report": true, "get_billing_report": true, "banking_integration_context": true, "detect_subscriptions": true, "get_subscription_details": true, "analyze_subscription_cost": true, "suggest_subscription_actions": true, "create_cancel_intent": true, "get_subscription_status": true, "get_financial_plan": true, "compare_financial_plans": true, "replan_financial_plan": true}
			allowed["prepare_operation"] = true
			allowed["get_operation"] = true
			allowed["get_orchestration"] = true
			allowed["get_orchestration_result"] = true
			allowed["get_lifestyle_goal"] = true
			allowed["get_lifestyle_plan"] = true
			allowed["compile_lifestyle_plan"] = true
			for _, item := range doc.Paths {
				for _, op := range item.Operations() {
					if op.Responses["200"] == nil {
						t.Fatal("native Coze requires HTTP 200 success for tools", op.OperationID)
					}
					if !allowed[op.OperationID] {
						t.Fatal("unexpected plugin operation", op.OperationID)
					}
				}
			}
			response := doc.Components.Schemas["BillingReportResponse"].Value
			if response.Type != "object" || len(response.AllOf) != 0 || response.Properties["data"].Value.Type != "object" {
				t.Fatal("native Coze response adapter requires explicit object properties")
			}
			var check func(*openapi3.Schema)
			check = func(s *openapi3.Schema) {
				if s.Type == "" {
					t.Fatal("plugin response field has no explicit type")
				}
				for _, p := range s.Properties {
					check(p.Value)
				}
				if s.Items != nil {
					check(s.Items.Value)
				}
			}
			check(response)
			for _, name := range []string{"SubscriptionReportResponse", "SubscriptionDetailResponse", "SubscriptionIntentResponse", "FinancialPlanViewResponse", "OrchestrationViewResponse", "OrchestrationResultResponse", "ExecutionViewResponse"} {
				check(doc.Components.Schemas[name].Value)
			}
			if _, found := doc.Components.SecuritySchemes["UserBearer"]; found {
				t.Fatal("plugin exposes user auth")
			}
		}
	}
}
