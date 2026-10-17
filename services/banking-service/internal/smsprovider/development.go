// Package smsprovider contains explicit delivery adapters. The development
// adapter writes an encrypted, expiring local Redis inbox and never sends SMS.
package smsprovider

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/redis/go-redis/v9"
)

const inboxTTL = 5 * time.Minute

var mainlandPhone = regexp.MustCompile(`^\+861[3-9][0-9]{9}$`)
var numericCode = regexp.MustCompile(`^[0-9]{6}$`)

// InboxMessage is returned only to an explicitly invoked local developer CLI.
// It must never be included in a public HTTP response or application log.
type InboxMessage struct {
	Code      string    `json:"code"`
	Phone     string    `json:"phone"`
	Purpose   string    `json:"purpose"`
	ExpiresAt time.Time `json:"expires_at"`
}

type Development struct {
	cache  *redis.Client
	aead   cipher.AEAD
	keyMAC []byte
}

func NewDevelopment(cache *redis.Client, secret string) (*Development, error) {
	if cache == nil || len(secret) < 32 || len(secret) > 4096 || strings.TrimSpace(secret) == "" || strings.Contains(strings.ToUpper(secret), "CHANGE_ME") {
		return nil, fmt.Errorf("development SMS requires Redis and an independent non-template secret of at least 32 bytes")
	}
	derive := func(label string) []byte {
		mac := hmac.New(sha256.New, []byte(secret))
		_, _ = mac.Write([]byte(label))
		return mac.Sum(nil)
	}
	block, err := aes.NewCipher(derive("qingrongjin:sms-inbox:aes-gcm:v1"))
	if err != nil {
		return nil, fmt.Errorf("development SMS encryption initialization failed")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("development SMS encryption initialization failed")
	}
	return &Development{cache: cache, aead: aead, keyMAC: derive("qingrongjin:sms-inbox:redis-key:v1")}, nil
}

func (*Development) Mode() string { return "development" }

func validPurpose(purpose string) bool {
	return purpose == "register" || purpose == "verify_phone" || purpose == "password_reset"
}

func (d *Development) key(phone, purpose string) string {
	mac := hmac.New(sha256.New, d.keyMAC)
	_, _ = mac.Write([]byte(phone + "\x00" + purpose))
	return "qingrongjin:sms-inbox:v1:" + hex.EncodeToString(mac.Sum(nil))
}

func messageAAD(phone, purpose string) []byte {
	return []byte("qingrongjin:sms-inbox:v1\x00" + phone + "\x00" + purpose)
}

func (d *Development) seal(message InboxMessage) ([]byte, error) {
	plain, err := json.Marshal(message)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, d.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	payload := append([]byte{1}, nonce...)
	return d.aead.Seal(payload, nonce, plain, messageAAD(message.Phone, message.Purpose)), nil
}

func (d *Development) open(payload []byte, phone, purpose string) (InboxMessage, error) {
	var message InboxMessage
	if len(payload) < 1+d.aead.NonceSize()+d.aead.Overhead() || len(payload) > 2048 || payload[0] != 1 {
		return message, fmt.Errorf("invalid inbox ciphertext")
	}
	nonce := payload[1 : 1+d.aead.NonceSize()]
	plain, err := d.aead.Open(nil, nonce, payload[1+d.aead.NonceSize():], messageAAD(phone, purpose))
	if err != nil {
		return message, fmt.Errorf("invalid inbox ciphertext")
	}
	if err := json.Unmarshal(plain, &message); err != nil || message.Phone != phone || message.Purpose != purpose || !numericCode.MatchString(message.Code) || message.ExpiresAt.IsZero() {
		return InboxMessage{}, fmt.Errorf("invalid inbox message")
	}
	return message, nil
}

func (d *Development) SendCode(ctx context.Context, phone, code, purpose string) error {
	if !mainlandPhone.MatchString(phone) || !numericCode.MatchString(code) || !validPurpose(purpose) {
		return entity.Fail("INVALID_ARGUMENT", "invalid verification message")
	}
	if d == nil || d.cache == nil || d.aead == nil {
		return unavailable()
	}
	message := InboxMessage{Code: code, Phone: phone, Purpose: purpose, ExpiresAt: time.Now().UTC().Add(inboxTTL)}
	payload, err := d.seal(message)
	if err != nil {
		return unavailable()
	}
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := d.cache.Set(bounded, d.key(phone, purpose), payload, inboxTTL).Err(); err != nil {
		return unavailable()
	}
	return nil
}

func (d *Development) Read(ctx context.Context, phone, purpose string) (InboxMessage, error) {
	if !mainlandPhone.MatchString(phone) || !validPurpose(purpose) {
		return InboxMessage{}, entity.Fail("INVALID_ARGUMENT", "invalid verification inbox lookup")
	}
	if d == nil || d.cache == nil || d.aead == nil {
		return InboxMessage{}, unavailable()
	}
	bounded, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	payload, err := d.cache.Get(bounded, d.key(phone, purpose)).Bytes()
	if err == redis.Nil {
		return InboxMessage{}, notFound()
	}
	if err != nil {
		return InboxMessage{}, unavailable()
	}
	message, err := d.open(payload, phone, purpose)
	if err != nil {
		return InboxMessage{}, unavailable()
	}
	if !time.Now().Before(message.ExpiresAt) {
		return InboxMessage{}, notFound()
	}
	return message, nil
}

func unavailable() error {
	return entity.Fail("SMS_UNAVAILABLE", "verification inbox is temporarily unavailable")
}
func notFound() error {
	return entity.Fail("SMS_MESSAGE_NOT_FOUND", "verification message was not found or has expired")
}
