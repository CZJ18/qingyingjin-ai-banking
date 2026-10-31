package bankingscope

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDeepSeekScopeUsesExplicitNonThinkingJSONProtocol(t *testing.T) {
	calls := 0
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		raw, _ := io.ReadAll(r.Body)
		if r.ContentLength != int64(len(raw)) {
			t.Error("stale content length after request binding")
		}
		var body map[string]json.RawMessage
		if json.Unmarshal(raw, &body) != nil || string(body["thinking"]) != `{"type":"disabled"}` || string(body["response_format"]) != `{"type":"json_object"}` {
			t.Error("bounded DeepSeek protocol absent", string(raw))
		}
		w.Write([]byte(`{}`))
	}))
	defer s.Close()
	for _, control := range []string{"", `,"thinking":{"type":"enabled"}`, `,"reasoning_effort":"high"`} {
		ctx, err := configured(t, safeCanvas)
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("BANKING_MODEL_GATEWAY_ORIGIN", s.URL)
		t.Setenv("BANKING_MODEL_NAME", "deepseek-flash")
		ctx, err = Attach(context.Background(), "bgc1.opaque", ctx.Value(key{}).(*scope).digest, "42", false)
		if err != nil {
			t.Fatal(err)
		}
		if VerifyPublished(ctx, safeCanvas) != nil {
			t.Fatal("graph rejected")
		}
		client, err := NewClient(ctx)
		if err != nil {
			t.Fatal(err)
		}
		req, _ := http.NewRequestWithContext(ctx, "POST", s.URL+"/v1/chat/completions", strings.NewReader(`{"model":"deepseek-flash","max_tokens":512,"messages":[]`+control+`}`))
		response, err := client.Do(req)
		if control != "" {
			if err == nil {
				response.Body.Close()
				t.Error("unreviewed thinking mode admitted")
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			response.Body.Close()
		}
	}
	if calls != 1 {
		t.Fatal("unexpected dispatch count", calls)
	}
}
