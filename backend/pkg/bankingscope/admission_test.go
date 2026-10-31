package bankingscope

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestValidateInputBindsExactRawParametersWithoutModelClaim(t *testing.T) {
	parameters := `{"question":"种植建议","user_id":"spoofed"}`
	expected := sha256.Sum256([]byte(parameters))
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Method != "POST" || r.URL.Path != "/v1/workflow-admit" || r.Header.Get("Authorization") != "Bearer bgc1.opaque" {
			t.Error("wrong admission request")
		}
		var in map[string]string
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			t.Error("bad admission body")
		}
		if len(in) != 3 || in["workflow_id"] != "42" || in["input_sha256"] != hex.EncodeToString(expected[:]) || len(in["workflow_sha256"]) != 64 {
			w.WriteHeader(403)
			return
		}
		w.WriteHeader(204)
	}))
	defer server.Close()
	ctx, err := configured(t, safeCanvas)
	if err != nil {
		t.Fatal(err)
	}
	ctx.Value(key{}).(*scope).config.Origin = server.URL
	if err = ValidateInput(ctx, "42", parameters); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatal("missing actual preflight")
	}
	if _, err = Gateway(ctx); err == nil {
		t.Fatal("input admission bypassed graph verification")
	}
	if err = ValidateInput(ctx, "42", parameters+" "); err == nil {
		t.Fatal("modified exact parameters admitted")
	}
	if err = VerifyPublished(ctx, safeCanvas); err != nil {
		t.Fatal(err)
	}
	if err = ClaimCall(ctx); err != nil {
		t.Fatal("admission consumed the model call")
	}
}

func TestValidateInputDeniesRedirectNon204AndOversizedResponses(t *testing.T) {
	for _, status := range []int{200, 302, 403, 500} {
		called := 0
		target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { called++; w.WriteHeader(204) }))
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Location", target.URL)
			w.WriteHeader(status)
			io.WriteString(w, strings.Repeat("x", 4097))
		}))
		ctx, err := configured(t, safeCanvas)
		if err != nil {
			t.Fatal(err)
		}
		ctx.Value(key{}).(*scope).config.Origin = server.URL
		if err = ValidateInput(ctx, "42", `{}`); err == nil {
			t.Fatalf("status %d admitted", status)
		}
		server.Close()
		target.Close()
		if called != 0 {
			t.Fatal("redirect followed")
		}
	}
	if err := ValidateInput(context.Background(), "42", `{}`); err == nil {
		t.Fatal("unscoped admission")
	}
}

func TestValidateInputHasThreeSecondDeadline(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.Copy(io.Discard, r.Body); <-r.Context().Done() }))
	defer server.Close()
	ctx, err := configured(t, safeCanvas)
	if err != nil {
		t.Fatal(err)
	}
	ctx.Value(key{}).(*scope).config.Origin = server.URL
	started := time.Now()
	if err = ValidateInput(ctx, "42", `{}`); err == nil {
		t.Fatal("timeout admitted")
	}
	if elapsed := time.Since(started); elapsed < 2500*time.Millisecond || elapsed > 4*time.Second {
		t.Fatalf("unexpected deadline %v", elapsed)
	}
}

func TestRejectsNonLoopbackPlainHTTPGateway(t *testing.T) {
	for _, origin := range []string{"http://example.com", "http://10.0.0.1:8099", "http://127.0.0.1.evil.invalid"} {
		ctx, err := configured(t, safeCanvas)
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv("BANKING_MODEL_GATEWAY_ORIGIN", origin)
		if _, err = Attach(context.Background(), "bgc1.x", ctx.Value(key{}).(*scope).digest, "42", false); err == nil {
			t.Fatal("nonloopback plain HTTP origin accepted")
		}
	}
}
