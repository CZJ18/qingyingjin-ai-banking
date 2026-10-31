package modelbuilder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudwego/eino/schema"
	"github.com/coze-dev/coze-studio/backend/api/model/admin/config"
	"github.com/coze-dev/coze-studio/backend/pkg/bankingscope"
)

func bankingContext(t *testing.T, origin string, models ...string) context.Context {
	t.Helper()
	canvas := `{"nodes":[{"id":"s","type":"1"},{"id":"m","type":"3","data":{"inputs":{"llmParam":[{"name":"maxTokens","input":{"type":"integer","value":{"type":"literal","content":"512"}}}]}}},{"id":"e","type":"2"}],"edges":[{"sourceNodeID":"s","targetNodeID":"m"},{"sourceNodeID":"m","targetNodeID":"e"}]}`
	sum := sha256.Sum256([]byte(canvas))
	digest := hex.EncodeToString(sum[:])
	t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", `{"42":"`+digest+`"}`)
	t.Setenv("BANKING_MODEL_GATEWAY_ORIGIN", origin)
	model := "reviewed-model"
	if len(models) == 1 {
		model = models[0]
	}
	t.Setenv("BANKING_MODEL_NAME", model)
	t.Setenv("BANKING_MODEL_MAX_OUTPUT_TOKENS", "512")
	ctx, err := bankingscope.Attach(context.Background(), "bgc1.opaque", digest, "42", false)
	if err != nil {
		t.Fatal(err)
	}
	if err = bankingscope.VerifyPublished(ctx, canvas); err != nil {
		t.Fatal(err)
	}
	return ctx
}

func TestBankingModelUsesOnlyGateway(t *testing.T) {
	calls := 0
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer bgc1.opaque" {
			t.Error("wrong endpoint or auth")
		}
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"fixture","choices":[{"index":0,"message":{"role":"assistant","content":"bounded result"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`)
	}))
	defer fixture.Close()
	ctx := bankingContext(t, fixture.URL)
	model, err := newBankingGatewayModel(ctx)
	if err != nil {
		t.Fatal(err)
	}
	out, err := model.Generate(ctx, []*schema.Message{schema.UserMessage("hello")})
	if err != nil {
		t.Fatal(err)
	}
	if out.Content != "bounded result" || calls != 1 {
		t.Fatal("model request failed")
	}
	if _, err = model.Generate(ctx, []*schema.Message{schema.UserMessage("hello")}); err == nil {
		t.Fatal("retry allowed")
	}
}

func TestBankingDeniesEveryDirectProvider(t *testing.T) {
	ctx := bankingContext(t, "http://127.0.0.1:8099")
	for class := range modelClass2NewModelBuilder {
		builder, err := NewModelBuilder(class, &config.Model{Connection: &config.Connection{BaseConnInfo: &config.BaseConnectionInfo{}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = builder.Build(ctx, nil); err == nil {
			t.Fatalf("direct provider %v accepted Banking scope", class)
		}
	}
}

func TestDedicatedBankingRuntimeDeniesUnscopedDirectProviders(t *testing.T) {
	for _, mode := range []string{`{"42":"pinned"}`, "invalid", " "} {
		t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", mode)
		for class, makeBuilder := range modelClass2NewModelBuilder {
			err := func() (err error) {
				defer func() {
					if recover() != nil {
						err = fmt.Errorf("provider touched direct config")
					}
				}()
				_, err = makeBuilder(nil).Build(context.Background(), nil)
				return
			}()
			if !errors.Is(err, bankingscope.ErrDenied) {
				t.Fatalf("provider %v escaped dedicated-mode guard: %v", class, err)
			}
		}
	}
}

func TestGeneralRuntimeKeepsDirectOpenAIModel(t *testing.T) {
	t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", "")
	calls := 0
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer fixture-only-key" {
			t.Error("general runtime provider config changed")
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"choices":[{"index":0,"message":{"role":"assistant","content":"ordinary response"},"finish_reason":"stop"}]}`)
	}))
	defer fixture.Close()
	builder := newOpenaiModelBuilder(&config.Model{Connection: &config.Connection{BaseConnInfo: &config.BaseConnectionInfo{APIKey: "fixture-only-key", Model: "fixture-model", BaseURL: fixture.URL + "/v1"}}})
	m, err := builder.Build(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := m.Generate(context.Background(), []*schema.Message{schema.UserMessage("fixture prompt")})
	if err != nil || out.Content != "ordinary response" || calls != 1 {
		t.Fatalf("general runtime failed: %v", err)
	}
}
