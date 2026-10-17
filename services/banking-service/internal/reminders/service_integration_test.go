//go:build integration

package reminders

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/migration"
	"github.com/qingyingjin/banking-service/internal/platform"
	"gorm.io/gorm"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestRenewalsDatabase(t *testing.T) {
	path := os.Getenv("BANKING_REMINDER_TEST_ENV")
	if path == "" {
		t.Skip("isolated BANKING_REMINDER_TEST_ENV required")
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
		t.Fatal("isolated test DB required")
	}
	c, e := platform.Open(cfg)
	if e != nil {
		t.Fatal("database unavailable")
	}
	defer c.Close()
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if e = migration.Apply(ctx, c.SQL); e != nil {
			t.Fatal(e)
		}
	}
	uid := time.Now().UnixNano()%1000000000000000 + 1000000000000
	for _, id := range []int64{uid, uid + 1} {
		if e = c.DB.Create(&entity.User{ID: id, Status: "active", DisplayName: "Renewal test"}).Error; e != nil {
			t.Fatal(e)
		}
	}
	a := entity.BankAccount{Base: entity.Base{UserID: uid}, DisplayName: "Renewal fixture", AccountType: "checking", MaskedAccountNumber: "SIM ****1234", Currency: "CNY", Status: "active", Version: 1, IsSimulated: true}
	if e = c.DB.Create(&a).Error; e != nil {
		t.Fatal(e)
	}
	sub := entity.Subscription{Base: entity.Base{UserID: uid}, BankAccountID: a.ID, Fingerprint: uuid.NewString(), MerchantName: "Renewal service", Category: "subscription", BillingInterval: "monthly", AmountMinor: 2000, Currency: "CNY", NextBillingAt: time.Now().Add(48 * time.Hour).UnixMilli(), ConfidenceBPS: 10000, Status: "active", Version: 1, IsSimulated: true}
	if e = c.DB.Create(&sub).Error; e != nil {
		t.Fatal(e)
	}
	svc := New(c.DB)
	t.Run("get_does_not_generate", func(t *testing.T) {
		v, e := svc.Read(ctx, uid)
		if e != nil || len(v.Items) != 0 || v.LastCheckedAt != 0 {
			t.Fatal(v, e)
		}
	})
	t.Run("concurrent_scan_deduplicates", func(t *testing.T) {
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); _, e := svc.Generate(ctx, uid, time.Now()); errs <- e }()
		}
		wg.Wait()
		close(errs)
		for e := range errs {
			if e != nil {
				t.Fatal(e)
			}
		}
		v, e := svc.Read(ctx, uid)
		if e != nil || len(v.Items) != 1 || v.UnreadCount != 1 {
			t.Fatal(v, e)
		}
	})
	t.Run("owner_and_read_retry", func(t *testing.T) {
		v, _ := svc.Read(ctx, uid)
		n := v.Items[0]
		if _, e := svc.ReadNotice(ctx, uid+1, n.ID, n.Version); entity.Code(e) != "NOT_FOUND" {
			t.Fatal(e)
		}
		for i := 0; i < 2; i++ {
			v, e = svc.ReadNotice(ctx, uid, n.ID, n.Version)
			if e != nil || v.UnreadCount != 0 {
				t.Fatal(v, e)
			}
		}
	})
	t.Run("price_change_invalidates_old_ack", func(t *testing.T) {
		v, _ := svc.Read(ctx, uid)
		n := v.Items[0]
		if e := c.DB.Model(&sub).Update("amount_minor", 2500).Error; e != nil {
			t.Fatal(e)
		}
		if _, e := svc.ReadNotice(ctx, uid, n.ID, n.Version); entity.Code(e) != "VERSION_CONFLICT" {
			t.Fatal(e)
		}
		v, e := svc.Generate(ctx, uid, time.Now())
		if e != nil || v.Items[0].AmountMinor != 2500 || v.UnreadCount != 1 {
			t.Fatal(v, e)
		}
	})
	t.Run("disable_and_optimistic_conflict", func(t *testing.T) {
		v, e := svc.Configure(ctx, uid, false, 3, 1)
		if e != nil || v.UnreadCount != 0 || v.Items[0].Status != "withdrawn" {
			t.Fatal(v, e)
		}
		if _, e := svc.Configure(ctx, uid, true, 3, 1); entity.Code(e) != "VERSION_CONFLICT" {
			t.Fatal(e)
		}
		if _, e := svc.Configure(ctx, uid, true, 31, 2); entity.Code(e) != "INVALID_ARGUMENT" {
			t.Fatal(e)
		}
		if _, e := svc.Configure(ctx, uid, true, 3, 2); e != nil {
			t.Fatal(e)
		}
	})
	t.Run("background_worker_and_restart", func(t *testing.T) {
		if e := c.DB.Model(&sub).Update("merchant_name", "Changed service").Error; e != nil {
			t.Fatal(e)
		}
		run, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			defer close(done)
			svc.Run(run, slog.New(slog.NewTextHandler(io.Discard, nil)), 20*time.Millisecond)
		}()
		defer func() { cancel(); <-done }()
		deadline := time.Now().Add(5 * time.Second)
		for {
			v, e := New(c.DB).Read(ctx, uid)
			if e != nil {
				t.Fatal(e)
			}
			if len(v.Items) == 1 && v.Items[0].MerchantName == "Changed service" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("background did not persist notice")
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	t.Run("cancel_and_reschedule", func(t *testing.T) {
		if e := c.DB.Model(&sub).Update("next_billing_at", time.Now().Add(72*time.Hour).UnixMilli()).Error; e != nil {
			t.Fatal(e)
		}
		v, e := svc.Generate(ctx, uid, time.Now())
		if e != nil || len(v.Items) != 2 || v.UnreadCount != 1 {
			t.Fatal(v, e)
		}
		if e := c.DB.Model(&sub).Update("status", "cancelled").Error; e != nil {
			t.Fatal(e)
		}
		v, e = svc.Generate(ctx, uid, time.Now())
		if e != nil || v.UnreadCount != 0 {
			t.Fatal(v, e)
		}
	})
	t.Run("storage_fence_blocks_writes", func(t *testing.T) {
		rollback := errors.New("test rollback")
		e := c.DB.Transaction(func(tx *gorm.DB) error {
			if e := tx.Exec("UPDATE banking_storage_control SET mode='frozen' WHERE id=1").Error; e != nil {
				return e
			}
			if _, e := New(tx).Configure(ctx, uid, false, 3, 3); e == nil {
				return errors.New("frozen storage accepted reminder mutation")
			}
			return rollback
		})
		if e != rollback {
			t.Fatal(e)
		}
		v, e := svc.Read(ctx, uid)
		if e != nil || !v.Settings.Enabled || v.Settings.Version != 3 {
			t.Fatal("frozen update escaped rollback", v, e)
		}
	})
	t.Run("no_money_or_ledger_mutation", func(t *testing.T) {
		var account entity.BankAccount
		if e := c.DB.First(&account, a.ID).Error; e != nil {
			t.Fatal(e)
		}
		var count int64
		if e := c.DB.Model(&entity.LedgerEntry{}).Where("user_id = ?", uid).Count(&count).Error; e != nil {
			t.Fatal(e)
		}
		if account.BalanceMinor != 0 || count != 0 {
			t.Fatal("reminders changed money")
		}
	})
}
