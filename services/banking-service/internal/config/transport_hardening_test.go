package config

import "testing"

func TestTransportEnvironmentRejectsUnsafeRemoteHTTP(t *testing.T) {
	for _, tc := range []struct {
		env, target string
		allowed     bool
	}{
		{"production", "http://coze.example", false}, {"production", "https://coze.example", true},
		{"development", "http://127.0.0.1:8888", true}, {"test", "http://127.0.0.1:8888", true},
		{"development", "http://coze-internal:8888", false}, {"unknown", "https://coze.example", false},
	} {
		t.Run(tc.env+tc.target, func(t *testing.T) {
			m := validEnv()
			m["BANKING_ENVIRONMENT"] = tc.env
			m["BANKING_COZE_BASE_URL"] = tc.target
			m["BANKING_COZE_PAT"] = "synthetic-pat"
			m["BANKING_ORCHESTRATOR_API_BASE_URL"] = "https://bank.example"
			_, e := Load(func(k string) string { return m[k] })
			if (e == nil) != tc.allowed {
				t.Fatalf("allowed=%v err=%v", tc.allowed, e)
			}
		})
	}
}
