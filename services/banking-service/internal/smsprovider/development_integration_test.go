//go:build integration

package smsprovider

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/redis/go-redis/v9"
)

func inboxHarness(t *testing.T) (*Development, string) {
	t.Helper()
	path := os.Getenv("BANKING_SMS_TEST_ENV")
	if path == "" {
		t.Skip("BANKING_SMS_TEST_ENV not configured; no provider delivery was attempted")
	}
	env, err := godotenv.Read(path)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	database := cfg.MySQLDatabase
	if cfg.DatabaseDriver == "postgres" {
		database = cfg.PostgresDatabase
	}
	if !strings.HasSuffix(database, "_test") {
		t.Fatal("inbox acceptance requires an isolated *_test configuration")
	}
	cache := redis.NewClient(&redis.Options{Addr: cfg.RedisAddr, Username: cfg.RedisUsername, Password: cfg.RedisPassword, DB: cfg.RedisDB, MaxRetries: -1, ContextTimeoutEnabled: true, DialTimeout: 3 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second})
	t.Cleanup(func() { _ = cache.Close() })
	if err := cache.Ping(context.Background()).Err(); err != nil {
		t.Fatal("test Redis is unavailable")
	}
	d, err := NewDevelopment(cache, strings.Repeat("acceptance-only-inbox-key-", 2))
	if err != nil {
		t.Fatal(err)
	}
	phone := fmt.Sprintf("+86139%08d", time.Now().UnixNano()%100000000)
	t.Cleanup(func() {
		for _, purpose := range []string{"register", "verify_phone", "password_reset"} {
			_ = cache.Del(context.Background(), d.key(phone, purpose)).Err()
		}
	})
	return d, phone
}

func TestDevelopmentInboxRealRedisEncryptedPersistenceAndPurposeIsolation(t *testing.T) {
	d, phone := inboxHarness(t)
	ctx := context.Background()
	if err := d.SendCode(ctx, phone, "123456", "register"); err != nil {
		t.Fatal(err)
	}
	if err := d.SendCode(ctx, phone, "654321", "password_reset"); err != nil {
		t.Fatal(err)
	}
	key := d.key(phone, "register")
	raw, err := d.cache.Get(ctx, key).Bytes()
	if err != nil {
		t.Fatal("encrypted development message was not persisted")
	}
	if strings.Contains(string(raw), phone) || strings.Contains(string(raw), "123456") {
		t.Fatal("Redis contains plaintext recipient or OTP")
	}
	ttl, err := d.cache.TTL(ctx, key).Result()
	if err != nil || ttl > 5*time.Minute || ttl < 4*time.Minute {
		t.Fatal("inbox TTL is not bounded to five minutes")
	}
	// Constructing a new adapter proves reads are persistent, rather than an in-memory stub.
	restarted, err := NewDevelopment(d.cache, strings.Repeat("acceptance-only-inbox-key-", 2))
	if err != nil {
		t.Fatal(err)
	}
	got, err := restarted.Read(ctx, phone, "register")
	if err != nil || got.Code != "123456" || got.Phone != phone || got.Purpose != "register" || time.Until(got.ExpiresAt) > 5*time.Minute {
		t.Fatal("persisted development message did not round trip")
	}
	reset, err := restarted.Read(ctx, phone, "password_reset")
	if err != nil || reset.Code != "654321" {
		t.Fatal("separate OTP purposes collided")
	}
	if _, err := restarted.Read(ctx, phone, "verify_phone"); entity.Code(err) != "SMS_MESSAGE_NOT_FOUND" {
		t.Fatal("nonexistent message returned as delivered")
	}
}

func TestDevelopmentInboxRealRedisRejectsTamperingAndExpiry(t *testing.T) {
	d, phone := inboxHarness(t)
	ctx := context.Background()
	if err := d.SendCode(ctx, phone, "123456", "register"); err != nil {
		t.Fatal(err)
	}
	key := d.key(phone, "register")
	raw, err := d.cache.Get(ctx, key).Bytes()
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 1
	if err := d.cache.Set(ctx, key, raw, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Read(ctx, phone, "register"); entity.Code(err) != "SMS_UNAVAILABLE" {
		t.Fatal("tampered inbox message was accepted")
	}
	old, err := d.seal(InboxMessage{Phone: phone, Code: "123456", Purpose: "register", ExpiresAt: time.Now().UTC().Add(-time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if err := d.cache.Set(ctx, key, old, time.Minute).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Read(ctx, phone, "register"); entity.Code(err) != "SMS_MESSAGE_NOT_FOUND" {
		t.Fatal("expired ciphertext was accepted")
	}
	if err := d.SendCode(ctx, phone, "123456", "register"); err != nil {
		t.Fatal(err)
	}
	if err := d.cache.PExpire(ctx, key, time.Millisecond).Err(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if _, err := d.Read(ctx, phone, "register"); entity.Code(err) != "SMS_MESSAGE_NOT_FOUND" {
		t.Fatal("Redis TTL did not expire inbox message")
	}
}

func TestDevelopmentInboxRealRedisCanceledRequestFailsClosed(t *testing.T) {
	d, phone := inboxHarness(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := d.SendCode(ctx, phone, "123456", "register"); entity.Code(err) != "SMS_UNAVAILABLE" {
		t.Fatal("canceled write did not fail closed")
	}
	if _, err := d.Read(ctx, phone, "register"); entity.Code(err) != "SMS_UNAVAILABLE" {
		t.Fatal("canceled read did not fail closed")
	}
	if d.cache.Exists(context.Background(), d.key(phone, "register")).Val() != 0 {
		t.Fatal("canceled send persisted OTP")
	}
}
