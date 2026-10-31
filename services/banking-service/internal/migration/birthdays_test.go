package migration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"strings"
	"testing"
)

func TestBirthdayCalendar023IsDedicatedAndAdditive(t *testing.T) {
	if CurrentVersion < 23 {
		t.Fatal("birthday schema023 required")
	}
	for _, ddl := range []string{birthdayMySQLSQL, birthdayPostgresSQL} {
		if !strings.Contains(ddl, "ALTER TABLE banking_users ADD COLUMN birthday_calendar") || strings.Contains(strings.ToUpper(ddl), "DROP ") || strings.Contains(ddl, "transfer_directory") || strings.Contains(ddl, "renewal_reminders") {
			t.Fatal("birthday calendar must use a dedicated additive column")
		}
	}
}

// Reuse the existing migration driver; only route the new checksum and empty
// column probe. No real database is opened by this test.
type birthdayMigrationConn struct{ *spendMigrationConn }

func (c *birthdayMigrationConn) QueryContext(ctx context.Context, q string, args []driver.NamedValue) (driver.Rows, error) {
	if strings.Contains(q, "information_schema.columns") {
		return &spendMigrationRows{}, nil
	}
	return c.spendMigrationConn.QueryContext(ctx, strings.ReplaceAll(q, "version=23", "version=20"), args)
}

type birthdayMigrationConnector struct{ c *birthdayMigrationConn }

func (c birthdayMigrationConnector) Connect(context.Context) (driver.Conn, error) { return c.c, nil }
func (c birthdayMigrationConnector) Driver() driver.Driver                        { return spendMigrationDriver{} }

func TestBirthdayCalendar023ChecksumReplayAndMismatch(t *testing.T) {
	for _, dialect := range []string{"mysql", "postgres"} {
		fixture := &birthdayMigrationConn{&spendMigrationConn{}}
		db := sql.OpenDB(birthdayMigrationConnector{fixture})
		ddl := birthdayMySQLSQL
		if dialect == "postgres" {
			ddl = birthdayPostgresSQL
		}
		if err := applyBirthdaysOn(context.Background(), db, dialect); err != nil {
			t.Fatal(dialect, err)
		}
		if fixture.previous != fmt.Sprintf("%x", sha256.Sum256([]byte(ddl))) || len(fixture.executed) != 2 {
			t.Fatal("migration must record exact checksum after one additive DDL", fixture.executed)
		}
		if err := applyBirthdaysOn(context.Background(), db, dialect); err != nil || len(fixture.executed) != 2 {
			t.Fatal("identical replay mutated schema", err)
		}
		fixture.previous = strings.Repeat("0", 64)
		if err := applyBirthdaysOn(context.Background(), db, dialect); err == nil || len(fixture.executed) != 2 {
			t.Fatal("checksum mismatch mutated schema", err)
		}
		db.Close()
	}
}
