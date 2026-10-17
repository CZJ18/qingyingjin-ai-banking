//go:build integration

package smsverify

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/redis/go-redis/v9"
)

type captureSender struct {
	mu      sync.Mutex
	code    string
	failure bool
}

func (*captureSender) Mode() string { return "development" }
func (s *captureSender) SendCode(_ context.Context, _, code, _ string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.code = code
	if s.failure {
		return fmt.Errorf("test delivery rejected")
	}
	return nil
}
func (s *captureSender) captured() string { s.mu.Lock(); defer s.mu.Unlock(); return s.code }
func verifierHarness(t *testing.T) (*Verifier, *captureSender, string) {
	t.Helper()
	path := os.Getenv("BANKING_SMS_TEST_ENV")
	if path == "" {
		t.Skip("BANKING_SMS_TEST_ENV not configured")
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
		t.Fatal("isolated test configuration required")
	}
	cache := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Password: cfg.RedisPassword, Username: cfg.RedisUsername, DB: cfg.RedisDB})
	t.Cleanup(func() { _ = cache.Close() })
	if cache.Ping(context.Background()).Err() != nil {
		t.Fatal("Redis unavailable")
	}
	secret, err := randomID()
	if err != nil {
		t.Fatal(err)
	}
	sender := &captureSender{}
	v := New(cache, sender, secret)
	phone := fmt.Sprintf("+86189%08d", time.Now().UnixNano()%100000000)
	return v, sender, phone
}
func TestOTPRealRedisOneUsePurposeAndExpiration(t *testing.T) {
	v, s, phone := verifierHarness(t)
	ctx := context.Background()
	c, err := v.Issue(ctx, phone, RegisterPhone, "otp-lifecycle", Binding{})
	if err != nil {
		t.Fatal(err)
	}
	if c.ExpiresIn != 300 || c.RetryAfter != 60 {
		t.Fatal("invalid challenge lifetime")
	}
	raw, _ := json.Marshal(c)
	if strings.Contains(string(raw), s.captured()) {
		t.Fatal("OTP exposed in response")
	}
	if _, err = v.Consume(ctx, phone, ResetPassword, c.ChallengeID, s.captured(), "otp-lifecycle"); entity.Code(err) != "AUTH_CODE_INVALID" {
		t.Fatal("purpose confusion")
	}
	if _, err = v.Issue(ctx, phone, RegisterPhone, "otp-lifecycle", Binding{}); entity.Code(err) != "AUTH_RATE_LIMITED" {
		t.Fatal("send cooldown missing")
	}
	if _, err = v.Consume(ctx, phone, RegisterPhone, c.ChallengeID, s.captured(), "otp-lifecycle"); err != nil {
		t.Fatal(err)
	}
	if _, err = v.Consume(ctx, phone, RegisterPhone, c.ChallengeID, s.captured(), "otp-lifecycle"); entity.Code(err) != "AUTH_CODE_INVALID" {
		t.Fatal("replayed OTP")
	}
	_ = v.cache.Del(ctx, v.key("cooldown", phone)).Err()
	c, err = v.Issue(ctx, phone, VerifyPhone, "otp-lifecycle", Binding{UserID: "42", AuthVersion: 7})
	if err != nil {
		t.Fatal(err)
	}
	if ttl := v.cache.TTL(ctx, v.key("challenge", c.ChallengeID)).Val(); ttl > 5*time.Minute || ttl < 4*time.Minute {
		t.Fatal("invalid Redis expiry")
	}
	_ = v.cache.PExpire(ctx, v.key("challenge", c.ChallengeID), time.Millisecond).Err()
	time.Sleep(3 * time.Millisecond)
	if _, err = v.Consume(ctx, phone, VerifyPhone, c.ChallengeID, s.captured(), "otp-lifecycle"); entity.Code(err) != "AUTH_CODE_INVALID" {
		t.Fatal("expired challenge accepted")
	}
}
func TestOTPRealRedisConcurrentConsumptionAndRetryLimit(t *testing.T) {
	v, s, phone := verifierHarness(t)
	ctx := context.Background()
	c, err := v.Issue(ctx, phone, VerifyPhone, "otp-concurrent", Binding{UserID: "42", AuthVersion: 8})
	if err != nil {
		t.Fatal(err)
	}
	var success atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, e := v.Consume(ctx, phone, VerifyPhone, c.ChallengeID, s.captured(), "otp-concurrent")
			if e == nil {
				if b.UserID != "42" || b.AuthVersion != 8 {
					t.Error("lost binding")
				}
				success.Add(1)
			} else if entity.Code(e) != "AUTH_CODE_INVALID" {
				t.Error(e)
			}
		}()
	}
	wg.Wait()
	if success.Load() != 1 {
		t.Fatal("OTP consumed multiple times")
	}
	_ = v.cache.Del(ctx, v.key("cooldown", phone)).Err()
	c, err = v.Issue(ctx, phone, RegisterPhone, "otp-concurrent", Binding{})
	if err != nil {
		t.Fatal(err)
	}
	wrong := "000000"
	if s.captured() == wrong {
		wrong = "999999"
	}
	for i := 0; i < 5; i++ {
		if _, err = v.Consume(ctx, phone, RegisterPhone, c.ChallengeID, wrong, "otp-concurrent"); entity.Code(err) != "AUTH_CODE_INVALID" {
			t.Fatal(err)
		}
	}
	if _, err = v.Consume(ctx, phone, RegisterPhone, c.ChallengeID, s.captured(), "otp-concurrent"); entity.Code(err) != "AUTH_CODE_INVALID" {
		t.Fatal("retry limit bypassed")
	}
}
func TestOTPRealRedisFailedDeliveryAndResendInvalidateCode(t *testing.T) {
	v, s, phone := verifierHarness(t)
	ctx := context.Background()
	s.failure = true
	if _, err := v.Issue(ctx, phone, RegisterPhone, "otp-failure", Binding{}); entity.Code(err) != "SMS_UNAVAILABLE" {
		t.Fatal("fake send success")
	}
	if v.cache.Exists(ctx, v.key("active", phone+"|register")).Val() != 0 {
		t.Fatal("failed delivery left active challenge")
	}
	s.failure = false
	_ = v.cache.Del(ctx, v.key("cooldown", phone)).Err()
	first, err := v.Issue(ctx, phone, RegisterPhone, "otp-failure", Binding{})
	if err != nil {
		t.Fatal(err)
	}
	old := s.captured()
	_ = v.cache.Del(ctx, v.key("cooldown", phone)).Err()
	second, err := v.Issue(ctx, phone, RegisterPhone, "otp-failure", Binding{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = v.Consume(ctx, phone, RegisterPhone, first.ChallengeID, old, "otp-failure"); entity.Code(err) != "AUTH_CODE_INVALID" {
		t.Fatal("old resend challenge accepted")
	}
	if _, err = v.Consume(ctx, phone, RegisterPhone, second.ChallengeID, s.captured(), "otp-failure"); err != nil {
		t.Fatal(err)
	}
	_ = v.cache.Close()
	if _, err = v.Issue(ctx, phone, RegisterPhone, "otp-failure", Binding{}); entity.Code(err) != "DEPENDENCY_UNAVAILABLE" {
		t.Fatal("Redis outage accepted")
	}
}
