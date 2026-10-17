package service

import "github.com/qingyingjin/banking-service/internal/aa"

func (s *Core) AA() *aa.Service { return s.repo.AA() }
