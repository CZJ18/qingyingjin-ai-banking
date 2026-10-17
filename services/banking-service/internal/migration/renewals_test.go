package migration

import (
	"strings"
	"testing"
)

func TestRenewalMigrationIsAdditive(t *testing.T) {
	if CurrentVersion < 15 {
		t.Fatal("schema 15 required")
	}
	for _, ddl := range []string{renewalMySQLSQL, renewalPostgresSQL} {
		if !strings.Contains(ddl, "ALTER TABLE banking_users ADD COLUMN renewal_reminders") || strings.Contains(ddl, "DROP") {
			t.Fatal("invalid renewal migration")
		}
	}
}
