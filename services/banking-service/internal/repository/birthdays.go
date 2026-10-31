package repository

import "github.com/qingyingjin/banking-service/internal/birthdays"

func (r *Core) Birthdays() *birthdays.Service { return birthdays.New(r.db) }
