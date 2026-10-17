package userauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"

	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/redis/go-redis/v9"
)

// Both windows are checked and incremented atomically before password hashing.
// A successful login does not reset counters; this is an attempt limit.
var authLimiter = redis.NewScript(`
local phone=tonumber(redis.call('GET',KEYS[1]) or '0')
local ip=tonumber(redis.call('GET',KEYS[2]) or '0')
if phone>=5 or ip>=100 then return 0 end
if redis.call('INCR',KEYS[1])==1 then redis.call('EXPIRE',KEYS[1],900) end
if redis.call('INCR',KEYS[2])==1 then redis.call('EXPIRE',KEYS[2],60) end
return 1
`)

func limiterKey(kind, value string) string {
	sum := sha256.Sum256([]byte(value))
	return "banking:auth:v1:" + kind + ":" + hex.EncodeToString(sum[:])
}
func (s *Service) rateLimit(ctx context.Context, phone, ip string) error {
	if s == nil || s.redis == nil {
		return unavailable()
	}
	ip = strings.TrimSpace(ip)
	if ip == "" || len(ip) > 128 {
		ip = "unknown"
	}
	allowed, err := authLimiter.Run(ctx, s.redis, []string{limiterKey("phone", phone), limiterKey("ip", ip)}).Int()
	if err != nil {
		return unavailable()
	}
	if allowed != 1 {
		return entity.Fail("AUTH_RATE_LIMITED", "too many authentication attempts; try again later")
	}
	return nil
}
