package migration

import (
	"strings"
	"testing"
)

func TestPhoneAuthMigrationContract(t *testing.T) {
	if CurrentVersion < 12 {
		t.Fatalf("phone/password deployment requires at least schema 12, got %d", CurrentVersion)
	}
	for _, dialect := range []string{"mysql", "postgres"} {
		statements, err := phoneAuthStatements(dialect)
		if err != nil {
			t.Fatal(err)
		}
		if len(statements) != 7 {
			t.Fatalf("%s: want five additive columns and two unique indexes, got %d", dialect, len(statements))
		}
		sql := strings.Join(statements, ";")
		for _, required := range []string{"phone_number VARCHAR(20) NULL", "password_hash VARCHAR(255) NOT NULL DEFAULT ''", "auth_version BIGINT NOT NULL DEFAULT 0", "refresh_hash VARCHAR(64) NULL", "refresh_expires_at BIGINT NOT NULL DEFAULT 0", "banking_users_phone_number_uq", "banking_users_refresh_hash_uq"} {
			if !strings.Contains(sql, required) {
				t.Errorf("%s missing reviewed auth contract %q", dialect, required)
			}
		}
		for _, forbidden := range []string{"CREATE TABLE", "DROP ", "DELETE ", "UPDATE "} {
			if strings.Contains(sql, forbidden) {
				t.Errorf("%s auth migration must preserve existing users and 46 business tables: %s", dialect, forbidden)
			}
		}
	}
	if _, err := phoneAuthStatements("sqlite"); err == nil {
		t.Fatal("unknown dialect accepted")
	}
}
