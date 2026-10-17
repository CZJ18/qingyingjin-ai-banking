package service

import (
	"context"
	"github.com/qingyingjin/banking-service/internal/reminders"
)

func (s *Core) Renewals(ctx context.Context, user int64) (reminders.View, error) {
	return s.repo.Renewals().Read(ctx, user)
}
func (s *Core) RenewalSettings(ctx context.Context, user int64, enabled bool, days int, expected int64) (reminders.View, error) {
	return s.repo.Renewals().Configure(ctx, user, enabled, days, expected)
}
func (s *Core) ReadRenewal(ctx context.Context, user int64, id string, expected int64) (reminders.View, error) {
	return s.repo.Renewals().ReadNotice(ctx, user, id, expected)
}
