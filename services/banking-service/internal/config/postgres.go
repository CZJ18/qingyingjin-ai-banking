package config

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
)

func loadPostgres(c *Config, get func(string) string, value func(string, string) string) error {
	c.PostgresHost = value("BANKING_POSTGRES_HOST", "127.0.0.1")
	if strings.IndexFunc(c.PostgresHost, func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune("/?#@", r)
	}) >= 0 {
		return fmt.Errorf("invalid BANKING_POSTGRES_HOST")
	}
	var err error
	c.PostgresPort, err = strconv.Atoi(value("BANKING_POSTGRES_PORT", "5432"))
	if err != nil || c.PostgresPort < 1 || c.PostgresPort > 65535 {
		return fmt.Errorf("invalid BANKING_POSTGRES_PORT")
	}
	c.PostgresUser = value("BANKING_POSTGRES_USER", "banking_finance_app")
	c.PostgresDatabase = value("BANKING_POSTGRES_DATABASE", "qingrongjin_finance")
	for key, name := range map[string]string{"BANKING_POSTGRES_USER": c.PostgresUser, "BANKING_POSTGRES_DATABASE": c.PostgresDatabase} {
		if len(name) == 0 || len(name) > 63 {
			return fmt.Errorf("invalid %s", key)
		}
		for _, r := range name {
			if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_') {
				return fmt.Errorf("invalid %s", key)
			}
		}
	}
	c.PostgresPassword = get("BANKING_POSTGRES_PASSWORD")
	if strings.TrimSpace(c.PostgresPassword) == "" || strings.Contains(c.PostgresPassword, "CHANGE_ME") {
		return fmt.Errorf("BANKING_POSTGRES_PASSWORD is required and cannot be a template")
	}
	c.PostgresSSLMode = value("BANKING_POSTGRES_SSLMODE", "verify-full")
	switch c.PostgresSSLMode {
	case "disable", "require", "verify-ca", "verify-full":
	default:
		return fmt.Errorf("invalid BANKING_POSTGRES_SSLMODE")
	}
	return nil
}
