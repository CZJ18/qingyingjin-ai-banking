package config

import (
	"strings"
	"testing"
)

func TestSMSDefaultsDisabledAndDoesNotRequireCloudCredentials(t *testing.T) {
	var c Config
	if err := loadSMS(&c, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	if c.SMS.Provider != "disabled" || c.SMS.OTPSecret != "" {
		t.Fatal("SMS should be disabled by default")
	}
}

func TestSMSDisabledClearsPreviouslyLoadedSecret(t *testing.T) {
	c := Config{SMS: SMSConfig{Provider: "development", OTPSecret: strings.Repeat("d", 32)}}
	if err := loadSMS(&c, func(string) string { return "" }); err != nil {
		t.Fatal(err)
	}
	if c.SMS.OTPSecret != "" {
		t.Fatal("disabled SMS retained a previously loaded secret")
	}
}

func TestLoadSMSDevelopmentRequiresExplicitLocalCookieConfiguration(t *testing.T) {
	for _, secure := range []string{"", "true", "false"} {
		env := validEnv()
		env["BANKING_SMS_PROVIDER"] = "development"
		env["BANKING_SMS_OTP_SECRET"] = strings.Repeat("d", 32)
		env["BANKING_AUTH_COOKIE_SECURE"] = secure
		c, err := Load(func(k string) string { return env[k] })
		if secure != "false" {
			if err == nil {
				t.Fatal("development inbox enabled without an explicit local HTTP override")
			}
		} else if err != nil || c.SMS.Provider != "development" {
			t.Fatal("local development SMS configuration was rejected")
		}
	}
}

func TestSMSDevelopmentRequiresIndependentSecret(t *testing.T) {
	for _, tc := range []struct {
		name, provider, secret string
		valid                  bool
	}{
		{"development", "development", strings.Repeat("d", 32), true},
		{"short", "development", "short", false},
		{"empty", "development", "", false},
		{"placeholder", "development", "CHANGE_ME_SECRET_AT_LEAST_32_BYTES", false},
		{"jwt-reuse", "development", strings.Repeat("u", 32), false},
		{"tool-reuse", "development", strings.Repeat("t", 32), false},
		{"langgraph-reuse", "development", strings.Repeat("l", 32), false},
		{"unimplemented-cloud", "tencent", strings.Repeat("d", 32), false},
		{"unknown", "bogus", strings.Repeat("d", 32), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := Config{JWTSecret: strings.Repeat("u", 32), ToolToken: strings.Repeat("t", 32), LangGraphServiceToken: strings.Repeat("l", 32)}
			env := map[string]string{"BANKING_SMS_PROVIDER": tc.provider, "BANKING_SMS_OTP_SECRET": tc.secret}
			err := loadSMS(&c, func(k string) string { return env[k] })
			if (err == nil) != tc.valid {
				t.Fatalf("config accepted=%v, wanted=%v", err == nil, tc.valid)
			}
			if err != nil && strings.Contains(err.Error(), tc.secret) && tc.secret != "" {
				t.Fatal("configuration error leaks secret")
			}
		})
	}
}
