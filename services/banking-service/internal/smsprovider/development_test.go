package smsprovider

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/redis/go-redis/v9"
)

func TestDevelopmentRequiresRedisAndIndependentSecret(t *testing.T) {
	cache := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	t.Cleanup(func() { _ = cache.Close() })
	for _, tc := range []struct {
		name   string
		cache  *redis.Client
		secret string
	}{
		{"nil-redis", nil, strings.Repeat("s", 32)},
		{"short-secret", cache, "short"},
		{"template-secret", cache, "CHANGE_ME_SECRET_AT_LEAST_32_BYTES"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := NewDevelopment(tc.cache, tc.secret); err == nil {
				t.Fatal("accepted unsafe dependency")
			}
		})
	}
}

func TestDevelopmentInboxAuthenticatedEncryption(t *testing.T) {
	d, err := NewDevelopment(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}), strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.cache.Close() })
	if d.Mode() != "development" {
		t.Fatal("development sender claims a real delivery provider")
	}
	message := InboxMessage{Phone: "+8613900000001", Code: "123456", Purpose: "register", ExpiresAt: time.Now().UTC().Add(time.Minute).Truncate(time.Millisecond)}
	first, err := d.seal(message)
	if err != nil {
		t.Fatal(err)
	}
	second, err := d.seal(message)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) == string(second) {
		t.Fatal("encryption reused nonce")
	}
	for _, sensitive := range []string{message.Phone, message.Code, message.Purpose} {
		if strings.Contains(string(first), sensitive) {
			t.Fatal("plaintext in ciphertext")
		}
	}
	got, err := d.open(first, message.Phone, message.Purpose)
	if err != nil || got != message {
		t.Fatal("encrypted message does not round trip")
	}
	if _, err := d.open(first, message.Phone, "password_reset"); err == nil {
		t.Fatal("purpose substitution accepted")
	}
	if _, err := d.open(first, "+8613900000002", message.Purpose); err == nil {
		t.Fatal("recipient substitution accepted")
	}
	first[len(first)-1] ^= 1
	if _, err := d.open(first, message.Phone, message.Purpose); err == nil {
		t.Fatal("modified ciphertext accepted")
	}
	other, _ := NewDevelopment(d.cache, strings.Repeat("z", 32))
	if _, err := other.open(second, message.Phone, message.Purpose); err == nil {
		t.Fatal("wrong key accepted")
	}
	for _, malformed := range [][]byte{nil, {1}, append([]byte{2}, second[1:]...), make([]byte, 2049)} {
		if _, err := d.open(malformed, message.Phone, message.Purpose); err == nil {
			t.Fatal("malformed encrypted message accepted")
		}
	}
	if d.key(message.Phone, message.Purpose) == d.key(message.Phone, "password_reset") || strings.Contains(d.key(message.Phone, message.Purpose), message.Phone) {
		t.Fatal("Redis key exposes recipient or ignores purpose")
	}
}

func TestDevelopmentUnavailableErrorsDoNotExposeSensitiveData(t *testing.T) {
	cache := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", MaxRetries: -1, DialTimeout: 20 * time.Millisecond})
	t.Cleanup(func() { _ = cache.Close() })
	secret, phone, code := strings.Repeat("private-inbox-secret", 2), "+8613900000001", "123456"
	d, err := NewDevelopment(cache, secret)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	errors := []error{d.SendCode(ctx, phone, code, "register")}
	_, err = d.Read(ctx, phone, "register")
	errors = append(errors, err)
	for _, err := range errors {
		if entity.Code(err) != "SMS_UNAVAILABLE" {
			t.Fatal("unavailable dependency did not fail closed")
		}
		for _, sensitive := range []string{secret, phone, code, "127.0.0.1"} {
			if strings.Contains(err.Error(), sensitive) {
				t.Fatal("dependency error disclosed sensitive request or infrastructure")
			}
		}
	}
	if _, err := d.Read(context.Background(), phone, "unknown"); entity.Code(err) != "INVALID_ARGUMENT" {
		t.Fatal("invalid inbox purpose reached dependency")
	}
}

func TestDevelopmentRejectsInvalidMessageBeforeRedis(t *testing.T) {
	d, err := NewDevelopment(redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"}), strings.Repeat("s", 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = d.cache.Close() })
	for _, tc := range []struct{ phone, code, purpose string }{
		{"13900000001", "123456", "register"},
		{"+8613900000001", "12345", "register"},
		{"+8613900000001", "123abc", "register"},
		{"+8613900000001", "123456", "unknown"},
	} {
		if err := d.SendCode(context.Background(), tc.phone, tc.code, tc.purpose); entity.Code(err) != "INVALID_ARGUMENT" {
			t.Fatal("invalid message not rejected before dependency")
		}
	}
}
