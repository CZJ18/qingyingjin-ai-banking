package config

import "testing"

func TestAuthCookieIsSecureUnlessExplicitLocalOverride(t *testing.T) {
	env := validEnv()
	c, err := Load(func(k string) string { return env[k] })
	if err != nil || !c.AuthCookieSecure {
		t.Fatalf("secure cookie default missing: %v", err)
	}
	env["BANKING_AUTH_COOKIE_SECURE"] = "false"
	c, err = Load(func(k string) string { return env[k] })
	if err != nil || c.AuthCookieSecure {
		t.Fatalf("local explicit override failed: %v", err)
	}
	env["BANKING_AUTH_COOKIE_SECURE"] = "off"
	if _, err = Load(func(k string) string { return env[k] }); err == nil {
		t.Fatal("ambiguous cookie security setting accepted")
	}
}
