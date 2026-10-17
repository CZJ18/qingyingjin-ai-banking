//go:build integration

package aa

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/platform"
	"gorm.io/gorm"
)

func TestWeChatManualDatabase(t *testing.T) {
	path := os.Getenv("BANKING_AA_TEST_ENV")
	if path == "" {
		t.Skip("isolated BANKING_AA_TEST_ENV required")
	}
	env, e := godotenv.Read(path)
	if e != nil {
		t.Fatal("test configuration unavailable")
	}
	cfg, e := config.Load(func(k string) string { return env[k] })
	if e != nil {
		t.Fatal("invalid test configuration")
	}
	name := cfg.MySQLDatabase
	if cfg.DatabaseDriver == "postgres" {
		name = cfg.PostgresDatabase
	}
	if !strings.HasSuffix(name, "_test") {
		t.Fatal("isolated test DB required")
	}
	conn, e := platform.Open(cfg)
	if e != nil {
		t.Fatal("test database unavailable")
	}
	defer conn.Close()
	db := conn.DB
	ctx := context.Background()
	svc := New(db)
	uid := time.Now().UnixNano()%1000000000000000 + 1000000000000
	for i := int64(0); i < 3; i++ {
		if e = db.Create(&entity.User{ID: uid + i, DisplayName: "WeChat AA fixture", Status: "active"}).Error; e != nil {
			t.Fatal(e)
		}
	}
	in := Input{Title: "Dinner detail", TotalMinor: 10001, Mode: "equal", AlreadyPaid: true, PaymentMethod: "wechat_manual", ManagementMode: "collaborative", RecipientHint: "Owner - verify in WeChat", Memo: "AA dinner", Items: []LineItem{{Name: "Dinner", AmountMinor: 8000}, {Name: "Travel", AmountMinor: 2001}}, Participants: []ShareInput{{Name: "Payer"}}}
	b, e := svc.Create(ctx, uid, uuid.NewString(), in)
	if e != nil {
		t.Fatal(e)
	}
	if b.Participants[1].AmountMinor != 5000 || b.PaymentMethod != "wechat_manual" || len(b.Items) != 2 {
		t.Fatal("invalid immutable bill detail")
	}
	b, e = svc.Join(ctx, uid+1, b.Invitation, b.Participants[1].ID)
	if e != nil {
		t.Fatal(e)
	}
	if e = db.Transaction(func(tx *gorm.DB) error {
		_, _, _, _, _, e := Payment(tx, uid+1, PaymentInput{OwnerID: uid, BillID: b.ID, ParticipantID: b.Participants[1].ID, SourceAccountID: 1})
		return e
	}); entity.Code(e) != "STATE_CONFLICT" {
		t.Fatal("unpaid manual share reached simulated payment", e)
	}
	input := ReviewInput{ParticipantID: b.Participants[1].ID, Purpose: "report_paid", ExpectedVersion: b.Version}
	if _, e = svc.PrepareManual(ctx, uid+2, uid, b.ID, input, "fixture"); entity.Code(e) != "NOT_FOUND" {
		t.Fatal("nonmember read review", e)
	}
	if _, e = svc.PrepareManual(ctx, uid, uid, b.ID, input, "fixture"); entity.Code(e) != "FORBIDDEN" {
		t.Fatal("owner reported for payer", e)
	}
	review, e := svc.PrepareManual(ctx, uid+1, uid, b.ID, input, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	confirmation := func(r Review) ConfirmInput {
		return ConfirmInput{ReviewInput: ReviewInput{ParticipantID: r.ParticipantID, Purpose: r.Purpose, ExpectedVersion: r.Bill.Version}, IntentID: r.IntentID, Digest: r.Digest, Nonce: r.Nonce}
	}
	c := confirmation(review)
	bad := c
	bad.Digest = strings.Repeat("0", 64)
	if _, e = svc.ConfirmManual(ctx, uid+1, uid, b.ID, bad, "fixture"); entity.Code(e) != "STATE_CONFLICT" {
		t.Fatal("tampered confirmation accepted", e)
	}
	// Expire the actual persisted challenge, then require a fresh review.
	e = db.Transaction(func(tx *gorm.DB) error {
		if e := LockUsers(tx, uid); e != nil {
			return e
		}
		p, e := Load(tx, uid)
		if e != nil {
			return e
		}
		bill, e := Find(&p, b.ID)
		if e != nil {
			return e
		}
		bill.Shares[1].External.Report.ExpiresAt = 1
		return Save(tx, uid, p)
	})
	if e != nil {
		t.Fatal(e)
	}
	if _, e = svc.ConfirmManual(ctx, uid+1, uid, b.ID, c, "fixture"); entity.Code(e) != "STATE_CONFLICT" {
		t.Fatal("expired review accepted", e)
	}
	input.ExpectedVersion = review.Bill.Version
	review, e = svc.PrepareManual(ctx, uid+1, uid, b.ID, input, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	c = confirmation(review)
	// A rejected audit INSERT must roll back the participant state update.
	callback := "wechat_aa_test_audit_failure"
	if e = db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
		if tx.Statement.Table == "banking_execution_audits" {
			tx.AddError(errors.New("injected audit failure"))
		}
	}); e != nil {
		t.Fatal(e)
	}
	_, e = svc.ConfirmManual(ctx, uid+1, uid, b.ID, c, "fixture")
	_ = db.Callback().Create().Remove(callback)
	if e == nil {
		t.Fatal("audit failure did not roll back")
	}
	persisted, e := svc.Get(ctx, uid+1, uid, b.ID)
	if e != nil || persisted.Participants[1].Status != "joined" {
		t.Fatal("partial payment report after audit failure", e)
	}
	updateCallback := "wechat_aa_test_save_failure"
	if e = db.Callback().Update().Before("gorm:update").Register(updateCallback, func(tx *gorm.DB) {
		if tx.Statement.Table == "banking_users" {
			tx.AddError(errors.New("injected portfolio write failure"))
		}
	}); e != nil {
		t.Fatal(e)
	}
	_, e = svc.ConfirmManual(ctx, uid+1, uid, b.ID, c, "fixture")
	_ = db.Callback().Update().Remove(updateCallback)
	if e == nil {
		t.Fatal("portfolio save failure not propagated")
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := svc.ConfirmManual(ctx, uid+1, uid, b.ID, c, "fixture"); results <- e }()
	}
	wg.Wait()
	close(results)
	success := 0
	for e := range results {
		if e == nil {
			success++
		} else if entity.Code(e) != "STATE_CONFLICT" && entity.Code(e) != "VERSION_CONFLICT" {
			t.Fatal(e)
		}
	}
	if success != 1 {
		t.Fatal("duplicate concurrent report", success)
	}
	b, e = svc.Get(ctx, uid, uid, b.ID)
	if e != nil {
		t.Fatal(e)
	}
	if b.CollectedMinor != 0 || b.Participants[1].Status != "payment_reported" || b.PlatformVerified {
		t.Fatal("payer self-report treated as receipt")
	}
	receiptInput := ReviewInput{ParticipantID: b.Participants[1].ID, Purpose: "confirm_received", ExpectedVersion: b.Version}
	if _, e = svc.PrepareManual(ctx, uid+1, uid, b.ID, receiptInput, "fixture"); entity.Code(e) != "FORBIDDEN" {
		t.Fatal("payer confirmed own receipt", e)
	}
	receipt, e := svc.PrepareManual(ctx, uid, uid, b.ID, receiptInput, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	b, e = svc.ConfirmManual(ctx, uid, uid, b.ID, confirmation(receipt), "fixture")
	if e != nil {
		t.Fatal(e)
	}
	if b.Status != "settled" || b.CollectedMinor != 5000 || b.OutstandingMinor != 0 || b.PlatformVerified {
		t.Fatal("incorrect human receipt state")
	}
	if _, e = svc.ConfirmManual(ctx, uid, uid, b.ID, confirmation(receipt), "fixture"); entity.Code(e) != "STATE_CONFLICT" {
		t.Fatal("replay receipt", e)
	}
	// The external channel cannot be paid through the simulated AA ledger.
	if e = db.Transaction(func(tx *gorm.DB) error {
		_, _, _, _, _, e := Payment(tx, uid+1, PaymentInput{OwnerID: uid, BillID: b.ID, ParticipantID: b.Participants[1].ID, SourceAccountID: 1})
		return e
	}); entity.Code(e) != "STATE_CONFLICT" {
		t.Fatal("channel isolation", e)
	}
	for _, model := range []any{&entity.BankAccount{}, &entity.Transaction{}, &entity.LedgerEntry{}} {
		var count int64
		if e = db.Model(model).Where("user_id IN ?", []int64{uid, uid + 1, uid + 2}).Count(&count).Error; e != nil || count != 0 {
			t.Fatal("manual receipt created financial data", count, e)
		}
	}
	var audits int64
	if e = db.Model(&entity.AuditLog{}).Where("user_id IN ? AND operation_type IN ?", []int64{uid, uid + 1}, []string{"aa_wechat_report_paid", "aa_wechat_confirm_received"}).Count(&audits).Error; e != nil || audits != 5 {
		t.Fatal("audit trail mismatch", audits, e)
	}
	t.Log("real database: detail binding, expiry, ownership, rollback, concurrent dedupe, separate receipt, replay and no financial writes passed")
}
