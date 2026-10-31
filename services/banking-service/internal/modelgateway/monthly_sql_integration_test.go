//go:build integration

package modelgateway_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/httpapi"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/modelgateway"
	"github.com/qingyingjin/banking-service/internal/modelspend"
	"github.com/qingyingjin/banking-service/internal/outbound"
	"github.com/qingyingjin/banking-service/internal/platform"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/risk"
	"github.com/qingyingjin/banking-service/internal/service"
)

// Real Banking JWT -> real Banking HTTP -> real SQL monthly ledger. Only the
// provider is a synthetic protocol fixture; this is NOT a paid-model acceptance.
func TestSQLMonthlyGatewayWithRealBankingIdentity(t *testing.T) {
	_, p := fixture(t)
	p.Limit = "10"
	values, e := godotenv.Read(os.Getenv("BANKING_SPEND_TEST_ENV"))
	if e != nil {
		t.Fatal(e)
	}
	cfg, e := config.Load(func(k string) string { return values[k] })
	if e != nil {
		t.Fatal(e)
	}
	conn, e := platform.Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	ledger, e := modelspend.New(conn.DB, p)
	if e != nil {
		t.Fatal(e)
	}
	uid := time.Now().UnixMicro()
	for _, u := range []int64{uid, uid + 1} {
		if e := conn.DB.Create(&entity.User{ID: u, DisplayName: "Synthetic monthly gateway", Status: "active"}).Error; e != nil {
			t.Fatal(e)
		}
		if e := ledger.ProvisionUserLimit(t.Context(), u, "0.00120000"); e != nil {
			t.Fatal(e)
		}
	}
	auth := identity.New(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, cfg.ToolToken, "42", "")
	core := service.New(repository.New(conn.DB), risk.SimulationGateway{})
	bank := httptest.NewServer(httpapi.New(auth, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, time.Second, core))
	defer bank.Close()
	verifier, e := modelgateway.NewIdentityVerifier(bank.URL, outbound.TestPolicy())
	if e != nil {
		t.Fatal(e)
	}
	token1, e := auth.IssueUser(fmt.Sprint(uid), "", time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	token2, e := auth.IssueUser(fmt.Sprint(uid+1), "", time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	var calls atomic.Int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Banking-Authorization") != "" || strings.Contains(r.Header.Get("Authorization"), token1) {
			t.Error("bank credential leaked to supplier")
		}
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"monthly-synthetic-%d","model":"synthetic-model","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"synthetic protocol answer"}}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`, n)
	}))
	defer up.Close()
	h, e := modelgateway.New(modelgateway.Config{Policy: p, GatewayToken: access, SupplierKey: supplier, BaseURL: up.URL + "/v1", Outbound: outbound.TestPolicy(), VerifyIdentity: verifier}, ledger)
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	send := func(key, credential string) (int, string) {
		req, e := http.NewRequest(http.MethodPost, server.URL+"/v1/chat/completions", strings.NewReader(prompt))
		if e != nil {
			t.Fatal(e)
		}
		req.Header.Set("Authorization", "Bearer "+access)
		req.Header.Set("X-Model-Request-ID", key)
		req.Header.Set("X-User-ID", fmt.Sprint(uid+1)) // must be ignored
		if credential != "" {
			req.Header.Set("X-Banking-Authorization", "Bearer "+credential)
		}
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	if status, _ := send("missing", ""); status != 401 || calls.Load() != 0 {
		t.Fatal("unsigned identity dispatched", status)
	}
	if status, _ := send("forged", "forged"); status != 401 || calls.Load() != 0 {
		t.Fatal("forged identity dispatched", status)
	}
	status, first := send("shared-client-key", token1)
	if status != 200 || calls.Load() != 1 {
		t.Fatal(status, first)
	}
	status, cached := send("shared-client-key", token1)
	if status != 200 || cached != first || calls.Load() != 1 {
		t.Fatal("replay paid again", status, cached)
	}
	status, body := send("next", token1)
	if status != 429 || !strings.Contains(body, "USER_MONTHLY_BUDGET_EXCEEDED") || calls.Load() != 1 {
		t.Fatal("monthly gate failed", status, body)
	}
	if status, body := send("shared-client-key", token2); status != 200 || body == first || calls.Load() != 2 {
		t.Fatal("cross-user cache/limit", status, body)
	}
	budgetRequest, e := http.NewRequest(http.MethodGet, server.URL+"/v1/budget", nil)
	if e != nil {
		t.Fatal(e)
	}
	budgetRequest.Header.Set("Authorization", "Bearer "+access)
	budgetRequest.Header.Set("X-Banking-Authorization", "Bearer "+token1)
	budgetResponse, e := http.DefaultClient.Do(budgetRequest)
	if e != nil {
		t.Fatal(e)
	}
	var budgetView map[string]any
	if e := json.NewDecoder(budgetResponse.Body).Decode(&budgetView); e != nil {
		t.Fatal(e)
	}
	budgetResponse.Body.Close()
	if budgetResponse.StatusCode != 200 || budgetView["reserved"] != "0.00120000" || budgetView["settled"] != "0.00000000" || budgetView["timezone"] != "Asia/Shanghai" {
		t.Fatal("authenticated quota view", budgetView)
	}
	// Disable the actual bank user and revalidate before even returning a cache.
	if e := conn.DB.Model(&entity.User{}).Where("id=?", uid).Update("status", "inactive").Error; e != nil {
		t.Fatal(e)
	}
	if status, _ := send("shared-client-key", token1); status != 401 || calls.Load() != 2 {
		t.Fatal("inactive user cache access", status)
	}
	window, e := ledger.UserBudget(context.Background(), uid)
	if e != nil || window.HeldUnits != 120000 || window.UsedUnits != 0 {
		t.Fatal(window, e)
	}
	t.Logf("real Banking HTTP/JWT + SQL evidence: first_user=%d held=%d settled=%d supplier_fixture_calls=%d", uid, window.HeldUnits, window.UsedUnits, calls.Load())
}
