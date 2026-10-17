package config

import "testing"

func TestModelBudgetDefaultsAndBoundedOverrides(t *testing.T) {
	env := validEnv()
	c, e := Load(func(k string) string { return env[k] })
	if e != nil || c.ModelDailyCalls != 20 || c.ModelDailyTokens != 327680 || c.ModelReservationTokens != 16384 {
		t.Fatal("missing bounded model defaults")
	}
	for _, patch := range []map[string]string{{"BANKING_MODEL_DAILY_CALLS": "0"}, {"BANKING_MODEL_DAILY_CALLS": "1001"}, {"BANKING_MODEL_DAILY_TOKENS": "1"}, {"BANKING_MODEL_RESERVATION_TOKENS": "-1"}, {"BANKING_MODEL_DAILY_TOKENS": "1e9"}, {"BANKING_MODEL_DAILY_TOKENS": "9223372036854775808"}} {
		env := validEnv()
		for k, v := range patch {
			env[k] = v
		}
		if _, e := Load(func(k string) string { return env[k] }); e == nil {
			t.Fatal("unsafe model budget config", patch)
		}
	}
}
