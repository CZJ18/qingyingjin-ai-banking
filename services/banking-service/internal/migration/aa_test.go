package migration

import (
	"strings"
	"testing"
)

func TestAAMigrationAdditive(t *testing.T) {
	if CurrentVersion < 16 {
		t.Fatal("AA schema 016 required")
	}
	for _, sql := range []string{aaMySQLSQL, aaPostgresSQL} {
		if !strings.Contains(sql, "ALTER TABLE banking_users ADD COLUMN aa_portfolio") || strings.Contains(strings.ToUpper(sql), "DROP ") {
			t.Fatal("AA migration must add one nullable user column")
		}
	}
}
