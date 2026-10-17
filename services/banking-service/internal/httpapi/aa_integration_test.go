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
	"github.com/qingyingjin/banking-service/internal/aa"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/execution"
	"github.com/qingyingjin/banking-service/internal/platform"
	"github.com/qingyingjin/banking-service/internal/service"
	"io"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestAAHTTPContractAndOwner(t *testing.T) {
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

	payerAccount, e := h.core.CreateAccount(context.Background(), service.Meta{UserID: uid + 1}, uuid.NewString(), service.AccountInput{DisplayName: "HTTP payer", Currency: "CNY", InitialBalanceMinor: 10000, ConfirmSimulatedFunds: true})
	if e != nil {
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
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &envelope)
		return envelope.Data
	}
	base := "/api/v1/aa"
	agent, e := h.auth.IssueAgent(strconv.FormatInt(uid, 10), "supervisor", uuid.NewString(), time.Minute)
	if e != nil {
		t.Fatal(e)
	}
	call("GET", base+"/bills", nil, "", 401)
	call("GET", base+"/bills", nil, agent, 403)
	call("POST", base+"/bills", map[string]any{"title": "unknown", "user_id": "1"}, token, 400)
	var bill aa.View
	body := aa.Input{Title: "HTTP AA", TotalMinor: 6000, Mode: "equal", AccountID: a.ID, AlreadyPaid: true, Participants: []aa.ShareInput{{Name: "同伴"}}}
	if e = json.Unmarshal(call("POST", base+"/bills", body, token, 201), &bill); e != nil {
		t.Fatal(e)
	}
	if bill.Participants[1].AmountMinor != 3000 {
		t.Fatal("incorrect split")
	}
	billPath := base + "/bills/" + strconv.FormatInt(uid, 10) + "/" + bill.ID
	call("GET", billPath, nil, other, 404)
	var preview aa.View
	_ = json.Unmarshal(call("POST", base+"/invitations/preview", map[string]any{"invitation": bill.Invitation}, other, 200), &preview)
	if preview.Invitation != "" {
		t.Fatal("invite leaked to recipient")
	}
	var joined aa.View
	_ = json.Unmarshal(call("POST", base+"/join", map[string]any{"invitation": bill.Invitation, "participant_id": bill.Participants[1].ID}, other, 200), &joined)
	if !joined.Participants[1].IsMe {
		t.Fatal("join identity missing")
	}
	call("GET", billPath, nil, other, 200)
	call("GET", base+"/bills?limit=1&offset=0", nil, other, 200)
	var op execution.View
	_ = json.Unmarshal(call("POST", "/api/v1/execution/operations", execution.PrepareInput{Kind: "aa_payment", Parameters: marshalTest(aa.PaymentInput{OwnerID: uid, BillID: bill.ID, ParticipantID: bill.Participants[1].ID, SourceAccountID: payerAccount.ID})}, other, 201), &op)
	if op.Operation.AmountMinor != 3000 || op.Operation.TargetAccountID != a.ID {
		t.Fatal("intent is not bound to bill")
	}
	_ = json.Unmarshal(call("POST", "/api/v1/execution/operations/"+op.Operation.ID+"/authorize", execution.AuthorizeInput{Decision: "approve", IntentDigest: op.Operation.IntentDigest, ConfirmationNonce: op.Operation.Nonce}, other, 200), &op)
	_ = json.Unmarshal(call("POST", "/api/v1/execution/operations/"+op.Operation.ID+"/execute", execution.ExecuteInput{AuthorizationID: op.Operation.AuthorizationID, IntentDigest: op.Operation.IntentDigest, ExpectedVersion: op.Operation.Version}, other, 200), &op)
	if op.Operation.Status != "succeeded" {
		t.Fatal("AA HTTP payment failed", op.Operation.ErrorCode)
	}
	var settled aa.View
	_ = json.Unmarshal(call("GET", billPath, nil, token, 200), &settled)
	if settled.Status != "settled" || settled.CollectedMinor != 3000 {
		t.Fatal("progress did not settle")
	}
	call("POST", base+"/bills/"+bill.ID+"/cancel", map[string]any{"expected_version": settled.Version}, token, 409)
	var open aa.View
	_ = json.Unmarshal(call("POST", base+"/bills", body, token, 201), &open)
	call("POST", base+"/bills/"+open.ID+"/cancel", map[string]any{"expected_version": 1}, token, 200)
	// Manual WeChat shares are metadata, with a separate bound review for each human.
	manualInput := aa.Input{Title: "HTTP WeChat details", TotalMinor: 6000, Mode: "equal", AlreadyPaid: true, PaymentMethod: "wechat_manual", ManagementMode: "collaborative", RecipientHint: "Owner - verify in WeChat", Items: []aa.LineItem{{Name: "Dinner", AmountMinor: 5000}, {Name: "Travel", AmountMinor: 1000}}, Participants: []aa.ShareInput{{Name: "Payer"}}}
	var manual aa.View
	_ = json.Unmarshal(call("POST", base+"/bills", manualInput, token, 201), &manual)
	_ = json.Unmarshal(call("POST", base+"/join", map[string]any{"invitation": manual.Invitation, "participant_id": manual.Participants[1].ID}, other, 200), &manual)
	manualBase := base + "/wechat/" + strconv.FormatInt(uid, 10) + "/" + manual.ID
	input := aa.ReviewInput{ParticipantID: manual.Participants[1].ID, Purpose: "report_paid", ExpectedVersion: manual.Version}
	call("POST", manualBase+"/review", input, agent, 403)
	call("POST", manualBase+"/review", input, token, 403)
	call("POST", manualBase+"/confirm", map[string]any{"confirmed": true}, other, 400)
	var review aa.Review
	_ = json.Unmarshal(call("POST", manualBase+"/review", input, other, 200), &review)
	confirm := func(r aa.Review) aa.ConfirmInput {
		return aa.ConfirmInput{ReviewInput: aa.ReviewInput{ParticipantID: r.ParticipantID, Purpose: r.Purpose, ExpectedVersion: r.Bill.Version}, IntentID: r.IntentID, Digest: r.Digest, Nonce: r.Nonce}
	}
	_ = json.Unmarshal(call("POST", manualBase+"/confirm", confirm(review), other, 200), &manual)
	if manual.CollectedMinor != 0 || manual.Participants[1].Status != "payment_reported" {
		t.Fatal("self-report incorrectly verified")
	}
	input.Purpose = "confirm_received"
	input.ExpectedVersion = manual.Version
	call("POST", manualBase+"/review", input, other, 403)
	_ = json.Unmarshal(call("POST", manualBase+"/review", input, token, 200), &review)
	_ = json.Unmarshal(call("POST", manualBase+"/confirm", confirm(review), token, 200), &manual)
	if manual.Status != "settled" || manual.PlatformVerified || manual.CollectedMinor != 3000 {
		t.Fatal("manual receipt state")
	}
	call("POST", manualBase+"/confirm", confirm(review), token, 409)
	// New manual bills default to a private one-account management flow.
	manualInput.ManagementMode = ""
	var single aa.View
	_ = json.Unmarshal(call("POST", base+"/bills", manualInput, token, 201), &single)
	if single.ManagementMode != "single_manager" || single.Invitation != "" || single.Participants[1].Status != "awaiting_receipt" {
		t.Fatal("single default HTTP contract")
	}
	singleBase := base + "/wechat/" + strconv.FormatInt(uid, 10) + "/" + single.ID
	singleInput := aa.ReviewInput{ParticipantID: single.Participants[1].ID, Purpose: "confirm_received", ExpectedVersion: single.Version}
	call("POST", singleBase+"/review", singleInput, other, 404)
	call("POST", singleBase+"/review", singleInput, agent, 403)
	_ = json.Unmarshal(call("POST", singleBase+"/review", singleInput, token, 200), &review)
	_ = json.Unmarshal(call("POST", singleBase+"/confirm", confirm(review), token, 200), &single)
	if single.Status != "settled" || single.Participants[1].ReportedAt != 0 || single.Participants[1].IsMe || single.PlatformVerified || single.CollectedMinor != 3000 {
		t.Fatal("owner record incorrectly attributed to payer")
	}
	call("POST", singleBase+"/confirm", confirm(review), token, 409)
	if strings.Contains(h.logs.String(), review.Nonce) {
		t.Fatal("manual confirmation nonce leaked into logs")
	}
	if strings.Contains(h.logs.String(), bill.Invitation) {
		t.Fatal("private AA invitation leaked into HTTP logs")
	}
}
func marshalTest(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
