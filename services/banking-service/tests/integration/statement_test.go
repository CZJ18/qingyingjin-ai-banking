//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/execution"
	"github.com/qingyingjin/banking-service/internal/service"
	"github.com/qingyingjin/banking-service/internal/statement"
	"github.com/qingyingjin/banking-service/internal/subscription"
	"testing"
)

func TestCSVPreviewAuthorizedImportProvenanceAndIsolation(t *testing.T) {
	h := harness(t)
	a := h.account(t, 10000)
	installExecutionHTTP(t, h, subscription.LocalSimulator{})
	body := map[string]any{"account_id": fmt.Sprint(a.ID), "csv": "id,at,amount,direction,currency,status\n" + uuid.NewString() + ",2026-10-01 08:30:00,0.29,expense,CNY,posted\n", "timezone": "Asia/Shanghai", "columns": map[string]string{"external_id": "id", "occurred_at": "at", "amount": "amount", "direction": "direction", "currency": "currency", "status": "status"}}
	preview := parse[statement.Preview](t, h.call(t, "POST", "/api/v1/transactions/import/preview", "", body, 200))
	if len(preview.Records) != 1 || preview.FinancialExecutionEnabled {
		t.Fatal(preview)
	}
	var count int64
	h.conn.DB.Model(&entity.Transaction{}).Where("user_id=? AND kind='external'", h.uid).Count(&count)
	if count != 0 {
		t.Fatal("preview inserted transactions")
	}
	record := preview.Records[0]
	in := service.TransactionInput{BankAccountID: a.ID, ExternalTransactionID: record.ExternalTransactionID, Direction: record.Direction, AmountMinor: record.AmountMinor, Currency: record.Currency, Category: record.Category, OccurredAt: record.OccurredAt, EffectMode: record.EffectMode, Source: &record.Source}
	h.call(t, "POST", "/api/v1/transactions/import", uuid.NewString(), in, 403)
	prepared := execPrepare(t, h, "import_transaction", in)
	h.balance(t, a.ID, 10000)
	h.call(t, "POST", "/api/v1/execution/operations/"+prepared.Operation.ID+"/execute", uuid.NewString(), execution.ExecuteInput{IntentDigest: prepared.Operation.IntentDigest, ExpectedVersion: prepared.Operation.Version}, 422)
	authorized := execAuthorize(t, h, prepared)
	key := uuid.NewString()
	done := execRun(t, h, authorized, key)
	if done.Operation.Status != "succeeded" {
		t.Fatal(done.Operation.ErrorCode)
	}
	var transaction entity.Transaction
	if json.Unmarshal(done.Operation.Result, &transaction) != nil {
		t.Fatal("invalid result")
	}
	replay := execRun(t, h, authorized, key)
	if replay.Operation.ID != done.Operation.ID {
		t.Fatal("replay identity changed")
	}
	h.balance(t, a.ID, 10000)
	source := parse[statement.Source](t, h.call(t, "GET", fmt.Sprintf("/api/v1/transactions/%d/source", transaction.ID), "", nil, 200))
	if source.RowSHA256 != record.Source.RowSHA256 || source.RawRecord["amount"] != "0.29" {
		t.Fatal(source)
	}
	other := harness(t)
	code, out, err := h.raw("GET", fmt.Sprintf("/api/v1/transactions/%d/source", transaction.ID), "", nil, other.token)
	if err != nil || code != 404 || out.Code != "NOT_FOUND" {
		t.Fatalf("source leaked across user: %d %s %v", code, out.Code, err)
	}
	code, _, err = h.raw("POST", "/api/v1/transactions/import/preview", "", body, other.token)
	if err != nil || code != 404 {
		t.Fatal("foreign upload account accepted", code, err)
	}
	in.EffectMode = "apply_simulated_effect"
	raw, _ := json.Marshal(in)
	h.call(t, "POST", "/api/v1/execution/operations", uuid.NewString(), execution.PrepareInput{Kind: "import_transaction", Parameters: raw}, 400)
	h.conn.DB.Model(&entity.Transaction{}).Where("user_id=? AND kind='external'", h.uid).Count(&count)
	if count != 1 {
		t.Fatal("duplicate execution inserted rows", count)
	}
}
