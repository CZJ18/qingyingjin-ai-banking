package service

import (
	"context"
	"github.com/qingyingjin/banking-service/internal/birthdays"
)

func (s *Core) Birthdays(ctx context.Context, user int64) (birthdays.View, error) {
	return s.repo.Birthdays().Read(ctx, user)
}
func (s *Core) BirthdaySettings(ctx context.Context, user int64, in birthdays.Input) (birthdays.View, error) {
	return s.repo.Birthdays().Configure(ctx, user, in)
}
