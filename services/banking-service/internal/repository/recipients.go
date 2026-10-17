package repository

import "github.com/qingyingjin/banking-service/internal/recipients"

func (r *Core) Recipients() *recipients.Service { return recipients.New(r.db) }
