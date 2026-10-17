package userauth

import (
	"context"
	"github.com/qingyingjin/banking-service/internal/entity"
	"testing"
)

func TestRecipientLimiterFailsClosedWithoutRedis(t *testing.T) {
	for _, s := range []*Service{nil, New(nil, nil, nil)} {
		if e := s.AllowRecipientLookup(context.Background(), "42", "127.0.0.1"); entity.Code(e) != "DEPENDENCY_UNAVAILABLE" {
			t.Fatal(e)
		}
	}
}
