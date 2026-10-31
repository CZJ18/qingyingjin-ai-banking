package modelspend

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// ProjectBudget is a read-only preflight. A missing pool is the unused policy
// allowance; only Reserve may create it and atomically hold funds.
func (s *Service) ProjectBudget(ctx context.Context) (Pool, error) {
	if !s.ready() {
		return Pool{}, ErrUnavailable
	}
	var p Pool
	err := s.db.WithContext(ctx).Where("project_id=?", s.policy.ProjectID).Take(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return Pool{ProjectID: s.policy.ProjectID, Currency: s.policy.Currency, PolicyHash: s.policyHash, LimitUnits: s.limitUnits}, nil
	}
	if err != nil {
		return Pool{}, ledgerError(err)
	}
	if p.ProjectID != s.policy.ProjectID || p.Currency != s.policy.Currency || p.PolicyHash != s.policyHash || p.LimitUnits != s.limitUnits || p.UsedUnits < 0 || p.HeldUnits < 0 {
		return Pool{}, ErrConflict
	}
	return p, nil
}
