package config

import (
	"strings"
	"testing"
)

func TestLoadLangGraphServiceTokenBoundaryAndPreservation(t *testing.T) {
	for _, tc := range []struct {
		name, key string
		invalid   bool
	}{
		{"missing keeps bridge disabled", "", false},
		{"31 bytes rejected", strings.Repeat("l", 31), true},
		{"32 bytes accepted", strings.Repeat("l", 32), false},
		{"longer independent key accepted", strings.Repeat("l", 64), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env := validEnv()
			env["BANKING_LANGGRAPH_SERVICE_TOKEN"] = tc.key
			cfg, err := Load(func(name string) string { return env[name] })
			if tc.invalid {
				if err == nil || !strings.Contains(err.Error(), "BANKING_LANGGRAPH_SERVICE_TOKEN") {
					t.Fatal("invalid LangGraph key was not identified by config validation")
				}
				if strings.Contains(err.Error(), tc.key) {
					t.Fatal("config validation disclosed the rejected credential")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if cfg.LangGraphServiceToken != tc.key || cfg.JWTSecret != env["BANKING_USER_JWT_SECRET"] || cfg.ToolToken != env["BANKING_TOOL_TOKEN"] {
				t.Fatal("LangGraph config altered or reused an existing credential")
			}
		})
	}
}
