package repository

import (
	"context"
	"github.com/qingyingjin/banking-service/internal/cards"
)

func (s *Core) Cards(ctx context.Context, user int64) ([]cards.Card, error) {
	return cards.Read(s.db.WithContext(ctx), user)
}
