package spendconfig

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/qingyingjin/banking-service/internal/platform"
)

// A local SQL driver stops at the first ledger-column check. No sockets,
// environment, credentials, migrations or financial writes are involved.
type schemaBoundaryDriver struct{ version, queries int }
type schemaBoundaryRows struct {
	version int
	read    bool
}

func (d *schemaBoundaryDriver) Connect(context.Context) (driver.Conn, error) { return d, nil }
func (d *schemaBoundaryDriver) Driver() driver.Driver                        { return d }

func (d *schemaBoundaryDriver) Open(string) (driver.Conn, error) { return d, nil }
func (d *schemaBoundaryDriver) Close() error                     { return nil }
func (d *schemaBoundaryDriver) Begin() (driver.Tx, error)        { return nil, errors.New("writes forbidden") }
func (d *schemaBoundaryDriver) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("prepare forbidden")
}
func (d *schemaBoundaryDriver) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	d.queries++
	if query != "SELECT MAX(version) FROM banking_schema_migrations" {
		return nil, errors.New("ledger column intentionally missing")
	}
	return &schemaBoundaryRows{version: d.version}, nil
}
func (r *schemaBoundaryRows) Columns() []string { return []string{"version"} }
func (r *schemaBoundaryRows) Close() error      { return nil }
func (r *schemaBoundaryRows) Next(dest []driver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read = true
	dest[0] = int64(r.version)
	return nil
}

func TestSchemaCompatibilityNeverSkipsLedgerValidation(t *testing.T) {
	for _, version := range []int{20, 21, 22, 23, 24} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			d := &schemaBoundaryDriver{version: version}
			db := sql.OpenDB(d)
			defer db.Close()
			if Check(context.Background(), Loaded{}, &platform.Connections{SQL: db}) == nil {
				t.Fatal("incomplete ledger accepted")
			}
			want := 1
			if version >= 21 && version <= 23 {
				want = 2
			}
			if d.queries != want {
				t.Fatalf("schema %d made %d queries, want %d; reviewed schemas must continue to ledger checks and unknown schemas must stop", version, d.queries, want)
			}
		})
	}
}
