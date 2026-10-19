//go:build integration

package storage

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/execution"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/platform"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/risk"
	"github.com/qingyingjin/banking-service/internal/service"
)

func fixtureDB(t *testing.T, envName, expectedDialect string) *platform.Connections {
	t.Helper()
	path := os.Getenv(envName)
	if path == "" {
		t.Skip(envName + " is not configured; no migration integration test executed")
	}
	values, e := godotenv.Read(path)
	if e != nil {
		t.Fatal("cannot read test configuration")
	}
	cfg, e := config.Load(func(k string) string { return values[k] })
	if e != nil {
		t.Fatal(e)
	}
	name := cfg.MySQLDatabase
	if cfg.DatabaseDriver == "postgres" {
		name = cfg.PostgresDatabase
	}
	if cfg.DatabaseDriver != expectedDialect || !strings.Contains(name, "test") || (!strings.Contains(name, "p7") && !strings.Contains(name, "finance")) {
		t.Fatal("storage migration fixtures require explicitly isolated p7/finance test databases")
	}
	conn, e := platform.Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(conn.Close)
	return conn
}

func emptyFixture(t *testing.T, db *platform.Connections) {
	t.Helper()
	for _, table := range Tables {
		if table == "banking_risk_rules" {
			continue
		}
		var count int64
		if e := db.SQL.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); e != nil {
			t.Fatal(e)
		}
		if count != 0 {
			t.Fatal("storage test database is not pristine: " + table + "; fixtures are never overwritten")
		}
	}
}

func TestOfflineMigrationAndCutoverInvariants(t *testing.T) {
	source := fixtureDB(t, "BANKING_STORAGE_SOURCE_ENV", "mysql")
	target := fixtureDB(t, "BANKING_STORAGE_TARGET_ENV", "postgres")
	emptyFixture(t, source)
	emptyFixture(t, target)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if _, e := source.SQL.ExecContext(ctx, "UPDATE banking_storage_control SET mode='active' WHERE id=1"); e != nil {
		t.Fatal(e)
	}
	if _, e := target.SQL.ExecContext(ctx, "UPDATE banking_storage_control SET mode='standby' WHERE id=1"); e != nil {
		t.Fatal(e)
	}
	meta := service.Meta{UserID: 1960015007001, TraceID: "p7-migration-fixture", TaskID: "p7-migration-fixture"}
	core := service.New(repository.New(source.DB), risk.TrustedGateway{})
	if _, e := core.Register(ctx, meta, "P7 migration fixture"); e != nil {
		t.Fatal(e)
	}
	actor := identity.Actor{Kind: "user", Subject: "1960015007001", UserID: "1960015007001", TokenID: "p7-fixture-human-token-id", Scopes: []string{"execution:read", "execution:prepare", "execution:authorize", "execution:execute"}}
	gateway := execution.New(source.DB, nil)
	perform := func(kind string, payload any, key string) (execution.View, execution.View) {
		t.Helper()
		raw, _ := json.Marshal(payload)
		v, e := gateway.Prepare(ctx, actor, meta, key, execution.PrepareInput{Kind: kind, Parameters: raw})
		if e != nil {
			t.Fatal(e)
		}
		a, e := gateway.Authorize(ctx, actor, meta, v.Operation.ID, execution.AuthorizeInput{Decision: "approve", IntentDigest: v.Operation.IntentDigest, ConfirmationNonce: v.Operation.Nonce})
		if e != nil {
			t.Fatal(e)
		}
		result, e := gateway.Execute(ctx, actor, meta, v.Operation.ID, key+"-execute", execution.ExecuteInput{AuthorizationID: a.Operation.AuthorizationID, IntentDigest: a.Operation.IntentDigest, ExpectedVersion: a.Operation.Version})
		if e != nil || result.Operation.Status != "succeeded" {
			t.Fatalf("fixture operation failed: %s %v", result.Operation.ErrorCode, e)
		}
		return result, a
	}
	first, _ := perform("open_account", service.AccountInput{DisplayName: "P7正常生活账户", Currency: "CNY", InitialBalanceMinor: 10029}, "p7-fixture-first")
	second, _ := perform("open_account", service.AccountInput{DisplayName: "P7目标账户", Currency: "CNY"}, "p7-fixture-second")
	var a, b entity.BankAccount
	if json.Unmarshal(first.Operation.Result, &a) != nil || json.Unmarshal(second.Operation.Result, &b) != nil {
		t.Fatal("invalid fixture result")
	}
	transfer, authorized := perform("transfer", service.TransferInput{SourceAccountID: a.ID, TargetAccountID: b.ID, AmountMinor: 29, Currency: "CNY"}, "p7-fixture-transfer")
	t.Run("RejectedAndExpiredConsentsRemainValidSnapshots", func(t *testing.T) {
		prepare := func(key string, lifetime int64) execution.View {
			t.Helper()
			raw, _ := json.Marshal(service.AccountInput{DisplayName: "P7不执行账户意向", Currency: "CNY"})
			v, e := gateway.Prepare(ctx, actor, meta, key, execution.PrepareInput{Kind: "open_account", Parameters: raw, ExpiresInSeconds: lifetime})
			if e != nil {
				t.Fatal(e)
			}
			return v
		}
		rejected := prepare("p7-fixture-rejected", 900)
		r, e := gateway.Authorize(ctx, actor, meta, rejected.Operation.ID, execution.AuthorizeInput{Decision: "reject", IntentDigest: rejected.Operation.IntentDigest, ConfirmationNonce: rejected.Operation.Nonce})
		if e != nil || r.Operation.Status != "rejected" || len(r.Authorizations) != 1 || r.Authorizations[0].ConsumedAt == 0 {
			t.Fatal("rejected authorization fixture failed", e)
		}
		expiring := prepare("p7-fixture-expired", 1)
		consent := execution.AuthorizeInput{Decision: "approve", IntentDigest: expiring.Operation.IntentDigest, ConfirmationNonce: expiring.Operation.Nonce}
		r, e = gateway.Authorize(ctx, actor, meta, expiring.Operation.ID, consent)
		if e != nil || r.Operation.Status != "authorized" {
			t.Fatal("expiring authorization fixture failed", e)
		}
		timer := time.NewTimer(time.Until(time.UnixMilli(expiring.Operation.ExpiresAt)) + 20*time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		r, e = gateway.Authorize(ctx, actor, meta, expiring.Operation.ID, consent)
		if e != nil || r.Operation.Status != "expired" || len(r.Authorizations) != 1 || r.Authorizations[0].Decision != "approve" || r.Authorizations[0].ConsumedAt != 0 {
			t.Fatal("legitimate expiry without execution was not retained", e)
		}
	})

	// Include actual monetary rows and an SSE replay body in the cross-dialect
	// snapshot, so an empty-table manifest cannot conceal copy/primary-key bugs.
	t.Run("SeedMonetarySnapshotWithPlainSSE", func(t *testing.T) {
		tx, e := source.SQL.BeginTx(ctx, nil)
		if e != nil {
			t.Fatal(e)
		}
		defer tx.Rollback()
		for _, row := range []struct {
			query string
			args  []any
		}{
			{"INSERT INTO banking_model_spend_pools(project_id,currency,policy_hash,limit_units,used_units,held_units,frozen,created_at,updated_at) VALUES('p7-spend-snapshot','CNY',?,90,0,7,?,1,1)", []any{strings.Repeat("a", 64), false}},
			{"INSERT INTO banking_model_spend_attempts(id,project_id,request_key,request_hash,policy_hash,status,reserved_units,estimated_units,response_body,content_type,created_at,updated_at) VALUES('00000000-0000-4000-8000-000000000020','p7-spend-snapshot',?,?,?,'unknown',7,3,?,'text/event-stream',1,1)", []any{strings.Repeat("b", 64), strings.Repeat("c", 64), strings.Repeat("a", 64), "data: {\"text\":\"快照\"}\n\ndata: [DONE]\n\n"}},
			{"INSERT INTO banking_model_spend_events(id,project_id,attempt_id,kind,dedupe_key,request_hash,amount_units,snapshot_json,created_at) VALUES('00000000-0000-4000-8000-000000000021','p7-spend-snapshot','00000000-0000-4000-8000-000000000020','reserve',?,?,7,'{\"synthetic\":true}',1)", []any{strings.Repeat("d", 64), strings.Repeat("c", 64)}},
		} {
			if _, e = tx.ExecContext(ctx, row.query, row.args...); e != nil {
				t.Fatal("cannot seed monetary snapshot", e)
			}
		}
		if e = tx.Commit(); e != nil {
			t.Fatal(e)
		}
	})

	t.Run("ActiveSourceRefused", func(t *testing.T) {
		if _, e := Import(ctx, source.SQL, target.SQL, "mysql", "postgres", ImportOptions{AllowBootstrapRules: true}); e == nil {
			t.Fatal("active source was imported")
		}
	})
	if _, e := source.SQL.ExecContext(ctx, "UPDATE banking_storage_control SET mode='frozen' WHERE id=1"); e != nil {
		t.Fatal(e)
	}
	t.Run("BootstrapNeedsExplicitAcknowledgment", func(t *testing.T) {
		if _, e := Import(ctx, source.SQL, target.SQL, "mysql", "postgres"); e == nil {
			t.Fatal("bootstrap overwrite was implicit")
		}
	})
	t.Run("FailedCopyRollsBackAllRowsAndControlMode", func(t *testing.T) {
		var beforeGeneration int64
		if e := target.SQL.QueryRowContext(ctx, "SELECT generation FROM banking_storage_control WHERE id=1").Scan(&beforeGeneration); e != nil {
			t.Fatal(e)
		}
		if _, e := target.SQL.ExecContext(ctx, `CREATE FUNCTION banking_p7_test_reject_copy() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'controlled P7 copy failure'; END $$`); e != nil {
			t.Fatal(e)
		}
		defer target.SQL.ExecContext(context.Background(), "DROP FUNCTION banking_p7_test_reject_copy() CASCADE")
		if _, e := target.SQL.ExecContext(ctx, "CREATE TRIGGER banking_p7_test_reject_copy BEFORE INSERT ON banking_transactions FOR EACH ROW EXECUTE FUNCTION banking_p7_test_reject_copy()"); e != nil {
			t.Fatal(e)
		}
		if _, e := Import(ctx, source.SQL, target.SQL, "mysql", "postgres", ImportOptions{AllowBootstrapRules: true}); e == nil {
			t.Fatal("controlled insertion failure was ignored")
		}
		emptyFixture(t, target)
		var mode string
		var rules, generation int64
		if e := target.SQL.QueryRowContext(ctx, "SELECT mode FROM banking_storage_control WHERE id=1").Scan(&mode); e != nil {
			t.Fatal(e)
		}
		if e := target.SQL.QueryRowContext(ctx, "SELECT COUNT(*) FROM banking_risk_rules").Scan(&rules); e != nil {
			t.Fatal(e)
		}
		if e := target.SQL.QueryRowContext(ctx, "SELECT generation FROM banking_storage_control WHERE id=1").Scan(&generation); e != nil {
			t.Fatal(e)
		}
		if mode != "standby" || rules != 6 || generation != beforeGeneration {
			t.Fatal("failed import changed target baseline or state")
		}
	})
	t.Run("Exact52TableSnapshotAnd17FinancialChecks", func(t *testing.T) {
		var beforeGeneration, afterGeneration int64
		if e := target.SQL.QueryRowContext(ctx, "SELECT generation FROM banking_storage_control WHERE id=1").Scan(&beforeGeneration); e != nil {
			t.Fatal(e)
		}
		r, e := Import(ctx, source.SQL, target.SQL, "mysql", "postgres", ImportOptions{AllowBootstrapRules: true})
		if e != nil {
			t.Fatal(e)
		}
		if !r.Passed || len(r.Source) != 52 || len(r.Target) != 52 || len(r.SourceChecks) != 17 || !sameData(r.Source, r.Target) {
			t.Fatal("incomplete reconciliation")
		}
		if _, e := Compare(ctx, source.SQL, target.SQL, "mysql", "postgres"); e != nil {
			t.Fatal(e)
		}
		if e := target.SQL.QueryRowContext(ctx, "SELECT generation FROM banking_storage_control WHERE id=1").Scan(&afterGeneration); e != nil || afterGeneration != beforeGeneration+1 {
			t.Fatal("committed fresh import did not invalidate prior activation generations", e)
		}
	})
	t.Run("CompletedSnapshotRetryVerifiesWithoutWrites", func(t *testing.T) {
		var beforeGeneration, afterGeneration, beforeUpdated, afterUpdated int64
		if e := target.SQL.QueryRowContext(ctx, "SELECT generation,updated_at FROM banking_storage_control WHERE id=1").Scan(&beforeGeneration, &beforeUpdated); e != nil {
			t.Fatal(e)
		}
		before, e := Compare(ctx, source.SQL, target.SQL, "mysql", "postgres")
		if e != nil {
			t.Fatal(e)
		}
		r, e := Import(ctx, source.SQL, target.SQL, "mysql", "postgres", ImportOptions{AllowBootstrapRules: true})
		if e != nil || !r.Passed || !r.Replayed || r.Digest != before.Digest {
			t.Fatal("identical completed snapshot was not verified as completed", e)
		}
		after, e := Compare(ctx, source.SQL, target.SQL, "mysql", "postgres")
		if e != nil || !sameData(before.Target, after.Target) {
			t.Fatal("completed retry changed persisted rows", e)
		}
		if e := target.SQL.QueryRowContext(ctx, "SELECT generation,updated_at FROM banking_storage_control WHERE id=1").Scan(&afterGeneration, &afterUpdated); e != nil || afterGeneration != beforeGeneration || afterUpdated != beforeUpdated {
			t.Fatal("completed retry changed control metadata", e)
		}
	})
	t.Run("SnapshotReceiptMismatchNeverOverwritesTarget", func(t *testing.T) {
		var digest string
		if e := target.SQL.QueryRowContext(ctx, "SELECT snapshot_digest FROM banking_storage_control WHERE id=1").Scan(&digest); e != nil {
			t.Fatal(e)
		}
		if _, e := target.SQL.ExecContext(ctx, "UPDATE banking_storage_control SET snapshot_digest=$1 WHERE id=1", strings.Repeat("0", 64)); e != nil {
			t.Fatal(e)
		}
		defer target.SQL.ExecContext(context.Background(), "UPDATE banking_storage_control SET snapshot_digest=$1 WHERE id=1", digest)
		if _, e := Import(ctx, source.SQL, target.SQL, "mysql", "postgres", ImportOptions{AllowBootstrapRules: true}); e == nil {
			t.Fatal("unverified snapshot receipt was accepted")
		}
		if _, e := Compare(ctx, source.SQL, target.SQL, "mysql", "postgres"); e != nil {
			t.Fatal("receipt mismatch attempt changed business rows", e)
		}
	})
	t.Run("MigratedAuthorizationReplayCannotPostAgain", func(t *testing.T) {
		if _, e := target.SQL.ExecContext(ctx, "UPDATE banking_storage_control SET mode='active' WHERE id=1"); e != nil {
			t.Fatal(e)
		}
		targetGateway := execution.New(target.DB, nil)
		in := execution.ExecuteInput{AuthorizationID: authorized.Operation.AuthorizationID, IntentDigest: authorized.Operation.IntentDigest, ExpectedVersion: authorized.Operation.Version}
		var before, after int64
		if e := target.SQL.QueryRowContext(ctx, "SELECT COUNT(*) FROM banking_ledger_entries").Scan(&before); e != nil {
			t.Fatal(e)
		}
		v, e := targetGateway.Execute(ctx, actor, meta, transfer.Operation.ID, "p7-fixture-transfer-execute", in)
		if e != nil || v.Operation.Status != "succeeded" {
			t.Fatal("migrated replay failed", e)
		}
		if e = target.SQL.QueryRowContext(ctx, "SELECT COUNT(*) FROM banking_ledger_entries").Scan(&after); e != nil {
			t.Fatal(e)
		}
		if before != after {
			t.Fatal("replay posted a second journal")
		}
		var balance int64
		if e = target.SQL.QueryRowContext(ctx, "SELECT balance_minor FROM banking_bank_accounts WHERE id=$1", a.ID).Scan(&balance); e != nil {
			t.Fatal(e)
		}
		if balance != 10000 {
			t.Fatal("migrated balance changed on replay")
		}
		in.IntentDigest = "changed"
		if _, e = targetGateway.Execute(ctx, actor, meta, transfer.Operation.ID, "p7-fixture-transfer-execute", in); entity.Code(e) != "IDEMPOTENCY_CONFLICT" {
			t.Fatal("migrated key accepted different request")
		}
	})
	t.Run("CommittedTargetChangesRefuseStaleRollback", func(t *testing.T) {
		if _, e := target.SQL.ExecContext(ctx, "UPDATE banking_storage_control SET mode='frozen' WHERE id=1"); e != nil {
			t.Fatal(e)
		}
		r, e := Compare(ctx, source.SQL, target.SQL, "mysql", "postgres")
		if e == nil || r.Passed {
			t.Fatal("rollback comparison ignored new execution audit rows")
		}
		if _, e := target.SQL.ExecContext(ctx, "UPDATE banking_storage_control SET mode='standby' WHERE id=1"); e != nil {
			t.Fatal(e)
		}
		defer target.SQL.ExecContext(context.Background(), "UPDATE banking_storage_control SET mode='frozen' WHERE id=1")
		if _, e := Import(ctx, source.SQL, target.SQL, "mysql", "postgres", ImportOptions{AllowBootstrapRules: true}); e == nil {
			t.Fatal("changed target was accepted as a completed import")
		}
	})
	t.Run("ReverseImportIntoFreshMySQL", func(t *testing.T) {
		rollback := fixtureDB(t, "BANKING_STORAGE_ROLLBACK_ENV", "mysql")
		emptyFixture(t, rollback)
		if _, e := rollback.SQL.ExecContext(ctx, "UPDATE banking_storage_control SET mode='standby' WHERE id=1"); e != nil {
			t.Fatal(e)
		}
		r, e := Import(ctx, target.SQL, rollback.SQL, "postgres", "mysql", ImportOptions{AllowBootstrapRules: true})
		if e != nil || !r.Passed {
			t.Fatal("reverse migration failed", e)
		}
		if _, e = Compare(ctx, target.SQL, rollback.SQL, "postgres", "mysql"); e != nil {
			t.Fatal(e)
		}
		var importFlag int
		if e = rollback.SQL.QueryRowContext(ctx, "SELECT COALESCE(@banking_migration_import,0)").Scan(&importFlag); e != nil || importFlag != 0 {
			t.Fatal("operator import flag leaked to pooled connection")
		}
	})
}
