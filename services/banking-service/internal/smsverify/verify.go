// Package smsverify owns short-lived, purpose-bound, single-use challenges.
// The sender delivers codes; normal HTTP responses never contain their values.
package smsverify

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/redis/go-redis/v9"
)

type Purpose string

const (
	RegisterPhone Purpose = "register"
	VerifyPhone   Purpose = "verify_phone"
	ResetPassword Purpose = "password_reset"
)
const codeLifetime = 300 * time.Second

type Binding struct {
	UserID      string
	AuthVersion int64
}
type Challenge struct {
	ChallengeID string `json:"challenge_id"`
	ExpiresIn   int    `json:"expires_in"`
	RetryAfter  int    `json:"retry_after"`
}
type Sender interface {
	Mode() string
	SendCode(context.Context, string, string, string) error
}
type Verifier struct {
	cache  *redis.Client
	sender Sender
	secret []byte
}

func New(cache *redis.Client, sender Sender, secret string) *Verifier {
	return &Verifier{cache: cache, sender: sender, secret: []byte(secret)}
}
func (v *Verifier) Enabled() bool {
	return v != nil && v.cache != nil && v.sender != nil && len(v.secret) >= 32 && (v.sender.Mode() == "development" || v.sender.Mode() == "tencent")
}
func (v *Verifier) Mode() string {
	if !v.Enabled() {
		return "disabled"
	}
	return v.sender.Mode()
}
func unavailable() error {
	return entity.Fail("SMS_UNAVAILABLE", "verification delivery is unavailable")
}
func dependency() error {
	return entity.Fail("DEPENDENCY_UNAVAILABLE", "verification state is unavailable")
}
func invalidCode() error {
	return entity.Fail("AUTH_CODE_INVALID", "verification code is invalid, expired or already used")
}
func limited() error { return entity.Fail("AUTH_RATE_LIMITED", "verification attempt limit exceeded") }

var phonePattern = regexp.MustCompile(`^\+861[3-9][0-9]{9}$`)
var idPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43}$`)

func validPurpose(p Purpose) bool {
	return p == RegisterPhone || p == VerifyPhone || p == ResetPassword
}
func generateCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", unavailable()
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}
func randomID() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", unavailable()
	}
	return base64.RawURLEncoding.EncodeToString(b[:]), nil
}
func (v *Verifier) mac(label string, values ...string) string {
	h := hmac.New(sha256.New, v.secret)
	h.Write([]byte(label))
	for _, s := range values {
		h.Write([]byte{0})
		h.Write([]byte(s))
	}
	return hex.EncodeToString(h.Sum(nil))
}
func (v *Verifier) key(label, value string) string {
	return "banking:sms:v1:" + label + ":" + v.mac("key:"+label, value)
}
func (v *Verifier) digest(phone string, p Purpose, id, code string) string {
	return v.mac("otp", phone, string(p), id, code)
}
func normalizedIP(ip string) string {
	ip = strings.TrimSpace(ip)
	if ip == "" || len(ip) > 128 {
		return "unknown"
	}
	return ip
}

// Reserve limits before delivery and creates an uncheckable pending challenge.
// Global/day limits are per installation secret, including rejected deliveries.
var reserve = redis.NewScript(`
if redis.call('EXISTS',KEYS[1])==1 then return 0 end
local limits={5,10,20,100,1000}
local windows={3600,86400,3600,86400,86400}
for i=2,6 do if tonumber(redis.call('GET',KEYS[i]) or '0')>=limits[i-1] then return 0 end end
for i=2,6 do if redis.call('INCR',KEYS[i])==1 then redis.call('EXPIRE',KEYS[i],windows[i-1]) end end
redis.call('SET',KEYS[1],'1','EX',60)
redis.call('HSET',KEYS[7],'digest',ARGV[1],'phone',ARGV[2],'purpose',ARGV[3],'owner',ARGV[4],'version',ARGV[5],'state','pending','attempts','0')
redis.call('EXPIRE',KEYS[7],300)
redis.call('SET',KEYS[8],ARGV[6],'EX',300)
return 1
`)
var activate = redis.NewScript(`
if redis.call('GET',KEYS[2])~=ARGV[1] or redis.call('HGET',KEYS[1],'state')~='pending' then return 0 end
redis.call('HSET',KEYS[1],'state','active');return 1
`)
var discard = redis.NewScript(`
redis.call('DEL',KEYS[1]);if redis.call('GET',KEYS[2])==ARGV[1] then redis.call('DEL',KEYS[2]) end;return 1
`)

func (v *Verifier) Issue(ctx context.Context, phone string, p Purpose, ip string, b Binding) (Challenge, error) {
	var out Challenge
	if !v.Enabled() {
		return out, unavailable()
	}
	if !phonePattern.MatchString(phone) || !validPurpose(p) || b.AuthVersion < 0 {
		return out, entity.Fail("INVALID_ARGUMENT", "invalid verification request")
	}
	id, err := randomID()
	if err != nil {
		return out, err
	}
	code, err := generateCode()
	if err != nil {
		return out, err
	}
	activeKey := v.key("active", phone+"|"+string(p))
	challengeKey := v.key("challenge", id)
	ip = normalizedIP(ip)
	keys := []string{v.key("cooldown", phone), v.key("phone-hour", phone), v.key("phone-day", phone), v.key("ip-hour", ip), v.key("ip-day", ip), v.key("global-day", "all"), challengeKey, activeKey}
	ok, err := reserve.Run(ctx, v.cache, keys, v.digest(phone, p, id, code), v.mac("phone", phone), string(p), b.UserID, strconv.FormatInt(b.AuthVersion, 10), id).Int()
	if err != nil {
		return out, dependency()
	}
	if ok != 1 {
		return out, limited()
	}
	deliveryCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	err = v.sender.SendCode(deliveryCtx, phone, code, string(p))
	cancel()
	if err != nil {
		cleanupCtx, done := context.WithTimeout(context.Background(), 2*time.Second)
		defer done()
		_ = discard.Run(cleanupCtx, v.cache, []string{challengeKey, activeKey}, id).Err()
		return out, unavailable()
	}
	ok, err = activate.Run(ctx, v.cache, []string{challengeKey, activeKey}, id).Int()
	if err != nil {
		return out, dependency()
	}
	if ok != 1 {
		return out, unavailable()
	}
	return Challenge{ChallengeID: id, ExpiresIn: int(codeLifetime / time.Second), RetryAfter: 60}, nil
}

var consume = redis.NewScript(`
local limits={20,40}
for i=3,4 do if tonumber(redis.call('GET',KEYS[i]) or '0')>=limits[i-2] then return {'limited'} end end
for i=3,4 do if redis.call('INCR',KEYS[i])==1 then redis.call('EXPIRE',KEYS[i],900) end end
if redis.call('GET',KEYS[2])~=ARGV[4] or redis.call('HGET',KEYS[1],'state')~='active' then return {'invalid'} end
if redis.call('HGET',KEYS[1],'phone')~=ARGV[2] or redis.call('HGET',KEYS[1],'purpose')~=ARGV[3] then return {'invalid'} end
local attempts=redis.call('HINCRBY',KEYS[1],'attempts',1)
if attempts>5 then redis.call('DEL',KEYS[1]);return {'invalid'} end
if redis.call('HGET',KEYS[1],'digest')~=ARGV[1] then
 if attempts==5 then redis.call('DEL',KEYS[1]) end
 return {'invalid'}
end
local owner=redis.call('HGET',KEYS[1],'owner') or ''
local version=redis.call('HGET',KEYS[1],'version') or '0'
redis.call('DEL',KEYS[1]);redis.call('DEL',KEYS[2]);return {'ok',owner,version}
`)

func (v *Verifier) Consume(ctx context.Context, phone string, p Purpose, id, code, ip string) (Binding, error) {
	var out Binding
	if !v.Enabled() {
		return out, unavailable()
	}
	if !phonePattern.MatchString(phone) || !validPurpose(p) || !idPattern.MatchString(id) || len(code) != 6 || strings.Trim(code, "0123456789") != "" {
		return out, invalidCode()
	}
	keys := []string{v.key("challenge", id), v.key("active", phone+"|"+string(p)), v.key("verify-phone", phone), v.key("verify-ip", normalizedIP(ip))}
	values, err := consume.Run(ctx, v.cache, keys, v.digest(phone, p, id, code), v.mac("phone", phone), string(p), id).StringSlice()
	if err != nil {
		return out, dependency()
	}
	if len(values) == 0 {
		return out, dependency()
	}
	if values[0] == "limited" {
		return out, limited()
	}
	if values[0] != "ok" || len(values) != 3 {
		return out, invalidCode()
	}
	version, err := strconv.ParseInt(values[2], 10, 64)
	if err != nil || version < 0 {
		return out, invalidCode()
	}
	return Binding{UserID: values[1], AuthVersion: version}, nil
}
