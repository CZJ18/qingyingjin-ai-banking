//go:build integration

package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/platform"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/risk"
	"github.com/qingyingjin/banking-service/internal/service"
	"github.com/qingyingjin/banking-service/internal/statement"
)

// This test uses only its explicit new isolated configuration. No .env loading,
// model adapter, live supplier, execution grant or budget reservation is used.
func TestStatementPreviewStoredTransactions(t *testing.T) {
	path := os.Getenv("BANKING_STATEMENT_TEST_ENV")
	if path == "" {
		t.Skip("explicit isolated BANKING_STATEMENT_TEST_ENV required")
	}
	env, err := godotenv.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(env["BANKING_POSTGRES_DATABASE"], "_test") {
		t.Fatal("isolated test database required")
	}
	cfg, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	conn, err := platform.Open(cfg)
	if err != nil {
		t.Fatal("isolated DB unavailable")
	}
	defer conn.Close()
	uid := time.Now().UnixNano()%1000000000000000 + 1000000000000
	for _, id := range []int64{uid, uid + 1} {
		if err := conn.DB.Create(&entity.User{ID: id, DisplayName: "Synthetic statement owner", Status: "active"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	accounts := []entity.BankAccount{}
	for i, owner := range []int64{uid, uid, uid + 1} {
		account := entity.BankAccount{Base: entity.Base{UserID: owner}, DisplayName: fmt.Sprintf("Synthetic statement fixture %d", i), AccountType: "checking", MaskedAccountNumber: "SIM ****0000", Status: "active", Currency: "CNY", Version: 1, IsSimulated: true}
		if err := conn.DB.Create(&account).Error; err != nil {
			t.Fatal(err)
		}
		accounts = append(accounts, account)
	}
	stamp := uuid.NewString()
	input := func(id, amount string) string {
		return id + ",2026-09-02 12:00:00," + amount + ",expense,CNY,posted,shopping,合成测试商户\n"
	}
	header := "id,at,amount,direction,currency,status,category,merchant\n"
	columns := map[string]string{"external_id": "id", "occurred_at": "at", "amount": "amount", "direction": "direction", "currency": "currency", "status": "status", "category": "category", "merchant": "merchant"}
	seeded := []struct {
		account  int
		external string
	}{{0, "stored-" + stamp}, {1, "elsewhere-" + stamp}, {2, "foreign-" + stamp}, {0, "Résumé-" + stamp}}
	for _, seed := range seeded {
		parsed, e := statement.Parse(statement.Input{CSV: header + input(seed.external, "10.00"), Timezone: "Asia/Shanghai", Columns: columns})
		if e != nil {
			t.Fatal(e)
		}
		r := parsed.Records[0]
		external := r.ExternalTransactionID
		source, _ := json.Marshal(r.Source)
		tx := entity.Transaction{Base: entity.Base{UserID: accounts[seed.account].UserID}, BankAccountID: accounts[seed.account].ID, ExternalTransactionID: &external, Direction: r.Direction, AmountMinor: r.AmountMinor, Currency: r.Currency, Category: r.Category, MerchantName: r.MerchantName, OccurredAt: r.OccurredAt, Kind: "external", EffectMode: "record_only", SourceMetadata: source, AuditID: uuid.NewString()}
		if err := conn.DB.Create(&tx).Error; err != nil {
			t.Fatal(err)
		}
	}
	auth := identity.New(strings.Repeat("u", 32), "statement-test", "banking-service", strings.Repeat("t", 32), "42", "7")
	core := service.New(repository.New(conn.DB), risk.SimulationGateway{})
	handler := New(auth, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, time.Second, core)
	token, e := auth.IssueUser(strconv.FormatInt(uid, 10), "", time.Hour)
	if e != nil {
		t.Fatal(e)
	}
	snapshot := func() map[string]int64 {
		out := map[string]int64{}
		for _, table := range []string{"banking_transactions", "banking_operation_intents", "banking_user_authorizations", "banking_model_spend_attempts", "banking_model_spend_events", "banking_model_attempt_owners"} {
			var n int64
			if err := conn.DB.Table(table).Count(&n).Error; err != nil {
				t.Fatal(err)
			}
			out[table] = n
		}
		return out
	}
	before := snapshot()
	request := func(account int64, csv string) (*httptest.ResponseRecorder, statement.Preview) {
		raw, _ := json.Marshal(map[string]any{"account_id": strconv.FormatInt(account, 10), "csv": csv, "timezone": "Asia/Shanghai", "columns": columns})
		req := httptest.NewRequest("POST", "http://banking.test/api/v1/transactions/import/preview", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		var envelope struct {
			Data statement.Preview `json:"data"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &envelope)
		return rec, envelope.Data
	}
	t.Run("stored_rows_are_skipped_and_other_accounts_remain_eligible", func(t *testing.T) {
		rec, preview := request(accounts[0].ID, header+input("stored-"+stamp, "10.00")+input("elsewhere-"+stamp, "10.00")+input("foreign-"+stamp, "10.00"))
		if rec.Code != 200 || len(preview.Records) != 2 || preview.Skipped != 1 {
			t.Fatalf("want 2 eligible, 1 already imported; status=%d preview=%+v", rec.Code, preview)
		}
		if preview.FinancialExecutionEnabled || preview.Records[0].EffectMode != "record_only" {
			t.Fatal("preview enabled financial execution")
		}
	})
	t.Run("conflicting_persistent_external_id_is_not_silently_skipped", func(t *testing.T) {
		rec, _ := request(accounts[0].ID, header+input("stored-"+stamp, "11.00"))
		if rec.Code != 409 || !strings.Contains(rec.Body.String(), "IDEMPOTENCY_CONFLICT") {
			t.Fatalf("want conflict; got %d %s", rec.Code, rec.Body.String())
		}
	})
	t.Run("database_unique_key_collation_is_respected", func(t *testing.T) {
		rec, preview := request(accounts[0].ID, header+input("resume-"+stamp, "10.00"))
		if rec.Code != 200 || len(preview.Records) != 0 || preview.Skipped != 1 {
			t.Fatalf("database equivalent external ID offered again: %d %+v", rec.Code, preview)
		}
	})
	t.Run("foreign_account_is_not_disclosed", func(t *testing.T) {
		rec, _ := request(accounts[2].ID, header+input("foreign-"+stamp, "10.00"))
		if rec.Code != 404 {
			t.Fatalf("foreign account accepted: %d", rec.Code)
		}
	})
	t.Run("bounded_lookup_handles_more_than_one_batch", func(t *testing.T) {
		var csv strings.Builder
		csv.WriteString(header)
		for i := 0; i < 100; i++ {
			csv.WriteString(input(fmt.Sprintf("batch-%s-%d", stamp, i), "10.00"))
		}
		csv.WriteString(input("stored-"+stamp, "10.00"))
		rec, preview := request(accounts[0].ID, csv.String())
		if rec.Code != 200 || len(preview.Records) != 100 || preview.Skipped != 1 {
			t.Fatalf("cross-batch lookup lost scope or skipped rows: status=%d records=%d skipped=%d", rec.Code, len(preview.Records), preview.Skipped)
		}
	})
	after := snapshot()
	left, _ := json.Marshal(before)
	right, _ := json.Marshal(after)
	if string(left) != string(right) {
		t.Fatalf("read-only preview mutated persistent counts: before=%s after=%s", left, right)
	}
}
