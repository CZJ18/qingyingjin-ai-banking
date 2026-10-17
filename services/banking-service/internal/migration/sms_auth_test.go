package migration

import (
	"strings"
	"testing"
)

func TestSMSAuthMigrationContract(t *testing.T) {
	if CurrentVersion < 13 {
		t.Fatalf("SMS verification deployment requires schema 13, got %d", CurrentVersion)
	}
	for _, dialect := range []string{"mysql", "postgres"} {
		statements, err := smsAuthStatements(dialect)
		if err != nil {
			t.Fatal(err)
		}
		if len(statements) != 3 {
			t.Fatalf("%s: want two timestamps and a verification method, got %d", dialect, len(statements))
		}
		ddl := strings.Join(statements, ";")
		for _, required := range []string{"phone_verified_at BIGINT NOT NULL DEFAULT 0", "password_changed_at BIGINT NOT NULL DEFAULT 0", "phone_verification_method VARCHAR(16) NOT NULL DEFAULT ''"} {
			if !strings.Contains(ddl, required) {
				t.Errorf("%s missing reviewed SMS contract %q", dialect, required)
			}
		}
		for _, forbidden := range []string{"CREATE TABLE", "DROP ", "DELETE ", "UPDATE "} {
			if strings.Contains(ddl, forbidden) {
				t.Errorf("%s SMS migration must preserve existing users and 46 business tables: %s", dialect, forbidden)
			}
		}
	}
	if _, err := smsAuthStatements("sqlite"); err == nil {
		t.Fatal("unknown dialect accepted")
	}
}
