package repository

import "github.com/qingyingjin/banking-service/internal/reminders"

func (s *Core) Renewals() *reminders.Service { return reminders.New(s.db) }
