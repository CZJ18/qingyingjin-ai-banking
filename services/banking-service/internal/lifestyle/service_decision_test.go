package lifestyle

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/qingyingjin/banking-service/internal/billing"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/service"
	"gorm.io/gorm"
)

type decisionNoSQLPool struct{ snapshotNoSQL }
type decisionNoSQLTx struct{ snapshotNoSQL }

func (c decisionNoSQLPool) BeginTx(context.Context, *sql.TxOptions) (gorm.ConnPool, error) {
	return &decisionNoSQLTx{c.snapshotNoSQL}, nil
}
func (decisionNoSQLTx) Commit() error   { return nil }
func (decisionNoSQLTx) Rollback() error { return nil }

func TestDecideRechecksHistoricalMonthBeforeAccept(t *testing.T) {
	for _, tc := range []struct {
		name           string
		storedComplete bool
		closedSnapshot bool
		decision       string
		wantCode       string
	}{
		{"legacy_partial_report_cannot_be_accepted", true, false, "accept", "VERSION_CONFLICT"},
		{"legacy_partial_report_can_still_be_rejected", true, false, "reject", ""},
		{"legacy_closed_report_without_verified_coverage_cannot_be_accepted", true, true, "accept", "VERSION_CONFLICT"},
		{"unchanged_partial_report_can_still_be_rejected", false, false, "reject", ""},
		{"unchanged_closed_report_can_still_be_rejected", false, true, "reject", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			start := time.Date(2025, 9, 1, 0, 0, 0, 0, time.UTC)
			end := start.AddDate(0, 1, 0)
			at := start.AddDate(0, 0, 14)
			if tc.closedSnapshot {
				at = end
			}
			period, err := billing.Normalize(billing.Input{From: start.Format(time.RFC3339), To: end.Format(time.RFC3339), Timezone: "UTC"})
			if err != nil {
				t.Fatal(err)
			}
			account := entity.BankAccount{Base: entity.Base{ID: 7, UserID: 42}, Currency: "CNY"}
			report, err := billing.Analyze(42, "generate_billing_report", period, billing.Snapshot{At: at.UnixMilli(),
				Accounts: []entity.BankAccount{account}, Transactions: []entity.Transaction{{
					Base: entity.Base{ID: 8, UserID: 42}, BankAccountID: 7, Currency: "CNY",
					Kind: "external", Direction: "expense", Category: "shopping", AmountMinor: 200000,
					OccurredAt: start.Add(time.Hour).UnixMilli(),
				}}})
			if err != nil {
				t.Fatal(err)
			}
			db := snapshotFixture(t, report, []entity.BankAccount{account})
			goal := Goal{Base: entity.Base{ID: 1, UserID: 42}, CurrentVersion: 1}
			snap, err := loadSnapshot(db, 42, goal, CompileInput{ExpectedVersion: 1, BudgetID: 2, BillingReportID: report.ID})
			if err != nil || snap.CompleteMonth {
				t.Fatalf("invalid synthetic snapshot: %+v %v", snap, err)
			}
			// Simulate a retained plan compiled before the completeness fix. The
			// source report and SourceDigest remain unchanged across the decision.
			snap.CompleteMonth = tc.storedComplete
			snapshotJSON, err := json.Marshal(snap)
			if err != nil {
				t.Fatal(err)
			}
			originalSnapshotJSON := append([]byte(nil), snapshotJSON...)
			plan := Plan{Base: entity.Base{ID: 10, UserID: 42}, InputDigest: snap.SourceDigest, SnapshotJSON: snapshotJSON}
			rec := Recommendation{Base: entity.Base{ID: 11, UserID: 42}, GoalID: 1, PlanID: 10,
				Version: 1, Status: "pending", ExpiresAt: time.Now().Add(time.Hour).UnixMilli()}
			var cached *entity.Idempotency
			var audits, decisions, updates int
			originalQuery := db.Callback().Query().Get("gorm:query")
			err = db.Callback().Query().Replace("gorm:query", func(tx *gorm.DB) {
				tx.RowsAffected = 1
				switch dest := tx.Statement.Dest.(type) {
				case *entity.User:
					*dest = entity.User{ID: 42, Status: "active"}
				case *entity.Idempotency:
					if cached == nil {
						tx.RowsAffected = 0
					} else {
						*dest = *cached
					}
				case *Recommendation:
					*dest = rec
				case *[]Recommendation:
					*dest = []Recommendation{rec}
				case *Plan:
					*dest = plan
				case *Goal:
					*dest = goal
				default:
					originalQuery(tx)
				}
			})
			if err != nil {
				t.Fatal(err)
			}
			err = db.Callback().Create().Replace("gorm:create", func(tx *gorm.DB) {
				switch dest := tx.Statement.Dest.(type) {
				case *entity.Audit:
					audits++
				case *Decision:
					decisions++
					dest.ID = 30
				case *entity.Idempotency:
					copy := *dest
					cached = &copy
				default:
					t.Fatalf("unexpected decision create %T", dest)
				}
				tx.RowsAffected = 1
			})
			if err != nil {
				t.Fatal(err)
			}
			err = db.Callback().Update().Replace("gorm:update", func(tx *gorm.DB) {
				updates++
				model, ok := tx.Statement.Model.(*Recommendation)
				if !ok {
					t.Fatalf("unexpected decision update %T", tx.Statement.Model)
				}
				if model.ID == rec.ID {
					values := tx.Statement.Dest.(map[string]any)
					rec.Status = values["status"].(string)
					rec.Version = values["version"].(int64)
				}
				tx.RowsAffected = 1
			})
			if err != nil {
				t.Fatal(err)
			}
			db.Config.SkipDefaultTransaction = true
			db.ConnPool = decisionNoSQLPool{snapshotNoSQL{t}}
			db.Statement.ConnPool = db.ConnPool
			svc := New(db)
			meta := service.Meta{UserID: 42, TraceID: "synthetic-decision"}
			in := DecisionInput{ExpectedVersion: 1, PlanDigest: plan.InputDigest, Decision: tc.decision}
			decision, err := svc.Decide(context.Background(), meta, "synthetic-decision-key", rec.ID, in)
			if entity.Code(err) != tc.wantCode {
				t.Fatalf("Decide returned %v, want %q; decision=%+v", err, tc.wantCode, decision)
			}
			if !bytes.Equal(plan.SnapshotJSON, originalSnapshotJSON) {
				t.Fatal("retained plan snapshot was rewritten")
			}
			if tc.wantCode != "" {
				if rec.Status != "pending" || rec.Version != 1 || updates != 0 || audits != 0 || decisions != 0 || cached != nil {
					t.Fatalf("stale acceptance mutated retained data: rec=%+v updates=%d audits=%d decisions=%d cached=%+v", rec, updates, audits, decisions, cached)
				}
				return
			}
			wantStatus := "accepted"
			if tc.decision == "reject" {
				wantStatus = "rejected"
			}
			if decision.Decision != tc.decision || rec.Status != wantStatus || rec.Version != 2 || audits != 1 || decisions != 1 || cached == nil {
				t.Fatalf("valid decision was not recorded once: %+v rec=%+v audits=%d decisions=%d cached=%+v", decision, rec, audits, decisions, cached)
			}
			firstUpdates := updates
			replayed, err := svc.Decide(context.Background(), meta, "synthetic-decision-key", rec.ID, in)
			if err != nil || replayed.ID != decision.ID || updates != firstUpdates || audits != 1 || decisions != 1 {
				t.Fatalf("idempotent decision replay changed data: %+v %v updates=%d audits=%d decisions=%d", replayed, err, updates, audits, decisions)
			}
		})
	}
}
