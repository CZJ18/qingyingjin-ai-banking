package config

import "testing"

func TestPostgresDoesNotRequireMySQLSecret(t *testing.T) {
	env := validEnv()
	delete(env, "BANKING_MYSQL_PASSWORD")
	env["BANKING_DATABASE_DRIVER"] = "postgres"
	env["BANKING_POSTGRES_PASSWORD"] = "isolated-test-password"
	if _, err := Load(func(k string) string { return env[k] }); err != nil {
		t.Fatalf("valid independent PostgreSQL rejected: %v", err)
	}
}

func TestPostgresConfigurationFailsClosed(t *testing.T) {
	for key, value := range map[string]string{
		"BANKING_DATABASE_DRIVER": "sqlite", "BANKING_POSTGRES_PORT": "0",
		"BANKING_POSTGRES_DATABASE": "banking;DROP DATABASE x", "BANKING_POSTGRES_SSLMODE": "prefer",
		"BANKING_POSTGRES_PASSWORD": "", "BANKING_POSTGRES_USER": "bad\nrole",
	} {
		t.Run(key, func(t *testing.T) {
			env := validEnv()
			env["BANKING_DATABASE_DRIVER"] = "postgres"
			env["BANKING_POSTGRES_PASSWORD"] = "isolated-test-password"
			env[key] = value
			if _, err := Load(func(k string) string { return env[k] }); err == nil {
				t.Fatal("accepted invalid PostgreSQL configuration")
			}
		})
	}
}
