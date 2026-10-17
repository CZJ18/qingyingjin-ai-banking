package service

import "github.com/qingyingjin/banking-service/internal/recipients"

func (s *Core) Recipients() *recipients.Service { return s.repo.Recipients() }
