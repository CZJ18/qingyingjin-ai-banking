package execution

import (
	"context"
	"github.com/qingyingjin/banking-service/internal/entity"
	"gorm.io/gorm"
)

var requiredPolicyNames = []string{"simulation_only", "bound_authorization", "resource_ownership", "necessary_reserve", "amount_limit", "resource_version"}

func validateRequiredPolicies(rules []entity.RiskRule) error {
	found := map[string]entity.RiskRule{}
	for _, rule := range rules {
		if _, ok := found[rule.Name]; ok {
			return entity.Fail("DEPENDENCY_UNAVAILABLE", "duplicate execution policy")
		}
		found[rule.Name] = rule
	}
	for _, name := range requiredPolicyNames {
		rule, ok := found[name]
		if !ok || !rule.Enabled || rule.Version != 1 {
			return entity.Fail("DEPENDENCY_UNAVAILABLE", "required execution policy missing, disabled or incompatible")
		}
		if name == "amount_limit" && (rule.LimitMinor <= 0 || rule.LimitMinor > entity.MaxBalance) {
			return entity.Fail("DEPENDENCY_UNAVAILABLE", "invalid execution amount policy")
		}
	}
	return nil
}

func checkRequiredPolicies(tx *gorm.DB) error {
	var rules []entity.RiskRule
	if e := tx.Where("name IN ?", requiredPolicyNames).Find(&rules).Error; e != nil {
		return entity.Fail("DEPENDENCY_UNAVAILABLE", "execution policy unavailable")
	}
	return validateRequiredPolicies(rules)
}

// CheckReady only reads policy; it never repairs policy, grants or intents.
func (s *Service) CheckReady(ctx context.Context) error {
	if !s.Configured() {
		return entity.Fail("DEPENDENCY_UNAVAILABLE", "execution policy unavailable")
	}
	return checkRequiredPolicies(s.db.WithContext(ctx))
}
