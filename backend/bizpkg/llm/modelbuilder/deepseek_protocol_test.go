package modelbuilder

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudwego/eino/schema"
)

func TestBankingDeepSeekSDKUsesBoundedJSONAndNoThinking(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		var body struct {
			Model    string `json:"model"`
			Max      int    `json:"max_tokens"`
			Thinking struct {
				Type string `json:"type"`
			} `json:"thinking"`
			Format struct {
				Type string `json:"type"`
			} `json:"response_format"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.Model != "deepseek-flash" || body.Max != 512 || body.Thinking.Type != "disabled" || body.Format.Type != "json_object" {
			t.Error("SDK protocol not constrained", body)
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"SYNTHETIC-SDK-ONLY","choices":[{"index":0,"message":{"role":"assistant","content":"{}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer s.Close()
	ctx := bankingContext(t, s.URL, "deepseek-flash")
	model, err := newBankingGatewayModel(ctx)
	if err != nil {
		t.Fatal(err)
	}
	result, err := model.Generate(ctx, []*schema.Message{schema.UserMessage("Return JSON")})
	if err != nil || result.Content != "{}" || calls != 1 {
		t.Fatal(result, err, calls)
	}
}
