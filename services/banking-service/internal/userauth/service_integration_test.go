//go:build integration

package userauth

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/platform"
	"github.com/redis/go-redis/v9"
)

func authHarness(t *testing.T) (*Service, *identity.Auth, *platform.Connections) {
	t.Helper()
	path := os.Getenv("BANKING_AUTH_TEST_ENV")
	if path == "" {
		t.Skip("BANKING_AUTH_TEST_ENV not configured")
	}
	env, err := godotenv.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	name := cfg.MySQLDatabase
	if cfg.DatabaseDriver == "postgres" {
		name = cfg.PostgresDatabase
	}
	if !strings.HasSuffix(name, "_test") {
		t.Fatal("authentication acceptance requires an isolated *_test database")
	}
	conn, err := platform.Open(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(conn.Close)
	var version int
	if err = conn.SQL.QueryRow("SELECT MAX(version) FROM banking_schema_migrations").Scan(&version); err != nil || version < 12 {
		t.Fatal("migration 012 is required before authentication acceptance")
	}
	a := identity.New(cfg.JWTSecret, cfg.JWTIssuer, cfg.JWTAudience, cfg.ToolToken, cfg.ToolUserID, cfg.ToolCozeUserID)
	return New(conn.DB, a, conn.Redis), a, conn
}

func uniquePhone() string { return fmt.Sprintf("139%08d", time.Now().UnixNano()%100000000) }

func TestPersistentPhonePasswordSessionLifecycle(t *testing.T) {
	s, a, conn := authHarness(t)
	ctx := context.Background()
	phone := uniquePhone()
	password := "持久化密码 123"
	first, err := s.Register(ctx, RegisterInput{Phone: phone, Password: password, DisplayName: "登录验收"}, "lifecycle-"+phone)
	if err != nil {
		t.Fatal(err)
	}
	if first.User.ID <= 0 || first.PhoneMasked != maskPhone("+86"+phone) || first.ExpiresIn != 900 {
		t.Fatal("invalid session response")
	}
	var record credentialRecord
	if err = conn.DB.First(&record, first.User.ID).Error; err != nil {
		t.Fatal(err)
	}
	if record.PasswordHash == password || !passwordMatches(record.PasswordHash, password) || record.RefreshHash == nil || *record.RefreshHash == first.RefreshToken {
		t.Fatal("credential persistence is unsafe")
	}
	if _, err = a.User(first.AccessToken); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Login(ctx, Credentials{Phone: phone, Password: "wrong-password"}, "lifecycle-"+phone); entity.Code(err) != "AUTH_INVALID_CREDENTIALS" {
		t.Fatalf("wrong password accepted: %v", err)
	}
	second, err := s.Login(ctx, Credentials{Phone: "+86" + phone, Password: password}, "lifecycle-"+phone)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.User(first.AccessToken); err == nil {
		t.Fatal("old login was not revoked")
	}
	if err = s.Logout(ctx, first.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if _, err = a.User(second.AccessToken); err != nil {
		t.Fatal("stale logout revoked newer session")
	}
	// A newly constructed service reads credentials and refresh sessions from SQL.
	b := identity.New(strings.Repeat("s", 32), "persisted", "banking", "tool", "42", "")
	restarted := New(conn.DB, b, conn.Redis)
	third, err := restarted.Refresh(ctx, second.RefreshToken)
	if err != nil || third.User.ID != first.User.ID {
		t.Fatalf("persistent refresh failed: %v", err)
	}
	if err = restarted.Logout(ctx, third.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if _, err = b.User(third.AccessToken); err == nil {
		t.Fatal("logout did not revoke access JWT")
	}
	if _, err = restarted.Refresh(ctx, third.RefreshToken); entity.Code(err) != "AUTH_INVALID_CREDENTIALS" {
		t.Fatalf("logged out refresh accepted: %v", err)
	}
	if err = restarted.Logout(ctx, third.RefreshToken); err != nil {
		t.Fatal("logout must be idempotent", err)
	}
}

func TestConcurrentRegistrationAndLoginLimit(t *testing.T) {
	s, _, _ := authHarness(t)
	ctx := context.Background()
	phone := uniquePhone()
	var successes atomic.Int32
	var conflicts atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := s.Register(ctx, RegisterInput{Phone: phone, Password: "password-123"}, "race-"+phone)
			if err == nil {
				successes.Add(1)
			} else if entity.Code(err) == "AUTH_PHONE_UNAVAILABLE" {
				conflicts.Add(1)
			} else {
				t.Errorf("registration: %v", err)
			}
		}()
	}
	wg.Wait()
	if successes.Load() != 1 || conflicts.Load() != 2 {
		t.Fatalf("created=%d conflicts=%d", successes.Load(), conflicts.Load())
	}
	for i := 0; i < 2; i++ {
		if _, err := s.Login(ctx, Credentials{Phone: phone, Password: "incorrect-pass"}, "race-"+phone); entity.Code(err) != "AUTH_INVALID_CREDENTIALS" {
			t.Fatalf("wrong password: %v", err)
		}
	}
	if _, err := s.Login(ctx, Credentials{Phone: phone, Password: "password-123"}, "race-"+phone); entity.Code(err) != "AUTH_RATE_LIMITED" {
		t.Fatalf("phone limiter failed: %v", err)
	}
}

func TestAuthenticatedBindingDoesNotClaimExistingAccounts(t *testing.T) {
	s, a, conn := authHarness(t)
	ctx := context.Background()
	phone := uniquePhone()
	uid := time.Now().UnixNano() / 10
	user := entity.User{ID: uid, DisplayName: "历史本人", Status: "active"}
	if err := conn.DB.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	legacy, err := a.IssueUser(fmt.Sprint(uid), "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	actor, err := a.User(legacy)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := s.Bind(ctx, actor, Credentials{Phone: phone, Password: "bind-password-123"}, "bind-"+phone)
	if err != nil || bound.User.ID != uid {
		t.Fatalf("binding failed: %v", err)
	}
	if _, err = s.Register(ctx, RegisterInput{Phone: phone, Password: "attacker-password"}, "bind-"+phone); entity.Code(err) != "AUTH_PHONE_UNAVAILABLE" {
		t.Fatalf("existing phone claimed: %v", err)
	}
	if _, err = s.Bind(ctx, actor, Credentials{Phone: uniquePhone(), Password: "replace-password"}, "bind-"+phone); entity.Code(err) != "STATE_CONFLICT" {
		t.Fatalf("bound credentials replaced without password: %v", err)
	}
}

func TestConcurrentLoginsLeaveOneAuthoritativeSession(t *testing.T) {
	s, a, _ := authHarness(t)
	ctx := context.Background()
	phone := uniquePhone()
	ip := "sessions-" + phone
	if _, err := s.Register(ctx, RegisterInput{Phone: phone, Password: "session-password-123"}, ip); err != nil {
		t.Fatal(err)
	}
	var sessions []Session
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, err := s.Login(ctx, Credentials{Phone: phone, Password: "session-password-123"}, ip)
			if err != nil {
				t.Errorf("concurrent login: %v", err)
				return
			}
			mu.Lock()
			sessions = append(sessions, v)
			mu.Unlock()
		}()
	}
	wg.Wait()
	if len(sessions) != 2 {
		t.Fatal("login did not complete")
	}
	valid := 0
	for _, v := range sessions {
		if _, err := a.User(v.AccessToken); err == nil {
			valid++
		}
	}
	if valid != 1 {
		t.Fatalf("valid sessions = %d", valid)
	}
}

func TestLogoutRevokesEvenExhaustedVersion(t *testing.T) {
	s, a, conn := authHarness(t)
	ctx := context.Background()
	phone := uniquePhone()
	v, err := s.Register(ctx, RegisterInput{Phone: phone, Password: "logout-password-123"}, "overflow-"+phone)
	if err != nil {
		t.Fatal(err)
	}
	if err = conn.DB.Model(&credentialRecord{}).Where("id=?", v.User.ID).Update("auth_version", int64(math.MaxInt64)).Error; err != nil {
		t.Fatal(err)
	}
	token, err := a.IssueSession(fmt.Sprint(v.User.ID), math.MaxInt64, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = a.User(token); err != nil {
		t.Fatal(err)
	}
	if err = s.Logout(ctx, v.RefreshToken); err != nil {
		t.Fatal(err)
	}
	if _, err = a.User(token); err == nil {
		t.Fatal("exhausted version prevented logout")
	}
}

func TestRedisIPLimitAndFailureAreEnforced(t *testing.T) {
	s, a, conn := authHarness(t)
	ctx := context.Background()
	nonce := fmt.Sprint(time.Now().UnixNano())
	ip := "limit-" + nonce
	for i := 0; i < 100; i++ {
		if err := s.rateLimit(ctx, nonce+fmt.Sprint(i), ip); err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
	}
	if err := s.rateLimit(ctx, nonce+"overflow", ip); entity.Code(err) != "AUTH_RATE_LIMITED" {
		t.Fatalf("IP limiter failed: %v", err)
	}
	broken := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 50 * time.Millisecond, ReadTimeout: 50 * time.Millisecond, WriteTimeout: 50 * time.Millisecond, MaxRetries: -1})
	defer broken.Close()
	unavailableService := New(conn.DB, a, broken)
	if _, err := unavailableService.Login(ctx, Credentials{Phone: uniquePhone(), Password: "never-check-password"}, ip); entity.Code(err) != "DEPENDENCY_UNAVAILABLE" {
		t.Fatalf("Redis failure did not fail closed: %v", err)
	}
}
