package service

import (
	"context"
	"github.com/qingyingjin/banking-service/internal/cards"
	"github.com/qingyingjin/banking-service/internal/entity"
)

func (s *Core) Cards(ctx context.Context, user int64) ([]cards.Card, error) {
	return s.repo.Cards(ctx, user)
}
func (s *Core) Card(ctx context.Context, user int64, id string) (cards.Card, error) {
	all, e := s.Cards(ctx, user)
	if e != nil {
		return cards.Card{}, e
	}
	for _, c := range all {
		if c.ID == id {
			return c, nil
		}
	}
	return cards.Card{}, entity.Fail("NOT_FOUND", "card not found")
}
