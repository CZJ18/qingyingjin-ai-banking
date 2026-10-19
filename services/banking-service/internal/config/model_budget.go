package config

import (
	"fmt"
	"github.com/qingyingjin/banking-service/internal/coze"
	"strconv"
)

func loadModelBudget(c *Config, get func(string) string) error {
	c.ModelCostCurrency = get("BANKING_MODEL_REPORTED_COST_CURRENCY")
	c.ModelCostUnit = get("BANKING_MODEL_REPORTED_COST_UNIT")
	if !coze.ValidCostMetadata(c.ModelCostCurrency, c.ModelCostUnit) {
		return fmt.Errorf("invalid model reported cost metadata")
	}
	for _, item := range []struct {
		key, def string
		target   *int64
	}{{"BANKING_MODEL_DAILY_CALLS", "20", &c.ModelDailyCalls}, {"BANKING_MODEL_DAILY_TOKENS", "327680", &c.ModelDailyTokens}, {"BANKING_MODEL_RESERVATION_TOKENS", "16384", &c.ModelReservationTokens}} {
		v := get(item.key)
		if v == "" {
			v = item.def
		}
		n, e := strconv.ParseInt(v, 10, 64)
		if e != nil || n <= 0 {
			return fmt.Errorf("invalid %s", item.key)
		}
		*item.target = n
	}
	if c.ModelDailyCalls > 1000 || c.ModelDailyTokens > 1_000_000_000_000 || c.ModelReservationTokens > 1_000_000_000 || c.ModelDailyTokens < c.ModelReservationTokens {
		return fmt.Errorf("invalid bounded model budget")
	}
	return nil
}
