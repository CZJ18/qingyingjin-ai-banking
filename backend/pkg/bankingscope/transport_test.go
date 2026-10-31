package bankingscope

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGatewayTransportOpaqueAuthAndSingleCall(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer bgc1.opaque" {
			t.Error("wrong opaque authorization")
		}
		w.Write([]byte(`{}`))
	}))
	defer server.Close()
	ctx, err := configured(t, safeCanvas)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("BANKING_MODEL_GATEWAY_ORIGIN", server.URL)
	ctx, err = Attach(context.Background(), "bgc1.opaque", ctx.Value(key{}).(*scope).digest, "42", false)
	if err != nil {
		t.Fatal(err)
	}
	if err = VerifyPublished(ctx, safeCanvas); err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		req, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/v1/chat/completions", strings.NewReader(`{"model":"reviewed-model","max_tokens":512,"messages":[]}`))
		resp, err := client.Do(req)
		if i == 0 {
			if err != nil {
				t.Fatal(err)
			}
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		} else if err == nil {
			t.Fatal("second call accepted")
		}
	}
	if calls != 1 {
		t.Fatal("unexpected call count")
	}
}

func TestGatewayTransportRejectsOtherOriginsAndUnsafeBodies(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.Write([]byte(`{}`)) }))
	defer server.Close()
	for _, body := range []string{`{"model":"wrong","max_tokens":512}`, `{"model":"reviewed-model","max_tokens":513}`, `{"model":"reviewed-model","max_tokens":512,"max_completion_tokens":99999}`, `{"model":"reviewed-model","max_tokens":512,"tools":[{}]}`, `{"model":"reviewed-model","max_tokens":512,"stream":true}`} {
		ctx, err := configured(t, safeCanvas)
		if err != nil {
			t.Fatal(err)
		}
		VerifyPublished(ctx, safeCanvas)
		ctx.Value(key{}).(*scope).config.Origin = server.URL
		client, _ := NewClient(ctx)
		req, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/v1/chat/completions", strings.NewReader(body))
		if _, err := client.Do(req); err == nil {
			t.Fatal("unsafe body accepted")
		}
	}
	if calls != 0 {
		t.Fatal("rejected bodies reached gateway")
	}
	ctx, err := configured(t, safeCanvas)
	if err != nil {
		t.Fatal(err)
	}
	VerifyPublished(ctx, safeCanvas)
	client, _ := NewClient(ctx)
	req, _ := http.NewRequestWithContext(ctx, "POST", "http://example.invalid/v1/chat/completions", strings.NewReader(`{}`))
	if _, err := client.Do(req); err == nil {
		t.Fatal("other origin accepted")
	}
}
