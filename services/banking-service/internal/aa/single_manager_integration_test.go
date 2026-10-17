//go:build integration

package aa

import (
	"context"
	"os"
	"strconv"
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

func TestSingleManagerDatabase(t *testing.T) {
	path := os.Getenv("BANKING_AA_TEST_ENV")
	if path == "" {
		t.Skip("isolated BANKING_AA_TEST_ENV required")
	}
	env, e := godotenv.Read(path)
	if e != nil {
		t.Fatal("test config unavailable")
	}
	cfg, e := config.Load(func(k string) string { return env[k] })
	if e != nil {
		t.Fatal("test config invalid")
	}
	name := cfg.MySQLDatabase
	if cfg.DatabaseDriver == "postgres" {
		name = cfg.PostgresDatabase
	}
	if !strings.HasSuffix(name, "_test") {
		t.Fatal("isolated DB required")
	}
	conn, e := platform.Open(cfg)
	if e != nil {
		t.Fatal("test database unavailable")
	}
	defer conn.Close()
	db := conn.DB
	svc := New(db)
	ctx := context.Background()
	uid := time.Now().UnixNano()%1000000000000000 + 1000000000000
	for i := int64(0); i < 2; i++ {
		if e = db.Create(&entity.User{ID: uid + i, Status: "active", DisplayName: "Single manager fixture"}).Error; e != nil {
			t.Fatal(e)
		}
	}
	in := Input{Title: "One account", PaymentMethod: "wechat_manual", RecipientHint: "Owner", TotalMinor: 100, Mode: "equal", AlreadyPaid: true, Items: []LineItem{{Name: "Dinner", AmountMinor: 100}}, Participants: []ShareInput{{Name: "Alice"}, {Name: "Bob"}}}
	key := uuid.NewString()
	b, e := svc.Create(ctx, uid, key, in)
	if e != nil {
		t.Fatal(e)
	}
	if b.ManagementMode != "single_manager" || b.Invitation != "" || b.Participants[1].Status != "awaiting_receipt" || b.Participants[1].IsMe || b.OutstandingMinor != 66 {
		t.Fatal("incorrect default single manager")
	}
	again, e := svc.Create(ctx, uid, key, in)
	if e != nil || again.ID != b.ID {
		t.Fatal("create replay", e)
	}
	changed := in
	changed.ManagementMode = "collaborative"
	if _, e = svc.Create(ctx, uid, key, changed); entity.Code(e) != "IDEMPOTENCY_CONFLICT" {
		t.Fatal("mode changed on create replay", e)
	}
	if _, e = svc.Get(ctx, uid+1, uid, b.ID); entity.Code(e) != "NOT_FOUND" {
		t.Fatal("private single bill accessible", e)
	}
	portfolio, e := Load(db, uid)
	if e != nil {
		t.Fatal(e)
	}
	stored, e := Find(&portfolio, b.ID)
	if e != nil {
		t.Fatal(e)
	}
	// Even a recovered private invitation is not sufficient to join a single-manager bill.
	invite := strings.Join([]string{strconv.FormatInt(uid, 10), b.ID, stored.Invite}, ".")
	if _, e = svc.Preview(ctx, uid+1, invite); entity.Code(e) != "STATE_CONFLICT" {
		t.Fatal("single-manager invite preview", e)
	}
	if _, e = svc.Join(ctx, uid+1, invite, b.Participants[1].ID); entity.Code(e) != "STATE_CONFLICT" {
		t.Fatal("single-manager claim", e)
	}
	input := ReviewInput{ParticipantID: b.Participants[1].ID, Purpose: "confirm_received", ExpectedVersion: b.Version}
	if _, e = svc.PrepareManual(ctx, uid+1, uid, b.ID, input, "fixture"); entity.Code(e) != "NOT_FOUND" {
		t.Fatal("nonowner receipt", e)
	}
	review, e := svc.PrepareManual(ctx, uid, uid, b.ID, input, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	c := ConfirmInput{ReviewInput: ReviewInput{ParticipantID: review.ParticipantID, Purpose: review.Purpose, ExpectedVersion: review.Bill.Version}, IntentID: review.IntentID, Digest: review.Digest, Nonce: review.Nonce}
	bad := c
	bad.ParticipantID = b.Participants[2].ID
	if _, e = svc.ConfirmManual(ctx, uid, uid, b.ID, bad, "fixture"); entity.Code(e) != "STATE_CONFLICT" {
		t.Fatal("different share accepted", e)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, e := svc.ConfirmManual(ctx, uid, uid, b.ID, c, "fixture"); results <- e }()
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
		t.Fatal("duplicate manager receipt", success)
	}
	b, e = svc.Get(ctx, uid, uid, b.ID)
	if e != nil || b.CollectedMinor != 33 || b.OutstandingMinor != 33 || b.Participants[1].ReportedAt != 0 || b.Participants[1].ReceivedAt == 0 || b.PlatformVerified {
		t.Fatal("invented payer report or receipt totals", e)
	}
	portfolio, e = Load(db, uid)
	if e != nil {
		t.Fatal(e)
	}
	stored, e = Find(&portfolio, b.ID)
	if e != nil {
		t.Fatal(e)
	}
	if stored.Shares[1].UserID != 0 || stored.Shares[1].External.Report != nil || stored.Shares[1].External.ReportedAt != 0 {
		t.Fatal("created phantom participant identity")
	}
	// Cancelled remaining shares cannot be confirmed with a previously prepared review.
	input.ParticipantID = b.Participants[2].ID
	input.ExpectedVersion = b.Version
	r, e := svc.PrepareManual(ctx, uid, uid, b.ID, input, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	if _, e = svc.Cancel(ctx, uid, b.ID, r.Bill.Version); e != nil {
		t.Fatal(e)
	}
	c = ConfirmInput{ReviewInput: ReviewInput{ParticipantID: r.ParticipantID, Purpose: r.Purpose, ExpectedVersion: r.Bill.Version}, IntentID: r.IntentID, Digest: r.Digest, Nonce: r.Nonce}
	if _, e = svc.ConfirmManual(ctx, uid, uid, b.ID, c, "fixture"); entity.Code(e) != "STATE_CONFLICT" {
		t.Fatal("cancelled share confirmed", e)
	}
	// Explicit collaborative creation and stored historical omissions remain collaborative.
	in.ManagementMode = "collaborative"
	legacy, e := svc.Create(ctx, uid, uuid.NewString(), in)
	if e != nil || legacy.Invitation == "" {
		t.Fatal("collaborative unavailable", e)
	}
	e = db.Transaction(func(tx *gorm.DB) error {
		p, e := Load(tx, uid)
		if e != nil {
			return e
		}
		v, e := Find(&p, legacy.ID)
		if e != nil {
			return e
		}
		v.ManagementMode = ""
		return Save(tx, uid, p)
	})
	if e != nil {
		t.Fatal(e)
	}
	old, e := svc.Get(ctx, uid, uid, legacy.ID)
	if e != nil || old.ManagementMode != "collaborative" || old.Invitation == "" {
		t.Fatal("historical mode changed", e)
	}
	for _, m := range []any{&entity.BankAccount{}, &entity.Transaction{}, &entity.LedgerEntry{}} {
		var count int64
		if e = db.Model(m).Where("user_id=?", uid).Count(&count).Error; e != nil || count != 0 {
			t.Fatal("manager receipt wrote finances", e)
		}
	}
	var reports int64
	if e = db.Model(&entity.AuditLog{}).Where("user_id=? AND operation_type=?", uid, "aa_wechat_report_paid").Count(&reports).Error; e != nil || reports != 0 {
		t.Fatal("manager impersonated payer in audit", e)
	}
	t.Log("single manager default, owner-only bound receipt, concurrent dedupe, invitation isolation, no participant account/report, cancellation, old-mode compatibility and no financial writes passed")
}
