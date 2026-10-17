package config

import (
	"fmt"
	"strings"
)

// SMSConfig configures the local verification flow. No cloud provider is
// configured or claimed by the development adapter.
type SMSConfig struct {
	Provider  string
	OTPSecret string
}

func loadSMS(c *Config, get func(string) string) error {
	c.SMS = SMSConfig{Provider: get("BANKING_SMS_PROVIDER")}
	if c.SMS.Provider == "" {
		c.SMS.Provider = "disabled"
	}
	if c.SMS.Provider == "disabled" {
		return nil
	}
	if c.SMS.Provider != "development" {
		return fmt.Errorf("BANKING_SMS_PROVIDER must be disabled or development")
	}
	c.SMS.OTPSecret = get("BANKING_SMS_OTP_SECRET")
	if len(c.SMS.OTPSecret) < 32 || len(c.SMS.OTPSecret) > 4096 || strings.TrimSpace(c.SMS.OTPSecret) == "" || strings.Contains(strings.ToUpper(c.SMS.OTPSecret), "CHANGE_ME") {
		return fmt.Errorf("BANKING_SMS_OTP_SECRET must be an independent non-template secret of at least 32 bytes")
	}
	for _, secret := range []string{c.JWTSecret, c.ToolToken, c.LangGraphServiceToken} {
		if secret != "" && c.SMS.OTPSecret == secret {
			return fmt.Errorf("BANKING_SMS_OTP_SECRET must not reuse an authentication or tool credential")
		}
	}
	return nil
}
