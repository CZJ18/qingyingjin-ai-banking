package config

import (
	"testing"
	"time"
)

func TestCozeDeadlineIndependentOfHealthCheck(t *testing.T) {
	for _, value := range []string{"60s", "150s", "180s"} {
		env := validEnv()
		env["BANKING_COZE_TIMEOUT"] = value
		cfg, err := Load(func(key string) string { return env[key] })
		if err != nil {
			t.Fatalf("valid model deadline %s rejected: %v", value, err)
		}
		expected, _ := time.ParseDuration(value)
		if cfg.CozeTimeout != expected {
			t.Fatal("model deadline changed")
		}
	}
	for _, item := range []struct{ key, value string }{{"BANKING_COZE_TIMEOUT", "181s"}, {"BANKING_COZE_TIMEOUT", "0s"}, {"BANKING_CHECK_TIMEOUT", "61s"}} {
		env := validEnv()
		env[item.key] = item.value
		if _, err := Load(func(key string) string { return env[key] }); err == nil {
			t.Fatalf("accepted %s=%s", item.key, item.value)
		}
	}
}
