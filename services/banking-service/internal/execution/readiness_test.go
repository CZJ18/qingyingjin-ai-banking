package execution

import (
	"github.com/qingyingjin/banking-service/internal/entity"
	"testing"
)

func TestRequiredPoliciesFailClosed(t *testing.T) {
	rules := []entity.RiskRule{}
	for _, name := range []string{"simulation_only", "bound_authorization", "resource_ownership", "necessary_reserve", "amount_limit", "resource_version"} {
		rules = append(rules, entity.RiskRule{Name: name, Version: 1, Enabled: true, LimitMinor: entity.MaxBalance})
	}
	if e := validateRequiredPolicies(rules); e != nil {
		t.Fatal(e)
	}
	for _, test := range []struct {
		name   string
		change func([]entity.RiskRule) []entity.RiskRule
	}{
		{"missing", func(r []entity.RiskRule) []entity.RiskRule { return r[:5] }},
		{"disabled", func(r []entity.RiskRule) []entity.RiskRule { r[0].Enabled = false; return r }},
		{"version", func(r []entity.RiskRule) []entity.RiskRule { r[2].Version = 2; return r }},
		{"zero_limit", func(r []entity.RiskRule) []entity.RiskRule { r[4].LimitMinor = 0; return r }},
		{"oversize_limit", func(r []entity.RiskRule) []entity.RiskRule { r[4].LimitMinor = entity.MaxBalance + 1; return r }},
		{"duplicate", func(r []entity.RiskRule) []entity.RiskRule { return append(r, r[0]) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			if validateRequiredPolicies(test.change(append([]entity.RiskRule{}, rules...))) == nil {
				t.Fatal("invalid policy admitted")
			}
		})
	}
	if validateRequiredPolicies(nil) == nil {
		t.Fatal("empty policy admitted")
	}
}
