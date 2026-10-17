//go:build integration

package modelbudget

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/coze"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/platform"
)

func harness(t *testing.T, policy Policy) (*Service, *platform.Connections, int64) {
	t.Helper()
	path := os.Getenv("BANKING_AUTH_TEST_ENV")
	if path == "" {
		t.Skip("independent SQL test database required")
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
		t.Fatal("unsafe test database")
	}
	conn, e := platform.Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(conn.Close)
	uid := time.Now().UnixNano() / 1000
	if e = conn.DB.Create(&entity.User{ID: uid, DisplayName: "Synthetic quota", Status: "active"}).Error; e != nil {
		t.Fatal(e)
	}
	s, e := New(conn.DB, policy)
	if e != nil {
		t.Fatal(e)
	}
	return s, conn, uid
}
func TestDailyQuotaAtomicAndIsolatedAcrossUsers(t *testing.T) {
	s, conn, uid := harness(t, Policy{DailyCalls: 1, DailyTokens: 20, ReservationTokens: 10})
	ctx := context.Background()
	barrier := make(chan struct{})
	var wg sync.WaitGroup
	out := make(chan error, 2)
	for n := 0; n < 2; n++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-barrier
			_, _, e := s.Begin(ctx, uid, fmt.Sprintf("key-%d", i), "123", strings.Repeat("a", 64))
			out <- e
		}(n)
	}
	close(barrier)
	wg.Wait()
	close(out)
	accepted, denied := 0, 0
	for e := range out {
		if e == nil {
			accepted++
		} else if e == coze.ErrQuotaExceeded {
			denied++
		} else {
			t.Fatal(e)
		}
	}
	if accepted != 1 || denied != 1 {
		t.Fatalf("parallel overspend accepted=%d denied=%d", accepted, denied)
	}
	if e := conn.DB.Create(&entity.User{ID: uid + 1, DisplayName: "Quota B", Status: "active"}).Error; e != nil {
		t.Fatal(e)
	}
	if _, _, e := s.Begin(ctx, uid+1, "key-0", "123", strings.Repeat("a", 64)); e != nil {
		t.Fatal("A quota leaked into B", e)
	}
}
func TestCompletedCallCachesAndCountsOnce(t *testing.T) {
	s, _, uid := harness(t, Policy{DailyCalls: 1, DailyTokens: 10, ReservationTokens: 10})
	ctx := context.Background()
	hash := strings.Repeat("a", 64)
	id, _, e := s.Begin(ctx, uid, "same-task", "123", hash)
	if e != nil {
		t.Fatal(e)
	}
	used := int64(3)
	raw := []byte(`{"code":0,"execute_id":"456","data":"{}","token":3}`)
	if e = s.Complete(ctx, uid, id, raw, &used, "0.00000", true); e != nil {
		t.Fatal(e)
	}
	if e = s.Complete(ctx, uid, id, raw, &used, "0.00000", true); e != nil {
		t.Fatal("duplicate settlement", e)
	}
	_, cache, e := s.Begin(ctx, uid, "same-task", "123", hash)
	if e != nil || string(cache) != string(raw) {
		t.Fatal("completed retry lost cached result", e)
	}
	if _, _, e = s.Begin(ctx, uid, "different-task", "123", hash); e != coze.ErrQuotaExceeded {
		t.Fatal("call limit bypassed", e)
	}
	if _, _, e = s.Begin(ctx, uid, "same-task", "123", strings.Repeat("b", 64)); e != coze.ErrRequestConflict {
		t.Fatal("same key reused with changed content", e)
	}
}
func TestUnknownAndMissingUsageRemainReservedAcrossRestart(t *testing.T) {
	s, conn, uid := harness(t, Policy{DailyCalls: 10, DailyTokens: 20, ReservationTokens: 10})
	ctx := context.Background()
	hash := strings.Repeat("a", 64)
	first, _, e := s.Begin(ctx, uid, "unknown", "123", hash)
	if e != nil {
		t.Fatal(e)
	}
	if e = s.Complete(ctx, uid, first, nil, nil, "", false); e != nil {
		t.Fatal(e)
	}
	restarted, _ := New(conn.DB, s.policy)
	if _, _, e = restarted.Begin(ctx, uid, "unknown", "123", hash); e != coze.ErrOutcomeUnknown {
		t.Fatal("unknown request retried after restart", e)
	}
	next, _, e := restarted.Begin(ctx, uid, "missing-usage", "123", hash)
	if e != nil {
		t.Fatal(e)
	}
	if e = restarted.Complete(ctx, uid, next, []byte(`{"code":0,"execute_id":"456","data":"{}"}`), nil, "", true); e != nil {
		t.Fatal(e)
	}
	if _, _, e = restarted.Begin(ctx, uid, "third", "123", hash); e != coze.ErrQuotaExceeded {
		t.Fatal("missing usage returned reserved quota", e)
	}
}
func TestReportedTokenDebtAndInactiveUserFailClosed(t *testing.T) {
	s, conn, uid := harness(t, Policy{DailyCalls: 10, DailyTokens: 20, ReservationTokens: 10})
	ctx := context.Background()
	hash := strings.Repeat("a", 64)
	id, _, e := s.Begin(ctx, uid, "large-real-usage", "123", hash)
	if e != nil {
		t.Fatal(e)
	}
	used := int64(25)
	if e = s.Complete(ctx, uid, id, []byte(`{"code":0,"execute_id":"456","data":"{}"}`), &used, "0.00000", true); e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.Begin(ctx, uid, "after-debt", "123", hash); e != coze.ErrQuotaExceeded {
		t.Fatal("oversized actual usage did not block next request", e)
	}
	if e = conn.DB.Model(&entity.User{}).Where("id=?", uid).Update("status", "inactive").Error; e != nil {
		t.Fatal(e)
	}
	if _, _, e = s.Begin(ctx, uid, "large-real-usage", "123", hash); e == nil {
		t.Fatal("inactive user obtained model response cache")
	}
}

func TestStorageFreezePreservesModelReservations(t *testing.T) {
	if os.Getenv("BANKING_BUDGET_TEST_ALLOW_FENCE") != "1" {
		t.Skip("operator-only freeze test requires an exclusive test database")
	}
	s, conn, uid := harness(t, Policy{DailyCalls: 10, DailyTokens: 20, ReservationTokens: 10})
	ctx := context.Background()
	hash := strings.Repeat("a", 64)
	id, _, e := s.Begin(ctx, uid, "before-freeze", "123", hash)
	if e != nil {
		t.Fatal(e)
	}
	if e = conn.DB.Exec("UPDATE banking_storage_control SET mode='frozen' WHERE id=1").Error; e != nil {
		t.Fatal(e)
	}
	defer conn.DB.Exec("UPDATE banking_storage_control SET mode='active' WHERE id=1")
	if _, _, e = s.Begin(ctx, uid, "during-freeze", "123", hash); e != coze.ErrBudgetUnavailable {
		t.Fatal("frozen database admitted model work", e)
	}
	used := int64(1)
	if e = s.Complete(ctx, uid, id, []byte(`{"code":0,"execute_id":"456","data":"{}"}`), &used, "0.00000", true); e != coze.ErrBudgetUnavailable {
		t.Fatal("frozen database silently settled quota", e)
	}
	var window Window
	if e = conn.DB.Where("user_id=?", uid).Take(&window).Error; e != nil {
		t.Fatal(e)
	}
	if window.CallsUsed != 1 || window.TokensReserved != 10 || window.TokensUsed != 0 {
		t.Fatal("freeze changed quota allocation", window)
	}
}
