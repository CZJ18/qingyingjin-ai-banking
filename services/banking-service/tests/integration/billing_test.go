//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/api"
	"github.com/qingyingjin/banking-service/internal/billing"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/httpapi"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestBillingMySQLAPI(t *testing.T) {
	h := harness(t)
	svc := billing.New(billing.NewRepository(h.conn.DB), nil)
	h.server.Close()
	h.server = httptest.NewServer(httpapi.NewWithGoals(h.auth, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, time.Second, h.svc, nil, svc))
	t.Cleanup(h.server.Close)
	doc, e := openapi3.NewLoader().LoadFromData(api.OpenAPI)
	if e != nil {
		t.Fatal(e)
	}
	doc.Servers = openapi3.Servers{{URL: h.server.URL}}
	h.contract, e = legacy.NewRouter(doc)
	if e != nil {
		t.Fatal(e)
	}
	in := billing.Input{From: "2026-01-01T00:00:00Z", To: "2026-03-01T00:00:00Z", Timezone: "UTC"}
	report := func(tool string, in billing.Input) billing.Report {
		r := h.call(t, "POST", "/api/v1/billing/"+tool, "", in, 200)
		var out billing.Report
		if json.Unmarshal(r.Data, &out) != nil {
			t.Fatal("report decode")
		}
		if e := billing.ValidateReport(out); e != nil {
			t.Fatal(e)
		}
		return out
	}
	t.Run("no records", func(t *testing.T) {
		r := report("analyze_spending", in)
		if r.TotalExpense != 0 || len(r.EvidenceTransactionIDs) != 0 || len(r.Anomalies) != 0 {
			t.Fatal(r)
		}
	})
	var account entity.BankAccount
	r := h.call(t, "POST", "/api/v1/simulation/accounts", uuid.NewString(), map[string]any{"display_name": "Billing test", "initial_balance_minor": 1000000, "confirm_simulated_funds": true}, 201)
	json.Unmarshal(r.Data, &account)
	times := []string{"2026-01-31T12:00:00Z", "2026-01-31T12:01:00Z", "2026-02-01T12:00:00Z", "2026-02-10T12:00:00Z"}
	for i, ts := range times {
		at, _ := time.Parse(time.RFC3339, ts)
		amount := int64(2000)
		cat := "餐饮"
		merchant := "模拟餐厅"
		if i == 2 {
			cat = "unknown-category"
			merchant = "未知分类商户"
		}
		if i == 3 {
			amount = 200000
			cat = "sports"
			merchant = "模拟运动商户"
		}
		h.call(t, "POST", "/api/v1/transactions/import", uuid.NewString(), map[string]any{"bank_account_id": fmt.Sprint(account.ID), "external_transaction_id": uuid.NewString(), "direction": "expense", "amount_minor": amount, "category": cat, "merchant_name": merchant, "occurred_at": at.UnixMilli(), "effect_mode": "record_only"}, 201)
	}
	h.call(t, "POST", "/api/v1/budgets", uuid.NewString(), map[string]any{"month": "2026-01", "total_minor": 100000, "enforce_reserve": true, "categories": []map[string]any{{"category": "餐饮", "allocated_minor": 100000, "essential_reserve_minor": 50000}}}, 201)
	var saved billing.Report
	t.Run("cross month classification totals and evidence", func(t *testing.T) {
		saved = report("generate_billing_report", in)
		if saved.TotalExpense != 206000 || saved.TotalIncome != 0 || saved.TransactionCount != 4 || len(saved.Monthly) != 2 || len(saved.EvidenceTransactionIDs) != 4 {
			t.Fatal(saved)
		}
		if saved.PotentialSavings.ConfirmedMinor != 0 {
			t.Fatal("unverified savings")
		}
		if e := h.conn.DB.Where("id=?", account.ID).First(&account).Error; e != nil || account.BalanceMinor != 1000000 {
			t.Fatal("analysis changed balance", account, e)
		}
	})
	t.Run("wrong category duplicate charge and high amount", func(t *testing.T) {
		kinds := map[string]bool{}
		for _, a := range saved.Anomalies {
			kinds[a.Type] = true
			if !a.ReviewRequired || a.ConfirmedLoss || len(a.Evidence) == 0 {
				t.Fatal(a)
			}
		}
		for _, name := range []string{"category_unknown", "possible_duplicate", "large_amount_review"} {
			if !kinds[name] {
				t.Fatal("missing", name)
			}
		}
	})
	t.Run("actual whole month budget remaining", func(t *testing.T) {
		part := in
		part.From = "2026-01-31T12:00:30Z"
		part.To = "2026-01-31T12:02:00Z"
		r := report("compare_budget", part)
		if r.TotalExpense != 2000 || len(r.BudgetStatus) != 1 || r.BudgetStatus[0].SpentMinor != 4000 || r.BudgetStatus[0].RemainingMinor != 96000 || len(r.BudgetStatus[0].TransactionIDs) != 2 {
			t.Fatal(r)
		}
	})
	t.Run("all six tools and pagination does not change total", func(t *testing.T) {
		p := in
		p.Limit = 1
		for _, tool := range billing.Tools {
			r := report(tool, p)
			if r.TotalExpense != 206000 || len(r.Transactions) != 1 {
				t.Fatal(r)
			}
		}
	})
	t.Run("report persisted and inaccessible to other subject", func(t *testing.T) {
		h.call(t, "GET", "/api/v1/billing/reports/"+saved.ID, "", nil, 200)
		token, _ := h.auth.IssueUser(fmt.Sprint(h.uid+1), "", time.Hour)
		status, _, e := h.raw("GET", "/api/v1/billing/reports/"+saved.ID, "", nil, token)
		if e != nil || status != 404 {
			t.Fatal(status, e)
		}
		other := in
		other.AccountID = &account.ID
		status, _, e = h.raw("POST", "/api/v1/billing/analyze_spending", "", other, token)
		if e != nil || status != 404 {
			t.Fatal(status, e)
		}
	})
	t.Run("tool credential is bound to configured user", func(t *testing.T) {
		body, _ := json.Marshal(billing.Input{From: in.From, To: in.To, Timezone: in.Timezone, AccountID: &account.ID})
		req, _ := http.NewRequest("POST", h.server.URL+"/tools/v1/billing/get_transactions", bytes.NewReader(body))
		req.Header.Set("X-Banking-Tool-Key", h.cfg.ToolToken)
		req.Header.Set("Content-Type", "application/json")
		resp, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 404 {
			t.Fatal("tool accessed another user's account", resp.StatusCode)
		}
	})
	t.Run("unconfigured natural query does not fake AI", func(t *testing.T) {
		r := h.call(t, "POST", "/api/v1/billing/query", "", map[string]any{"question": "这个月花了多少？"}, 503)
		if r.Code != "COZE_UNAVAILABLE" {
			t.Fatal(r)
		}
	})
}

func TestBillingNaturalQueryMySQLAPI(t *testing.T) {
	h := harness(t)
	var mode atomic.Value
	mode.Store("valid")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		route := billing.Route{Tool: "analyze_spending", From: "2026-01-01T00:00:00Z", To: "2026-02-01T00:00:00Z", Timezone: "UTC", Evidence: "2026年1月", MissingInformation: []string{}}
		currentMode := mode.Load().(string)
		if currentMode == "ambiguous" {
			route.From = ""
			route.To = ""
			route.MissingInformation = []string{"from", "to"}
		}
		raw, _ := json.Marshal(route)
		if currentMode == "malicious" {
			raw = []byte(`{"tool":"execute_transfer","user_id":"42"}`)
		}
		data, _ := json.Marshal(map[string]string{"output": string(raw)})
		json.NewEncoder(w).Encode(map[string]any{"code": 0, "execute_id": "456", "data": string(data)})
	}))
	defer upstream.Close()
	client := fixtureCozeClient(t, upstream.URL, "test-only-pat", time.Second)
	svc := billing.New(billing.NewRepository(h.conn.DB), &billing.QueryParser{Runner: client, WorkflowID: "123"})
	h.server.Close()
	h.server = httptest.NewServer(httpapi.NewWithGoals(h.auth, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, time.Second, h.svc, nil, svc))
	t.Cleanup(h.server.Close)
	doc, e := openapi3.NewLoader().LoadFromData(api.OpenAPI)
	if e != nil {
		t.Fatal(e)
	}
	doc.Servers = openapi3.Servers{{URL: h.server.URL}}
	h.contract, e = legacy.NewRouter(doc)
	if e != nil {
		t.Fatal(e)
	}
	input := billing.QueryInput{Question: "2026年1月消费情况", Timezone: "UTC"}
	t.Run("valid model route uses real empty database and saves report", func(t *testing.T) {
		r := h.call(t, "POST", "/api/v1/billing/query", "", input, 200)
		var out billing.QueryResult
		json.Unmarshal(r.Data, &out)
		if out.Status != "analyzed" || out.Report == nil || out.Report.TotalExpense != 0 || out.Report.UserID != h.uid || out.ExecuteID != "456" {
			t.Fatal(out)
		}
		h.call(t, "GET", "/api/v1/billing/reports/"+out.Report.ID, "", nil, 200)
	})
	t.Run("ambiguous model range does not invent analysis", func(t *testing.T) {
		mode.Store("ambiguous")
		r := h.call(t, "POST", "/api/v1/billing/query", "", input, 200)
		var out billing.QueryResult
		json.Unmarshal(r.Data, &out)
		if out.Status != "awaiting_clarification" || out.Report != nil {
			t.Fatal(out)
		}
	})
	t.Run("invalid financial task and identity cannot bypass gateway", func(t *testing.T) {
		mode.Store("malicious")
		r := h.call(t, "POST", "/api/v1/billing/query", "", input, 422)
		if r.Code != "MODEL_OUTPUT_INVALID" {
			t.Fatal(r)
		}
		var reports int64
		h.conn.DB.Model(&billing.StoredReport{}).Where("user_id=?", h.uid).Count(&reports)
		if reports != 1 {
			t.Fatal("invalid route persisted report")
		}
		var transfers int64
		h.conn.DB.Model(&entity.TransferIntent{}).Where("user_id=?", h.uid).Count(&transfers)
		if transfers != 0 {
			t.Fatal("model caused transfer")
		}
	})
}
