package modelgateway

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRequestAllowlistUsesExactFieldNamesAtEveryEnvelope(t *testing.T) {
	b := &memoryBudget{}
	h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) { t.Error("noncanonical field dispatched") }, b)
	for _, body := range []string{strings.Replace(validBody, `"model":`, `"MODEL":`, 1), strings.Replace(validBody, `"max_tokens":`, `"Max_Tokens":`, 1), strings.Replace(validBody, `"role":`, `"ROLE":`, 1), strings.Replace(validBody, `"content":`, `"Content":`, 1), strings.Replace(validBody, `"max_tokens":8`, `"max_tokens":8,"Response_Format":{"type":"text"}`, 1), strings.Replace(validBody, `"max_tokens":8`, `"max_tokens":8,"response_format":{"TYPE":"text"}`, 1)} {
		if w := call(h, body, "", "Bearer "+token); w.Code != 400 {
			t.Errorf("status=%d for %s", w.Code, body)
		}
	}
	if len(b.attempts) != 0 {
		t.Fatal("invalid request reserved")
	}
}
func TestModelMetadataBindsDeploymentToPolicy(t *testing.T) {
	h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) { t.Error("models called supplier") }, &memoryBudget{})
	r := httptest.NewRequest("GET", "/v1/models", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var body struct {
		Enforcement struct {
			Mode    string `json:"mode"`
			Project string `json:"project_id"`
			Policy  string `json:"policy_sha256"`
			Max     int64  `json:"max_output_tokens"`
		} `json:"model_spend_enforcement"`
	}
	if json.Unmarshal(w.Body.Bytes(), &body) != nil || body.Enforcement.Mode != "conservative_project_lifetime" || body.Enforcement.Project != "project" || len(body.Enforcement.Policy) != 64 || body.Enforcement.Max != 8 {
		t.Fatalf("missing deployment enforcement metadata: %s", w.Body.String())
	}
}
func TestExpiredPolicyModelsAndDispatchFailClosed(t *testing.T) {
	b := &memoryBudget{}
	h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) { t.Error("expired policy dispatched") }, b)
	h.(*gateway).cfg.Policy.ValidUntil = time.Now().Add(-time.Second).UTC().Format(time.RFC3339)
	r := httptest.NewRequest("GET", "/v1/models", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 503 {
		t.Fatal(w.Code)
	}
	if w = call(h, validBody, "", "Bearer "+token); w.Code != 503 || len(b.attempts) != 0 {
		t.Fatal("expired policy reserved")
	}
}
func TestMalformedCompletionChoicesStayUnknown(t *testing.T) {
	for _, reply := range []string{strings.Replace(validResponse, `"message":{"role":"assistant","content":"hello"}`, `"message":null`, 1), strings.Replace(validResponse, `"finish_reason":"stop"`, `"finish_reason":null`, 1), strings.Replace(validResponse, `"message":{"role":"assistant","content":"hello"}`, `"unexpected":"x"`, 1), strings.Replace(validResponse, `"index":0`, `"index":4`, 1)} {
		b := &memoryBudget{}
		h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, reply)
		}, b)
		if w := call(h, validBody, "", "Bearer "+token); w.Code != 502 || b.unknown != 1 {
			t.Fatal("malformed choice accepted")
		}
	}
}

func TestPinnedSDKToolCallHistoryIndexAndForbiddenChoiceAreAccepted(t *testing.T) {
	for _, body := range []string{strings.Replace(validBody, `"max_tokens":8`, `"max_tokens":8,"tool_choice":"none"`, 1), `{"model":"synthetic-model","messages":[{"role":"assistant","content":"","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{}"}}]},{"role":"tool","content":"ok","tool_call_id":"call_1"}],"max_tokens":8}`} {
		b := &memoryBudget{}
		h, _ := setup(t, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, validResponse)
		}, b)
		if w := call(h, body, "", "Bearer "+token); w.Code != 200 {
			t.Errorf("SDK field incompatibility status=%d body=%s", w.Code, w.Body.String())
		}
	}
}
