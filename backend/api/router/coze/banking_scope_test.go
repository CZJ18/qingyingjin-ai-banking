package coze

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app/server"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

func TestRegisteredBankingModelScopeIsClosedByDefaultAndContainsNoSecrets(t *testing.T) {
	h := server.New()
	registerBankingModelScope(h.Group("/"))
	t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", "")
	w := ut.PerformRequest(h.Engine, "GET", "/v1/banking/model-scope", nil)
	if w.Code != 503 {
		t.Fatalf("missing config returned %d", w.Code)
	}
	t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", `{"42":"`+strings.Repeat("a", 64)+`"}`)
	t.Setenv("BANKING_MODEL_GATEWAY_ORIGIN", "http://127.0.0.1:8099")
	t.Setenv("BANKING_MODEL_NAME", "reviewed-model")
	t.Setenv("BANKING_MODEL_MAX_OUTPUT_TOKENS", "512")
	t.Setenv("OPENAI_API_KEY", "never-export-provider-secret")
	w = ut.PerformRequest(h.Engine, "GET", "/v1/banking/model-scope", nil)
	if w.Code != 200 {
		t.Fatalf("configured scope returned %d", w.Code)
	}
	var view map[string]any
	if json.Unmarshal(w.Body.Bytes(), &view) != nil || len(view) != 5 || view["mode"] != "banking-task-scope-v1" || view["gateway_origin"] != "http://127.0.0.1:8099" || view["model"] != "reviewed-model" || view["max_output_tokens"] != float64(512) {
		t.Fatalf("wrong config view %s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "never-export-provider-secret") || strings.Contains(w.Body.String(), "bgc1.") {
		t.Fatal("credential exposed")
	}
}
