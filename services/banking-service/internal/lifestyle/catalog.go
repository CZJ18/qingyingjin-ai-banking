package lifestyle

import (
	"context"
	"github.com/qingyingjin/banking-service/internal/entity"
	"gorm.io/gorm"
)

// Goals returns only this owner's goals and each current persisted version.
func (s *Service) Goals(ctx context.Context, user int64, limit, offset int) ([]GoalView, error) {
	out := []GoalView{}
	if _, _, e := page([]int{limit, offset}); e != nil {
		return out, e
	}
	if user <= 0 {
		return out, entity.Fail("INVALID_ARGUMENT", "positive user required")
	}
	if !s.Configured() {
		return out, entity.Fail("DEPENDENCY_UNAVAILABLE", "lifestyle repository unavailable")
	}
	e := s.repo.transaction(ctx, user, func(tx *gorm.DB) error {
		goals := []Goal{}
		if e := tx.Where("user_id=?", user).Order("updated_at DESC,id DESC").Limit(limit).Offset(offset).Find(&goals).Error; e != nil {
			return e
		}
		for _, g := range goals {
			v, e := goalView(tx, g, 1, 0)
			if e != nil {
				return e
			}
			out = append(out, v)
		}
		return nil
	})
	return out, e
}
