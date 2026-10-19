//go:build integration

package modelgateway_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/modelgateway"
	"github.com/qingyingjin/banking-service/internal/modelspend"
	"github.com/qingyingjin/banking-service/internal/outbound"
	"github.com/qingyingjin/banking-service/internal/spendconfig"
)

func fixture(t *testing.T) (*modelspend.Service, modelspend.Policy) {
	t.Helper()
	env := os.Getenv("BANKING_SPEND_TEST_ENV")
	if env == "" {
		t.Skip("explicit new isolated fixture required")
	}
	if os.Getenv("BANKING_TEST_ENVIRONMENT") != "isolated" || !strings.HasSuffix(os.Getenv("BANKING_SPEND_TEST_EXPECT_DATABASE"), "_test") {
		t.Fatal("test fixture guard")
	}
	p := modelspend.Policy{ProjectID: "http-" + uuid.NewString(), Currency: "USD", Model: "synthetic-model", PriceVersion: "synthetic-v1", PriceSource: "synthetic:test-only", PriceEvidenceSHA256: strings.Repeat("a", 64), ValidFrom: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), ValidUntil: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), Limit: "0.00120100", InputPrice: "1", CacheHitPrice: "0.1", OutputPrice: "2", ContextTokens: 1000, MaxOutputTokens: 100}
	raw, _ := json.Marshal(p)
	path := filepath.Join(t.TempDir(), "policy.json")
	if e := os.WriteFile(path, raw, 0600); e != nil {
		t.Fatal(e)
	}
	c, e := spendconfig.Load(env, path, os.Getenv("BANKING_SPEND_TEST_EXPECT_DATABASE"), true)
	if e != nil {
		t.Fatal(e)
	}
	host := c.Database.MySQLHost
	if c.Database.DatabaseDriver == "postgres" {
		host = c.Database.PostgresHost
	}
	if host != "127.0.0.1" && host != "localhost" && host != "::1" {
		t.Fatal("test target must be loopback")
	}
	conn, e := spendconfig.Open(context.Background(), c)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(conn.Close)
	s, e := modelspend.New(conn.DB, p)
	if e != nil {
		t.Fatal(e)
	}
	return s, p
}

const access = "synthetic-gateway-token-local-only-1234567890"
const supplier = "synthetic-supplier-token-local-only-1234567890"

func request(t *testing.T, base, key, token, body string) (int, []byte) {
	t.Helper()
	r, e := http.NewRequest(http.MethodPost, base+"/v1/chat/completions", strings.NewReader(body))
	if e != nil {
		t.Fatal(e)
	}
	r.Header.Set("Authorization", "Bearer "+token)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("X-Model-Request-ID", key)
	resp, e := http.DefaultClient.Do(r)
	if e != nil {
		t.Fatal(e)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, raw
}

const prompt = `{"model":"synthetic-model","messages":[{"role":"user","content":"synthetic balance query"}],"max_tokens":100}`

func TestSQLGatewayAdmissionCacheBillAndNoExtraPaidDispatch(t *testing.T) {
	s, p := fixture(t)
	var calls atomic.Int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+supplier {
			t.Error("supplier credential binding")
		}
		n := calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"synthetic-response-%d","model":"synthetic-model","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"synthetic answer"}}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}`, n)
	}))
	defer up.Close()
	h, e := modelgateway.New(modelgateway.Config{Policy: p, GatewayToken: access, SupplierKey: supplier, BaseURL: up.URL + "/v1", Timeout: time.Second, Outbound: outbound.TestPolicy()}, s)
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	if status, _ := request(t, server.URL, "untrusted", "wrong", prompt); status != 401 || calls.Load() != 0 {
		t.Fatal("unauthorized request reached provider")
	}
	status, first := request(t, server.URL, "one", access, prompt)
	if status != 200 || calls.Load() != 1 {
		t.Fatal(status, string(first))
	}
	status, cached := request(t, server.URL, "one", access, prompt)
	if status != 200 || string(cached) != string(first) || calls.Load() != 1 {
		t.Fatal("cache re-dispatched")
	}
	if status, _ := request(t, server.URL, "one", access, strings.Replace(prompt, `"max_tokens":100`, `"max_tokens":99`, 1)); status != 409 || calls.Load() != 1 {
		t.Fatal("changed parameters reused admission")
	}
	if status, _ := request(t, server.URL, "two", access, prompt); status != 429 || calls.Load() != 1 {
		t.Fatal("budget denial dispatched paid request")
	}
	secret := strings.Repeat("z", 40)
	admin, e := modelspend.NewReconciler(s, secret, "synthetic-operator")
	if e != nil {
		t.Fatal(e)
	}
	pending, e := admin.Pending(context.Background(), secret)
	if e != nil || len(pending) != 1 || pending[0].EstimatedUnits == nil || pending[0].ActualUnits != nil || pending[0].ResponseBody != nil {
		t.Fatal("pending privacy/state", e)
	}
	in := modelspend.Settlement{AttemptID: pending[0].ID, ProjectID: p.ProjectID, Currency: p.Currency, BillID: "synthetic-bill", LineID: "1", ProviderID: "synthetic-response-1", ActualCost: "0.00000050", EvidenceDigest: fmt.Sprintf("%x", sha256.Sum256([]byte("synthetic bill evidence")))}
	if _, e = admin.Reconcile(context.Background(), secret, "reviewed-one", in); e != nil {
		t.Fatal(e)
	}
	if out, e := admin.Reconcile(context.Background(), secret, "reviewed-one", in); e != nil || !out.Replay {
		t.Fatal("replayed bill", out, e)
	}
	if status, raw := request(t, server.URL, "two", access, prompt); status != 200 || calls.Load() != 2 {
		t.Fatal("review did not release documented difference", status, string(raw))
	}
	if status, _ := request(t, server.URL, "three", access, prompt); status != 429 || calls.Load() != 2 {
		t.Fatal("second budget boundary")
	}
}
func TestSQLGatewayUnknownSurvivesNewServiceAndNeverReposts(t *testing.T) {
	s, p := fixture(t)
	var calls atomic.Int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"synthetic-unknown","model":"synthetic-model","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"unknown usage"}}]}`)
	}))
	defer up.Close()
	h, e := modelgateway.New(modelgateway.Config{Policy: p, GatewayToken: access, SupplierKey: supplier, BaseURL: up.URL + "/v1", Timeout: time.Second, Outbound: outbound.TestPolicy()}, s)
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	if status, _ := request(t, server.URL, "unknown", access, prompt); status != 502 {
		t.Fatal(status)
	}
	if status, _ := request(t, server.URL, "unknown", access, prompt); status != 409 || calls.Load() != 1 {
		t.Fatal("unknown reposted")
	}
	admin, _ := modelspend.NewReconciler(s, strings.Repeat("z", 40), "synthetic-operator")
	rows, e := admin.Pending(context.Background(), strings.Repeat("z", 40))
	if e != nil || len(rows) != 1 || rows[0].Status != "unknown" || rows[0].ActualUnits != nil || rows[0].ReservedUnits != 120000 {
		t.Fatal(rows, e)
	}
	if status, _ := request(t, server.URL, "fresh", access, prompt); status != 429 || calls.Load() != 1 {
		t.Fatal("unknown hold disappeared")
	}
}
func TestSQLGatewayConcurrentRequestsReserveOneProjectAtomically(t *testing.T) {
	s, p := fixture(t)
	var calls atomic.Int64
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"id":"synthetic-concurrent","model":"synthetic-model","choices":[{"index":0,"finish_reason":"stop","message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)
	}))
	defer up.Close()
	h, e := modelgateway.New(modelgateway.Config{Policy: p, GatewayToken: access, SupplierKey: supplier, BaseURL: up.URL + "/v1", Timeout: time.Second, Outbound: outbound.TestPolicy()}, s)
	if e != nil {
		t.Fatal(e)
	}
	server := httptest.NewServer(h)
	defer server.Close()
	start := make(chan struct{})
	var wg sync.WaitGroup
	statuses := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			status, _ := request(t, server.URL, fmt.Sprintf("parallel-%d", i), access, prompt)
			statuses <- status
		}(i)
	}
	close(start)
	wg.Wait()
	close(statuses)
	ok, denied := 0, 0
	for status := range statuses {
		if status == 200 {
			ok++
		} else if status == 429 {
			denied++
		} else {
			t.Fatal(status)
		}
	}
	if ok != 1 || denied != 1 || calls.Load() != 1 {
		t.Fatal("project quota race", ok, denied, calls.Load())
	}
}
