package userauth

import (
	"context"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/redis/go-redis/v9"
)

var recipientLimiter = redis.NewScript(`local u=tonumber(redis.call('GET',KEYS[1]) or '0');local ip=tonumber(redis.call('GET',KEYS[2]) or '0');if u>=20 or ip>=60 then return 0 end;if redis.call('INCR',KEYS[1])==1 then redis.call('EXPIRE',KEYS[1],60) end;if redis.call('INCR',KEYS[2])==1 then redis.call('EXPIRE',KEYS[2],60) end;return 1`)

func (s *Service) AllowRecipientLookup(ctx context.Context, user, ip string) error {
	if s == nil || s.redis == nil {
		return entity.Fail("DEPENDENCY_UNAVAILABLE", "recipient lookup limiter unavailable")
	}
	allowed, e := recipientLimiter.Run(ctx, s.redis, []string{limiterKey("recipient-user", user), limiterKey("recipient-ip", ip)}).Int()
	if e != nil {
		return entity.Fail("DEPENDENCY_UNAVAILABLE", "recipient lookup limiter unavailable")
	}
	if allowed != 1 {
		return entity.Fail("RECIPIENT_RATE_LIMITED", "too many recipient searches; try again in a minute")
	}
	return nil
}
