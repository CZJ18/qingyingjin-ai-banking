//go:build integration

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/api"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/execution"
	"github.com/qingyingjin/banking-service/internal/migration"
	"github.com/qingyingjin/banking-service/internal/platform"
	"github.com/qingyingjin/banking-service/internal/recipients"
	"github.com/qingyingjin/banking-service/internal/service"
	"io"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestRecipientHTTPContractAndOwner(t *testing.T) {
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
	if e = migration.Apply(context.Background(), c.SQL); e != nil {
		t.Fatal(e)
	}
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

	payerAccount, e := h.core.CreateAccount(context.Background(), service.Meta{UserID: uid + 1}, uuid.NewString(), service.AccountInput{DisplayName: "HTTP payer", Currency: "CNY", InitialBalanceMinor: 10000, ConfirmSimulatedFunds: true})
	if e != nil {
		t.Fatal(e)
	}

	phone := fmt.Sprintf("+86138%08d", uid%100000000)
	if e := c.DB.Model(&entity.User{}).Where("id=?", uid+1).Updates(map[string]any{"phone_number": phone, "phone_verified_at": time.Now().UnixMilli(), "phone_verification_method": "development", "display_name": "HTTP收款人"}).Error; e != nil {
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
	call := func(method, path string, body any, bearer string, status int) json.RawMessage {
		t.Helper()
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, "http://banking.test"+path, bytes.NewReader(raw))
		req.RemoteAddr = h.remote
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Idempotency-Key", uuid.NewString())
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		h.http.ServeHTTP(w, req)
		if w.Code != status {
			t.Fatalf("%s %s got %d want %d", method, path, w.Code, status)
		}
		route, params, e := router.FindRoute(req)
		if e != nil {
			t.Fatal(e)
		}
		if e = openapi3filter.ValidateResponse(context.Background(), &openapi3filter.ResponseValidationInput{RequestValidationInput: &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route}, Status: w.Code, Header: w.Header(), Body: io.NopCloser(bytes.NewReader(w.Body.Bytes()))}); e != nil {
			t.Fatal("OpenAPI", e)
		}
		var env struct {
			Data json.RawMessage `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &env)
		return env.Data
	}
	base := "/api/v1/recipients"
	agent, e := h.auth.IssueAgent(strconv.FormatInt(uid, 10), "supervisor", uuid.NewString(), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	call("GET", base+"/settings", nil, "", 401)
	call("POST", base+"/search", recipients.SearchInput{Kind: "name", Query: "HTTP收款人"}, agent, 403)
	call("PUT", base+"/settings", map[string]any{"account_id": "1", "expected_version": 1}, other, 400)
	call("PUT", base+"/settings", map[string]any{"enabled": false, "expected_version": 1}, other, 400)
	var settings recipients.Settings
	_ = json.Unmarshal(call("GET", base+"/settings", nil, other, 200), &settings)
	if settings.Enabled || settings.Version != 1 {
		t.Fatal("default receiver enabled")
	}
	_ = json.Unmarshal(call("PUT", base+"/settings", recipients.SettingsInput{Enabled: true, AccountID: payerAccount.ID, ExpectedVersion: 1}, other, 200), &settings)
	call("PUT", base+"/settings", recipients.SettingsInput{Enabled: true, AccountID: payerAccount.ID, ExpectedVersion: 1}, other, 409)
	var result recipients.SearchResult
	_ = json.Unmarshal(call("POST", base+"/search", recipients.SearchInput{Kind: "phone", Query: phone}, token, 200), &result)
	if len(result.Items) != 1 || result.Items[0].ID != uid+1 || strings.Contains(result.Items[0].PhoneMasked, phone) {
		t.Fatal("lookup missing or not masked")
	}
	call("POST", base+"/search", recipients.SearchInput{Kind: "phone", Query: "138"}, token, 400)
	var op execution.View
	// Original fixture account has zero cash; opening balance is posted through core, not modified.
	funded, e := h.core.CreateAccount(context.Background(), service.Meta{UserID: uid}, uuid.NewString(), service.AccountInput{DisplayName: "HTTP sender", Currency: "CNY", InitialBalanceMinor: 10000, ConfirmSimulatedFunds: true})
	if e != nil {
		t.Fatal(e)
	}
	p := recipients.PaymentInput{RecipientID: uid + 1, RecipientVersion: settings.Version, SourceAccountID: funded.ID, AmountMinor: 2500, Description: "HTTP备注"}
	_ = json.Unmarshal(call("POST", "/api/v1/execution/operations", execution.PrepareInput{Kind: "recipient_transfer", Parameters: marshalTest(p)}, token, 201), &op)
	if op.Operation.TargetAccountID != payerAccount.ID || op.Operation.AmountMinor != 2500 {
		t.Fatal("incorrect bound transfer")
	}
	_ = json.Unmarshal(call("POST", "/api/v1/execution/operations/"+op.Operation.ID+"/authorize", execution.AuthorizeInput{Decision: "approve", IntentDigest: op.Operation.IntentDigest, ConfirmationNonce: op.Operation.Nonce}, token, 200), &op)
	_ = json.Unmarshal(call("POST", "/api/v1/execution/operations/"+op.Operation.ID+"/execute", execution.ExecuteInput{AuthorizationID: op.Operation.AuthorizationID, IntentDigest: op.Operation.IntentDigest, ExpectedVersion: op.Operation.Version}, token, 200), &op)
	if op.Operation.Status != "succeeded" {
		t.Fatal("HTTP peer transfer failed", op.Operation.ErrorCode)
	}
	_ = json.Unmarshal(call("PUT", base+"/settings", recipients.SettingsInput{Enabled: false, AccountID: 0, ExpectedVersion: settings.Version}, other, 200), &settings)
	// Counts include both valid and invalid searches; the 21st per-user lookup is blocked.
	for i := 2; i < 20; i++ {
		call("POST", base+"/search", recipients.SearchInput{Kind: "phone", Query: phone}, token, 200)
	}
	call("POST", base+"/search", recipients.SearchInput{Kind: "phone", Query: phone}, token, 429)
	if strings.Contains(h.logs.String(), phone) {
		t.Fatal("full phone leaked into request logs")
	}
}
