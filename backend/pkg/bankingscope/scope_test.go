package bankingscope

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

const safeParams = `[{"name":"maxTokens","input":{"type":"integer","value":{"type":"literal","content":"512"}}}]`
const safeCanvas = `{"nodes":[{"id":"100001","type":"1","data":{"inputs":{}}},{"id":"llm","type":"3","data":{"inputs":{"llmParam":` + safeParams + `}}},{"id":"900001","type":"2","data":{"inputs":{}}}],"edges":[{"sourceNodeID":"100001","targetNodeID":"llm"},{"sourceNodeID":"llm","targetNodeID":"900001"}]}`

func configured(t *testing.T, canvas string) (context.Context, error) {
	t.Helper()
	sum := sha256.Sum256([]byte(canvas))
	digest := hex.EncodeToString(sum[:])
	t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", `{"42":"`+digest+`"}`)
	t.Setenv("BANKING_MODEL_GATEWAY_ORIGIN", "http://127.0.0.1:8099")
	t.Setenv("BANKING_MODEL_NAME", "reviewed-model")
	t.Setenv("BANKING_MODEL_MAX_OUTPUT_TOKENS", "512")
	return Attach(context.Background(), "bgc1.opaque", digest, "42", false)
}

func TestScopeRequiresPublishedGraphProof(t *testing.T) {
	ctx, err := configured(t, safeCanvas)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Gateway(ctx); err == nil {
		t.Fatal("unverified graph may not build a model")
	}
	if err := VerifyPublished(ctx, safeCanvas); err != nil {
		t.Fatal(err)
	}
	cfg, err := Gateway(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Capability != "bgc1.opaque" || cfg.MaxOutputTokens != 512 {
		t.Fatal("opaque scope lost")
	}
	if strings.Contains(strings.TrimSpace(strings.ReplaceAll(cfg.Origin, "http://", "")), "@") {
		t.Fatal("invalid origin")
	}
}

func TestRejectsUnsafeOrChangedPublishedGraphs(t *testing.T) {
	for _, canvas := range []string{
		strings.Replace(safeCanvas, `"llmParam":`+safeParams, `"llmParam":`+safeParams+`,"fcParam":{}`, 1),
		strings.Replace(safeCanvas, `"llmParam":`+safeParams, `"llmParam":`+safeParams+`,"settingOnError":{"retryTimes":1}`, 1),
		strings.Replace(safeCanvas, `"llmParam":`+safeParams, `"llmParam":[{"name":"enableChatHistory","input":{"value":{"content":true}}}]`, 1),
		strings.Replace(safeCanvas, `"llmParam":`+safeParams, `"llmParam":`+safeParams+`,"inputParameters":[{"input":{"type":"string","assistType":2}}]`, 1),
		strings.Replace(safeCanvas, `"type":"3"`, `"type":"4"`, 1),
		strings.Replace(safeCanvas, `"id":"llm"`, `"id":"llm","blocks":[{}]`, 1),
		strings.Replace(safeCanvas, `"targetNodeID":"900001"`, `"targetNodeID":"100001"`, 1),
	} {
		ctx, err := configured(t, canvas)
		if err != nil {
			t.Fatal(err)
		}
		if VerifyPublished(ctx, canvas) == nil {
			t.Fatal("unsafe graph accepted")
		}
	}
	ctx, err := configured(t, safeCanvas)
	if err != nil {
		t.Fatal(err)
	}
	if VerifyPublished(ctx, safeCanvas+" ") == nil {
		t.Fatal("changed exact bytes accepted")
	}
}

func TestPinnedCanvasOutputLimitNarrowsGlobalScope(t *testing.T) {
	for _, limit := range []string{"512", "1024"} {
		t.Run(limit, func(t *testing.T) {
			canvas := strings.Replace(safeCanvas, `"content":"512"`, `"content":"`+limit+`"`, 1)
			_, err := configured(t, canvas)
			if err != nil {
				t.Fatal(err)
			}
			t.Setenv("BANKING_MODEL_MAX_OUTPUT_TOKENS", "1024")
			sum := sha256.Sum256([]byte(canvas))
			ctx, err := Attach(context.Background(), "bgc1.offline-only", hex.EncodeToString(sum[:]), "42", false)
			if err != nil {
				t.Fatal(err)
			}
			if err = VerifyPublished(ctx, canvas); err != nil {
				t.Fatal(err)
			}
			cfg, err := Gateway(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if (limit == "512" && cfg.MaxOutputTokens != 512) || (limit == "1024" && cfg.MaxOutputTokens != 1024) {
				t.Fatalf("pinned Canvas %s was replaced by global scope limit %d", limit, cfg.MaxOutputTokens)
			}
		})
	}
}

func TestPinnedCanvasRequiresExplicitLiteralOutputLimit(t *testing.T) {
	for name, params := range map[string]string{
		"missing":     `[]`,
		"zero":        strings.Replace(safeParams, `"512"`, `"0"`, 1),
		"negative":    strings.Replace(safeParams, `"512"`, `"-1"`, 1),
		"above_scope": strings.Replace(safeParams, `"512"`, `"513"`, 1),
		"reference":   strings.Replace(safeParams, `"literal"`, `"ref"`, 1),
		"wrong_type":  strings.Replace(safeParams, `"integer"`, `"string"`, 1),
		"fraction":    strings.Replace(safeParams, `"512"`, `512.5`, 1),
		"duplicate":   `[` + strings.Trim(safeParams, "[]") + `,` + strings.Trim(safeParams, "[]") + `]`,
	} {
		t.Run(name, func(t *testing.T) {
			canvas := strings.Replace(safeCanvas, safeParams, params, 1)
			ctx, err := configured(t, canvas)
			if err != nil {
				t.Fatal(err)
			}
			if err = VerifyPublished(ctx, canvas); err == nil {
				t.Fatal("invalid Canvas output limit accepted")
			}
			if _, err = Gateway(ctx); err == nil {
				t.Fatal("invalid Canvas granted model access")
			}
		})
	}
}

func TestRejectsAsyncUnpinnedAndUnsafeOrigin(t *testing.T) {
	ctx, err := configured(t, safeCanvas)
	if err != nil {
		t.Fatal(err)
	}
	if !Present(ctx) || Present(context.Background()) {
		t.Fatal("scope marker")
	}
	for _, origin := range []string{"https://user:pass@example.com", "https://example.com/path", "https://example.com?x=1", "ftp://example.com"} {
		t.Setenv("BANKING_MODEL_GATEWAY_ORIGIN", origin)
		if _, err := Attach(context.Background(), "bgc1.x", strings.Repeat("a", 64), "42", false); err == nil {
			t.Fatal("unsafe origin accepted")
		}
	}
	t.Setenv("BANKING_MODEL_GATEWAY_ORIGIN", "http://127.0.0.1:8099")
	if _, err := Attach(context.Background(), "bgc1.x", strings.Repeat("a", 64), "42", true); err == nil {
		t.Fatal("async accepted")
	}
	t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", "{}")
	if _, err := Attach(context.Background(), "bgc1.x", strings.Repeat("a", 64), "42", false); err == nil {
		t.Fatal("unpinned workflow accepted")
	}
}
