package repository

import (
	"github.com/coze-dev/coze-studio/backend/domain/banking/internal/dal"
	"github.com/coze-dev/coze-studio/backend/infra/idgen"
	"gorm.io/gorm"
)

func NewBankingRepository(db *gorm.DB, generator idgen.IDGenerator) Repository {
	return dal.NewBankingDAO(db, generator)
}

var _ Repository = (*dal.BankingDAO)(nil)
