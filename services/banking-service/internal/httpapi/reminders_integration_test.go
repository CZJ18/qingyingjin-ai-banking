//go:build integration

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/api"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/platform"
	"github.com/qingyingjin/banking-service/internal/reminders"
	"io"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRenewalsHTTPContractAndOwner(t *testing.T) {
	path := os.Getenv("BANKING_AUTH_TEST_ENV")
	if path == "" {
		t.Skip("isolated BANKING_AUTH_TEST_ENV required")
	}
	env, e := godotenv.Read(path)
	if e != nil {
		t.Fatal(e)
	}
	cfg, e := config.Load(func(k string) string { return env[k] })
	if e != nil {
		t.Fatal(e)
	}
	name := cfg.MySQLDatabase
	if cfg.DatabaseDriver == "postgres" {
		name = cfg.PostgresDatabase
	}
	if !strings.HasSuffix(name, "_test") {
		t.Fatal("isolated DB required")
	}
	c, e := platform.Open(cfg)
	if e != nil {
		t.Fatal("database unavailable")
	}
	defer c.Close()
	h := newLivePhoneAuth(t, false)
	uid := time.Now().UnixNano()%1000000000000000 + 1000000000000
	for _, id := range []int64{uid, uid + 1} {
		if e = c.DB.Create(&entity.User{ID: id, Status: "active", DisplayName: "Renewal HTTP fixture"}).Error; e != nil {
			t.Fatal(e)
		}
	}
	token, _ := h.auth.IssueUser(strconv.FormatInt(uid, 10), "", time.Hour)
	other, _ := h.auth.IssueUser(strconv.FormatInt(uid+1, 10), "", time.Hour)
	a := entity.BankAccount{Base: entity.Base{UserID: uid}, DisplayName: "Reminder HTTP", AccountType: "checking", MaskedAccountNumber: "SIM ****1234", Currency: "CNY", Status: "active", Version: 1, IsSimulated: true}
	if e = c.DB.Create(&a).Error; e != nil {
		t.Fatal(e)
	}
	sub := entity.Subscription{Base: entity.Base{UserID: uid}, BankAccountID: a.ID, Fingerprint: uuid.NewString(), MerchantName: "HTTP service", Category: "subscription", BillingInterval: "monthly", AmountMinor: 2000, Currency: "CNY", NextBillingAt: time.Now().Add(24 * time.Hour).UnixMilli(), ConfidenceBPS: 10000, Status: "active", Version: 1, IsSimulated: true}
	if e = c.DB.Create(&sub).Error; e != nil {
		t.Fatal(e)
	}
	doc, e := openapi3.NewLoader().LoadFromData(api.OpenAPI)
	if e != nil {
		t.Fatal(e)
	}
	doc.Servers = nil
	router, e := legacy.NewRouter(doc)
	if e != nil {
		t.Fatal(e)
	}
	call := func(method, path string, body any, bearer string, status int) reminders.View {
		t.Helper()
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, "http://banking.test"+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		h.http.ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("%s %s got %d want %d: %s", method, path, w.Code, status, w.Body.String())
		}
		route, params, e := router.FindRoute(req)
		if e != nil {
			t.Fatal(e)
		}
		if e = openapi3filter.ValidateResponse(context.Background(), &openapi3filter.ResponseValidationInput{RequestValidationInput: &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route}, Status: w.Code, Header: w.Header(), Body: io.NopCloser(bytes.NewReader(w.Body.Bytes()))}); e != nil {
			t.Fatal("OpenAPI", e)
		}
		var env struct {
			Data reminders.View `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		return env.Data
	}
	base := "/api/v1/notifications/renewals"
	agent, e := h.auth.IssueAgent(strconv.FormatInt(uid, 10), "supervisor", uuid.NewString(), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	call("GET", base, nil, agent, 403)
	call("PUT", base+"/settings", map[string]any{"enabled": true, "lead_days": 3, "expected_version": 1}, agent, 403)
	call("GET", base, nil, "", 401)
	v := call("GET", base, nil, token, 200)
	if len(v.Items) != 0 || v.LastCheckedAt != 0 {
		t.Fatal("GET generated a notice")
	}
	v = call("PUT", base+"/settings", map[string]any{"enabled": true, "lead_days": 3, "expected_version": 1}, token, 200)
	if len(v.Items) != 1 {
		t.Fatal(v)
	}
	n := v.Items[0]
	call("PUT", base+"/settings", map[string]any{"enabled": true, "lead_days": 3, "expected_version": 1}, token, 409)
	call("PUT", base+"/settings", map[string]any{"enabled": true, "lead_days": 31, "expected_version": 2}, token, 400)
	call("PUT", base+"/settings", map[string]any{"lead_days": 3, "expected_version": 2}, token, 400)
	call("PUT", base+"/settings", map[string]any{"enabled": true, "lead_days": 3, "expected_version": 2, "user_id": "1"}, token, 400)
	call("POST", base+"/"+n.ID+"/read", map[string]any{"expected_version": n.Version}, other, 404)
	for i := 0; i < 2; i++ {
		v = call("POST", base+"/"+n.ID+"/read", map[string]any{"expected_version": n.Version}, token, 200)
		if v.UnreadCount != 0 {
			t.Fatal(v)
		}
	}
	call("POST", base+"/bad-id/read", map[string]any{"expected_version": 1}, token, 400)
}
