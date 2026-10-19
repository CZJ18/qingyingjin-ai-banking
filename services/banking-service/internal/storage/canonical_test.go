package storage

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestCanonicalJSONPreservesLargeIntegerAndSortsKeys(t *testing.T) {
	a, e := jsonCanonical([]byte(`{"z":9223372036854775807,"a":{"b":10029}}`))
	if e != nil || string(a) != `{"a":{"b":10029},"z":9223372036854775807}` {
		t.Fatal(string(a), e)
	}
	for _, bad := range []string{`{"a":`, `{} {}`, `[] trailing`} {
		if _, e := jsonCanonical([]byte(bad)); e == nil {
			t.Fatal("accepted invalid stored JSON")
		}
	}
}

func TestNormalizeFinancialIntegersBinaryAndNull(t *testing.T) {
	c, v, e := normalize([]byte("9223372036854775807"), "integer")
	if e != nil || c.Value != "9223372036854775807" || v != int64(9223372036854775807) {
		t.Fatal(c, e)
	}
	raw := []byte{0, 255, 34, 10}
	_, v, e = normalize(raw, "binary")
	raw[0] = 1
	if e != nil || !bytes.Equal(v.([]byte), []byte{0, 255, 34, 10}) {
		t.Fatal("idempotency response bytes mutated")
	}
	null, _, _ := normalize(nil, "json")
	jsonNull, _, _ := normalize([]byte("null"), "json")
	if null == jsonNull {
		t.Fatal("SQL NULL must differ from JSON null")
	}
	for _, invalid := range []string{"9223372036854775808", "1.5"} {
		if _, _, e := normalize(invalid, "integer"); e == nil {
			t.Fatal("accepted invalid integer")
		}
	}
	if _, _, e := normalize("2", "boolean"); e == nil {
		t.Fatal("accepted invalid boolean")
	}
}

func TestManifestHasExactlyBusinessSchemaAndSpecialKeys(t *testing.T) {
	seen := map[string]bool{}
	for _, table := range Tables {
		if !identifier(table) || seen[table] || table == "banking_schema_migrations" {
			t.Fatal(table)
		}
		seen[table] = true
	}
	if len(seen) != 52 || len(primaryKey("banking_core_idempotency")) != 3 || primaryKey("banking_transfer_approvals")[0] != "transfer_id" || primaryKey("banking_model_spend_pools")[0] != "project_id" {
		t.Fatal("manifest drift")
	}
	// The allowlist must cover the real persisted schema, rather than merely
	// containing the expected number of guessed table names.
	createTable := regexp.MustCompile(`CREATE TABLE (?:IF NOT EXISTS )?(banking_[a-z_]+)`)
	found := map[string]bool{}
	for version := 1; version <= 10; version++ {
		paths, e := filepath.Glob(filepath.Join("..", "migration", fmt.Sprintf("%03d_*.sql", version)))
		if e != nil || len(paths) != 1 {
			t.Fatal("cannot inspect actual financial migrations")
		}
		raw, e := os.ReadFile(paths[0])
		if e != nil {
			t.Fatal(e)
		}
		for _, match := range createTable.FindAllStringSubmatch(string(raw), -1) {
			if !seen[match[1]] {
				t.Fatal("persisted table excluded from snapshot: " + match[1])
			}
			found[match[1]] = true
		}
	}
	for _, filename := range []string{"model_budget_018_mysql.sql", "model_accounting_019_mysql.sql", "model_spend_020_mysql.sql"} {
		raw, e := os.ReadFile(filepath.Join("..", "migration", filename))
		if e != nil {
			t.Fatal(e)
		}
		for _, match := range createTable.FindAllStringSubmatch(string(raw), -1) {
			if !seen[match[1]] {
				t.Fatal("quota table missing from snapshot")
			}
			found[match[1]] = true
		}
	}
	if len(found) != len(seen) {
		t.Fatal("snapshot manifest contains nonexistent business tables")
	}
}

func TestDigestIndependentOfSQLCollationOrder(t *testing.T) {
	columns := []column{{Name: "id", Kind: "text"}, {Name: "amount_minor", Kind: "integer"}}
	first := []rowDigest{{key: "B", digest: [32]byte{1}}, {key: "a", digest: [32]byte{2}}}
	second := []rowDigest{{key: "a", digest: [32]byte{2}}, {key: "B", digest: [32]byte{1}}}
	if digestTable(columns, first) != digestTable(columns, second) {
		t.Fatal("database collation changed snapshot hash")
	}
}
