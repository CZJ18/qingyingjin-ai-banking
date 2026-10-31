package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/qingyingjin/banking-service/internal/modelgateway"
	"github.com/qingyingjin/banking-service/internal/modelspend"
	"github.com/qingyingjin/banking-service/internal/outbound"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func normalManifestArgs(args []string, file, origin string) []string {
	out := []string{}
	for _, v := range args {
		if v != "--offline-preflight" && v != "--allow-runtime-scope-probe" {
			out = append(out, v)
		}
	}
	return append(out, "--preflight", "--readiness-manifest-file", file, "--gateway-origin", origin, "--workflow-policy-file", file+".pins")
}
func writeManifest(t *testing.T, file string, m readinessManifest) {
	t.Helper()
	raw, e := json.Marshal(m)
	if e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(file, raw, 0600); e != nil {
		t.Fatal(e)
	}
	pins := map[string]string{}
	for _, w := range m.Workflows {
		if w.Enabled {
			pins[w.ID] = w.CanvasSHA256
		}
	}
	raw, _ = json.Marshal(pins)
	if e = os.WriteFile(file+".pins", raw, 0600); e != nil {
		t.Fatal(e)
	}
}
func TestManifestTemplateStartsWithoutWorkflowAuthority(t *testing.T) {
	raw, e := os.ReadFile("../../../../deploy/banking/model-integration-manifest.json.example")
	if e != nil {
		t.Fatal(e)
	}
	var m readinessManifest
	if json.Unmarshal(raw, &m) != nil {
		t.Fatal("template not parseable")
	}
	for role, w := range m.Workflows {
		if w.Enabled || w.ID != "" || w.Version != "" || w.CanvasSHA256 != "" {
			t.Fatalf("empty template enables %s", role)
		}
	}
}
func TestManifestOriginsAndRoleAliasingCannotExpandScope(t *testing.T) {
	for _, kind := range []string{"extra_origin", "wildcard", "invalid_port", "empty_port", "empty_fragment", "trailing_slash", "http_real", "duplicate_role_id"} {
		t.Run(kind, func(t *testing.T) {
			m, args, file := readinessFixture(t)
			switch kind {
			case "extra_origin":
				m.AllowedOrigins = append(m.AllowedOrigins, "https://unapproved.invalid")
			case "wildcard":
				m.AllowedOrigins = append(m.AllowedOrigins, "https://*.invalid")
			case "invalid_port":
				m.CozeOrigin = "https://coze.invalid:99999"
				m.AllowedOrigins[1] = m.CozeOrigin
			case "empty_port":
				m.CozeOrigin += ":"
				m.AllowedOrigins[1] = m.CozeOrigin
			case "empty_fragment":
				m.CozeOrigin += "#"
				m.AllowedOrigins[1] = m.CozeOrigin
			case "trailing_slash":
				m.CozeOrigin += "/"
				m.AllowedOrigins[1] = m.CozeOrigin
			case "http_real":
				m.CozeOrigin = "http://coze.invalid"
				m.AllowedOrigins[1] = m.CozeOrigin
			case "duplicate_role_id":
				m.Workflows["billing"] = m.Workflows["supervisor"]
			}
			writeManifest(t, file, m)
			r, e := captureReadiness(t, append(args, "--readiness-manifest-file", file))
			if e == nil || r["status"] != "FAIL" {
				t.Fatalf("unsafe %s accepted: %v", kind, r)
			}
		})
	}
}
func TestManifestDisabledRoleMustBeExplicit(t *testing.T) {
	m, args, file := readinessFixture(t)
	raw, _ := json.Marshal(m)
	var object map[string]any
	json.Unmarshal(raw, &object)
	delete(object["workflows"].(map[string]any)["billing"].(map[string]any), "enabled")
	raw, _ = json.Marshal(object)
	os.WriteFile(file, raw, 0600)
	r, _ := captureReadiness(t, append(args, "--readiness-manifest-file", file))
	if r["status"] != "FAIL" {
		t.Fatal("absent enabled silently treated as explicit disable")
	}
}
func TestRuntimePreflightCannotIgnoreManifest(t *testing.T) {
	for _, kind := range []string{"missing", "budget", "price", "disabled_supervisor", "gateway_flag", "model", "currency", "requests", "concurrent", "context", "output", "user", "task", "database", "scope", "time", "price_source", "price_digest", "canvas", "enabled_incomplete"} {
		t.Run(kind, func(t *testing.T) {
			m, args, file := readinessFixture(t)
			origin := m.GatewayOrigin
			switch kind {
			case "missing":
				file = ""
			case "budget":
				m.BudgetAmount = "0.5"
			case "price":
				m.PriceVersion = "other-price"
			case "disabled_supervisor":
				m.Workflows["supervisor"] = readinessWorkflow{}
			case "gateway_flag":
				origin = "https://unapproved.invalid"
			case "model":
				m.Model = "unapproved-model"
			case "currency":
				m.Currency = "CNY"
			case "requests":
				m.MaxRequests++
			case "concurrent":
				m.MaxConcurrent++
			case "context":
				m.ContextTokens++
			case "output":
				m.MaxOutputTokens++
			case "user":
				m.UserID++
			case "task":
				m.TaskRequestSHA256 = strings.Repeat("f", 64)
			case "database":
				m.Database = "other_test"
			case "scope":
				m.DataScope = "expanded scope"
			case "time":
				m.ValidFrom = "2020-01-01T00:00:00Z"
			case "price_source":
				m.PriceSource = "review:other"
			case "price_digest":
				m.PriceEvidenceSHA256 = strings.Repeat("f", 64)
			case "canvas":
				w := m.Workflows["supervisor"]
				w.CanvasSHA256 = strings.Repeat("f", 64)
				m.Workflows["supervisor"] = w
			case "enabled_incomplete":
				m.Workflows["billing"] = readinessWorkflow{Enabled: true}
			}
			if file != "" {
				writeManifest(t, file, m)
			}
			r, e := captureReadiness(t, normalManifestArgs(args, file, origin))
			checks := r["checks"].(map[string]any)
			if e == nil || r["blockers"].([]any)[0] != "MANIFEST_BINDING_INVALID_BEFORE_CONFIGURATION" || checks["supplier_credentials"] != "NOT_INSPECTED" {
				t.Fatalf("manifest ignored (%s): %v", kind, r)
			}
		})
	}
}
func TestManifestRejectsMalformedDeclarationTypesAndBounds(t *testing.T) {
	cases := []struct {
		name, field string
		value       any
	}{
		{"missing_model", "model", nil}, {"negative_requests", "max_requests", -1}, {"zero_requests", "max_requests", 0},
		{"null_requests", "max_requests", nil}, {"string_requests", "max_requests", "1"}, {"fractional_concurrent", "max_concurrent", 1.5},
		{"overflow_requests", "max_requests", json.Number("9223372036854775808")}, {"zero_concurrent", "max_concurrent", 0},
		{"negative_user", "user_id", -1}, {"zero_user", "user_id", 0}, {"overflow_user", "user_id", json.Number("9223372036854775808")},
		{"zero_context", "context_tokens", 0}, {"huge_context", "context_tokens", 1000000001}, {"negative_output", "max_output_tokens", -1},
		{"numeric_budget", "budget_amount", 1.0}, {"exponent_budget", "budget_amount", "1e0"}, {"negative_budget", "budget_amount", "-1"},
		{"hash", "task_request_sha256", "not-hex"}, {"price_hash", "price_evidence_sha256", strings.Repeat("A", 64)},
		{"time_no_zone", "valid_from", "2026-10-21T00:00:00"}, {"time_reverse", "valid_until", "2000-01-01T00:00:00Z"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m, args, file := readinessFixture(t)
			raw, _ := json.Marshal(m)
			var v map[string]any
			json.Unmarshal(raw, &v)
			if c.value == nil {
				delete(v, c.field)
			} else {
				v[c.field] = c.value
			}
			raw, _ = json.Marshal(v)
			os.WriteFile(file, raw, 0600)
			r, e := captureReadiness(t, append(args, "--readiness-manifest-file", file))
			if e == nil || r["status"] == "PARTIAL" || r["execution_authorized"] != false || r["reservations_created"].(float64) != 0 || r["network_requests"].(float64) != 0 || r["database_connections"].(float64) != 0 {
				t.Fatal(r, e)
			}
		})
	}
}
func TestReviewedCozeOriginMismatchStopsBeforeScopeRequest(t *testing.T) {
	gets := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { gets++; w.WriteHeader(500) }))
	defer server.Close()
	dir := t.TempDir()
	canvas := []byte(`{"synthetic":"unit only"}`)
	sum := fmt.Sprintf("%x", sha256.Sum256(canvas))
	cf, ef := filepath.Join(dir, "canvas.json"), filepath.Join(dir, "fake-scope.env")
	os.WriteFile(cf, canvas, 0600)
	os.WriteFile(ef, []byte("BANKING_COZE_BASE_URL="+server.URL+"\nBANKING_COZE_PAT=synthetic-unit-test-only-token\n"), 0600)
	a := modelspend.Approval{WorkflowID: "123", WorkflowSHA256: sum}
	cfg := modelgateway.Config{WorkflowPolicies: map[string]string{"123": sum}}
	if reviewRuntime(context.Background(), a, cfg, ef, cf, "http://127.0.0.1:18160", outbound.TestPolicy(), "http://127.0.0.1:1") == nil || gets != 0 {
		t.Fatal("mismatched declared Coze origin reached HTTP", gets)
	}
}
func TestValidRuntimeManifestStillCannotGrantExecution(t *testing.T) {
	m, args, file := readinessFixture(t)
	writeManifest(t, file, m)
	r, e := captureReadiness(t, normalManifestArgs(args, file, m.GatewayOrigin))
	if e == nil || r["blockers"].([]any)[0] != "GATEWAY_POLICY_OR_DATABASE_CONFIGURATION_INVALID" || r["supplier_requests"].(float64) != 0 {
		t.Fatal(r, e)
	}
	// Only a nonexistent fake config path can be reached; never a real env/SQL.
	if !strings.Contains(args[len(args)-2], "must-not-load") {
		t.Fatal("fixture must not point at real configuration")
	}
}
func TestOfflineRealPriceRiskRemainsUnknownWithoutReview(t *testing.T) {
	m, args, file := readinessFixture(t)
	writeManifest(t, file, m)
	r, e := captureReadiness(t, append(args, "--readiness-manifest-file", file))
	if e == nil || r["status"] != "PARTIAL" || r["declared_price_maximum_cost"] != nil || r["declared_price_independently_verified"] != false {
		t.Fatal("unreviewed real price risk exposed as available", r)
	}
}
func TestManifestMatchesCozeOutputTokenCeiling(t *testing.T) {
	for _, output := range []int64{4096, 4097} {
		t.Run(fmt.Sprint(output), func(t *testing.T) {
			m, args, file := readinessFixture(t)
			value := func(flag string) string {
				for i, v := range args {
					if v == flag {
						return args[i+1]
					}
				}
				t.Fatal(flag)
				return ""
			}
			change := func(flag, v string) {
				for i, k := range args {
					if k == flag {
						args[i+1] = v
						return
					}
				}
				t.Fatal(flag)
			}
			var a modelspend.Approval
			raw, _ := os.ReadFile(value("--approval-file"))
			json.Unmarshal(raw, &a)
			var p modelspend.Policy
			raw, _ = os.ReadFile(value("--policy-file"))
			json.Unmarshal(raw, &p)
			p.ContextTokens = 8192
			p.MaxOutputTokens = output
			a.ContextTokens = 8192
			a.MaxOutputTokens = output
			a.PolicySHA256, _ = p.Hash()
			raw, _ = json.Marshal(p)
			os.WriteFile(value("--policy-file"), raw, 0600)
			raw, _ = json.Marshal(a)
			os.WriteFile(value("--approval-file"), raw, 0600)
			change("--approval-sha256", fmt.Sprintf("%x", sha256.Sum256(raw)))
			var plan preflightReport
			raw, _ = os.ReadFile(value("--execution-plan-file"))
			json.Unmarshal(raw, &plan)
			plan.ExecutionPlan = &a
			plan.Approval.ApprovalSHA256 = a.Fingerprint()
			raw, _ = json.Marshal(plan)
			os.WriteFile(value("--execution-plan-file"), raw, 0600)
			digest := fmt.Sprintf("%x", sha256.Sum256(raw))
			change("--execution-plan-sha256", digest)
			change("--confirm-execution-plan-sha256", digest)
			m.ContextTokens = 8192
			m.MaxOutputTokens = output
			writeManifest(t, file, m)
			r, _ := captureReadiness(t, append(args, "--readiness-manifest-file", file))
			if output == 4096 && r["status"] != "PARTIAL" || output == 4097 && r["status"] != "BLOCKED" {
				t.Fatal("Coze output contract differs", r)
			}
		})
	}
}
func TestRuntimeWorkflowFileCannotReenableUndeclaredRole(t *testing.T) {
	m, args, file := readinessFixture(t)
	writeManifest(t, file, m)
	raw, _ := json.Marshal(map[string]string{m.Workflows["supervisor"].ID: m.Workflows["supervisor"].CanvasSHA256, "999": strings.Repeat("f", 64)})
	os.WriteFile(file+".pins", raw, 0600)
	r, e := captureReadiness(t, normalManifestArgs(args, file, m.GatewayOrigin))
	if e == nil || r["blockers"].([]any)[0] != "MANIFEST_BINDING_INVALID_BEFORE_CONFIGURATION" || r["checks"].(map[string]any)["supplier_credentials"] != "NOT_INSPECTED" {
		t.Fatal(r, e)
	}
}
