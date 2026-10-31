package planning

import (
	"context"
	"github.com/qingyingjin/banking-service/internal/entity"
	"gorm.io/gorm"
)

// Plans exposes persisted owned summaries, never a model-supplied owner filter.
func (s *Service) Plans(ctx context.Context, user int64, limit, offset int) ([]FinancialPlan, error) {
	out := []FinancialPlan{}
	if user <= 0 || limit < 1 || limit > 100 || offset < 0 || offset > 1000000 {
		return out, entity.Fail("INVALID_ARGUMENT", "invalid user or pagination")
	}
	if !s.Configured() {
		return out, entity.Fail("DEPENDENCY_UNAVAILABLE", "planning repository unavailable")
	}
	e := s.repo.transaction(ctx, user, func(tx *gorm.DB) error {
		return tx.Where("user_id=?", user).Order("updated_at DESC,id DESC").Limit(limit).Offset(offset).Find(&out).Error
	})
	return out, e
}
