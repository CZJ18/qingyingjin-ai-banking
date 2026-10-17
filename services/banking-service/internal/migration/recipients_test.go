package migration

import (
	"strings"
	"testing"
)

func TestRecipientDirectoryMigrationAdditive(t *testing.T) {
	if CurrentVersion < 17 {
		t.Fatal("schema017 required")
	}
	for _, sql := range []string{recipientMySQLSQL, recipientPostgresSQL} {
		if !strings.Contains(sql, "ALTER TABLE banking_users ADD COLUMN transfer_directory") || strings.Contains(strings.ToUpper(sql), "DROP ") {
			t.Fatal("recipient migration must be additive")
		}
	}
}
