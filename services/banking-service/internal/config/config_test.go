package config

import (
	"strings"
	"testing"
)

func validEnv() map[string]string {
	return map[string]string{
		"BANKING_ENVIRONMENT":    "test",
		"BANKING_MYSQL_PASSWORD": "test-password", "BANKING_REDIS_PASSWORD": "test-redis",
		"BANKING_USER_JWT_SECRET": strings.Repeat("u", 32), "BANKING_TOOL_TOKEN": strings.Repeat("t", 32),
		"BANKING_TOOL_USER_ID": "42", "BANKING_TOOL_COZE_USER_ID": "7",
	}
}
func TestLoadDefaultsAndValidation(t *testing.T) {
	env := validEnv()
	cfg, err := Load(func(k string) string { return env[k] })
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != "127.0.0.1:8090" || cfg.MySQLDatabase != "qingyingjin" || cfg.RedisDB != 0 {
		t.Fatalf("bad defaults: %#v", cfg)
	}
	for _, key := range []string{"BANKING_MYSQL_PASSWORD", "BANKING_REDIS_PASSWORD", "BANKING_USER_JWT_SECRET", "BANKING_TOOL_TOKEN", "BANKING_TOOL_USER_ID"} {
		t.Run(key, func(t *testing.T) {
			m := validEnv()
			m[key] = ""
			_, err := Load(func(k string) string { return m[k] })
			if err == nil {
				t.Fatal("accepted missing required config")
			}
		})
	}
}
func TestLoadRejectsUnsafeOrMalformedConfig(t *testing.T) {
	for k, v := range map[string]string{
		"BANKING_MYSQL_PORT": "0", "BANKING_REDIS_DB": "-1", "BANKING_HTTP_ADDR": "invalid",
		"BANKING_CHECK_TIMEOUT": "0s", "BANKING_TOOL_USER_ID": "-4", "BANKING_TOOL_COZE_USER_ID": "abc",
		"BANKING_LOG_LEVEL": "wat", "BANKING_COZE_BASE_URL": "https://example.com", "BANKING_MYSQL_DATABASE": "bad;db",
		"BANKING_ORCHESTRATOR_API_BASE_URL": "http://user:secret@example.com", "BANKING_SUPERVISOR_WORKFLOW_ID": "01", "BANKING_SAVINGS_AGENT_WORKFLOW_ID": "123",
	} {
		t.Run(k, func(t *testing.T) {
			m := validEnv()
			m[k] = v
			_, err := Load(func(k string) string { return m[k] })
			if err == nil {
				t.Fatal("accepted malformed config")
			}
		})
	}
}

func TestLoadRejectsTemplateSecrets(t *testing.T) {
	for _, key := range []string{"BANKING_MYSQL_PASSWORD", "BANKING_REDIS_PASSWORD", "BANKING_USER_JWT_SECRET", "BANKING_TOOL_TOKEN"} {
		env := validEnv()
		env[key] = "CHANGE_ME_SECRET_AT_LEAST_32_BYTES"
		if _, err := Load(func(k string) string { return env[k] }); err == nil {
			t.Fatalf("accepted placeholder %s", key)
		}
	}
}

func TestLoadLangGraphServiceToken(t *testing.T) {
	for _, value := range []string{"", strings.Repeat("l", 32)} {
		env := validEnv()
		env["BANKING_LANGGRAPH_SERVICE_TOKEN"] = value
		if _, err := Load(func(k string) string { return env[k] }); err != nil {
			t.Fatalf("optional LangGraph credential rejected: %v", err)
		}
	}
	for _, value := range []string{"short", "CHANGE_ME_SECRET_AT_LEAST_32_BYTES", strings.Repeat("u", 32), strings.Repeat("t", 32)} {
		env := validEnv()
		env["BANKING_LANGGRAPH_SERVICE_TOKEN"] = value
		if _, err := Load(func(k string) string { return env[k] }); err == nil {
			t.Fatal("accepted short, placeholder or reused LangGraph service credential")
		}
	}
}
