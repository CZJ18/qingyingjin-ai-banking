//go:build integration

package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/api"
	"github.com/qingyingjin/banking-service/internal/cards"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/execution"
	"github.com/qingyingjin/banking-service/internal/platform"
)

func TestCardsHTTPContractAndOwner(t *testing.T) {
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
	dbName := cfg.MySQLDatabase
	if cfg.DatabaseDriver == "postgres" {
		dbName = cfg.PostgresDatabase
	}
	if !strings.HasSuffix(dbName, "_test") {
		t.Fatal("isolated test DB required")
	}
	conn, e := platform.Open(cfg)
	if e != nil {
		t.Fatal("database unavailable")
	}
	defer conn.Close()
	var version int
	if e = conn.SQL.QueryRow("SELECT MAX(version) FROM banking_schema_migrations").Scan(&version); e != nil || version < 14 {
		t.Fatal("apply migration 014 to isolated database first")
	}
	h := newLivePhoneAuth(t, false)
	uid := time.Now().UnixNano()%1000000000000000 + 1000000000000
	for _, id := range []int64{uid, uid + 1} {
		if e = conn.DB.Create(&entity.User{ID: id, DisplayName: "Card HTTP fixture", Status: "active"}).Error; e != nil {
			t.Fatal(e)
		}
	}
	account := entity.BankAccount{Base: entity.Base{UserID: uid}, DisplayName: "HTTP fixture account", AccountType: "checking", MaskedAccountNumber: "SIM ****4567", Status: "active", Currency: "CNY", Version: 1, IsSimulated: true}
	if e = conn.DB.Create(&account).Error; e != nil {
		t.Fatal(e)
	}
	token, e := h.auth.IssueUser(strconv.FormatInt(uid, 10), "", time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	other, e := h.auth.IssueUser(strconv.FormatInt(uid+1, 10), "", time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	doc, e := openapi3.NewLoader().LoadFromData(api.OpenAPI)
	if e != nil {
		t.Fatal(e)
	}
	doc.Servers=nil // httptest uses its own origin; validate paths and bodies, not the deployment hostname.
	router, e := legacy.NewRouter(doc)
	if e != nil {
		t.Fatal(e)
	}
	request := func(method, path string, body any, bearer string, key string) *httptest.ResponseRecorder {
		t.Helper()
		raw, _ := json.Marshal(body)
		req := httptest.NewRequest(method, "http://banking.test:8088"+path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		if bearer != "" {
			req.Header.Set("Authorization", "Bearer "+bearer)
		}
		if key != "" {
			req.Header.Set("Idempotency-Key", key)
		}
		w := httptest.NewRecorder()
		h.http.ServeHTTP(w, req)
		route, params, e := router.FindRoute(req)
		if e != nil {
			t.Fatal(e)
		}
		e = openapi3filter.ValidateResponse(context.Background(), &openapi3filter.ResponseValidationInput{RequestValidationInput: &openapi3filter.RequestValidationInput{Request: req, PathParams: params, Route: route}, Status: w.Code, Header: w.Header(), Body: io.NopCloser(bytes.NewReader(w.Body.Bytes()))})
		if e != nil {
			t.Fatal("OpenAPI response mismatch", e)
		}
		return w
	}
	decode := func(w *httptest.ResponseRecorder, status int) execution.View {
		t.Helper()
		if w.Code != status {
			t.Fatalf("HTTP %d expected %d: %s", w.Code, status, w.Body.String())
		}
		var envelope struct {
			Data execution.View `json:"data"`
		}
		if e = json.Unmarshal(w.Body.Bytes(), &envelope); e != nil {
			t.Fatal(e)
		}
		return envelope.Data
	}
	if w := request("GET", "/api/v1/cards", nil, "", ""); w.Code != 401 {
		t.Fatal("anonymous card read accepted")
	}
	v := decode(request("POST", "/api/v1/execution/operations", map[string]any{"kind": "card_manage", "parameters": cards.Input{Action: "bind", AccountID: account.ID, Issuer: "HTTP simulated bank", Name: "HTTP card", LastFour: "4567", CardType: "credit", CreditLimitMinor: 123400}}, token, uuid.NewString()), 201)
	// Prepare does not bind a card.
	current, e := cards.Read(conn.DB, uid)
	if e != nil || len(current) != 0 {
		t.Fatal("prepare mutated portfolio")
	}
	v = decode(request("POST", "/api/v1/execution/operations/"+v.Operation.ID+"/authorize", execution.AuthorizeInput{Decision: "approve", IntentDigest: v.Operation.IntentDigest, ConfirmationNonce: v.Operation.Nonce}, token, ""), 200)
	v = decode(request("POST", "/api/v1/execution/operations/"+v.Operation.ID+"/execute", execution.ExecuteInput{AuthorizationID: v.Operation.AuthorizationID, IntentDigest: v.Operation.IntentDigest, ExpectedVersion: v.Operation.Version}, token, uuid.NewString()), 200)
	if v.Operation.Status != "succeeded" {
		t.Fatal(v.Operation.ErrorCode)
	}
	current, e = cards.Read(conn.DB, uid)
	if e != nil || len(current) != 1 {
		t.Fatal(e)
	}
	if w := request("GET", "/api/v1/cards", nil, token, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request("GET", "/api/v1/cards/"+current[0].ID, nil, token, ""); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := request("GET", "/api/v1/cards/"+current[0].ID, nil, other, ""); w.Code != 404 {
		t.Fatal("cross-owner card visible")
	}
	agent, e := h.auth.IssueAgent(strconv.FormatInt(uid, 10), "supervisor", uuid.NewString(), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	if w := request("GET", "/api/v1/cards", nil, agent, ""); w.Code != 403 {
		t.Fatal("agent read accepted")
	}
}
