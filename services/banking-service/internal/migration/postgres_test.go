package migration

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestPostgresBaselinePreservesBusinessSchema(t *testing.T) {
	sourceFiles, err := filepath.Glob("0*.sql")
	if err != nil {
		t.Fatal(err)
	}
	tableRE := regexp.MustCompile(`CREATE TABLE IF NOT EXISTS (banking_[a-z_]+)`)
	var mysqlSQL, postgresSQL string
	for _, path := range sourceFiles {
		if !regexp.MustCompile(`^0[0-9]{2}_[a-z_]+\.sql$`).MatchString(path) {
			continue
		}
		b, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		mysqlSQL += string(b)
		b, e = os.ReadFile("postgres_" + path)
		if e != nil {
			t.Fatalf("missing PostgreSQL baseline for %s: %v", path, e)
		}
		postgresSQL += string(b)
	}
	sourceTables := tableRE.FindAllStringSubmatch(mysqlSQL, -1)
	targetTables := tableRE.FindAllStringSubmatch(postgresSQL, -1)
	if len(sourceTables) != 46 || len(targetTables) != 46 {
		t.Fatalf("business tables: MySQL=%d PostgreSQL=%d, want 46 each", len(sourceTables), len(targetTables))
	}
	for i, source := range sourceTables {
		if source[1] != targetTables[i][1] {
			t.Fatalf("table order mismatch: %s / %s", source[1], targetTables[i][1])
		}
	}
	for _, token := range []string{"AUTO_INCREMENT", "ENGINE=", "LONGBLOB", "INSERT IGNORE", "<=>", "UNIX_TIMESTAMP", "`"} {
		if strings.Contains(postgresSQL, token) {
			t.Errorf("unported MySQL syntax %q", token)
		}
	}
	for _, pattern := range []string{`\bCHECK\s*\(`, `\bFOREIGN KEY\s*\(`, `\bPRIMARY KEY\b`, `\bUNIQUE\b`} {
		re := regexp.MustCompile(pattern)
		if a, b := len(re.FindAllString(mysqlSQL, -1)), len(re.FindAllString(postgresSQL, -1)); a != b {
			t.Errorf("constraint %q count changed: %d / %d", pattern, a, b)
		}
	}
	if strings.Count(postgresSQL, "CREATE TRIGGER ") != 19 {
		t.Errorf("want 19 transactional replan triggers")
	}
	if !strings.Contains(postgresSQL, "gen_random_uuid()") || !strings.Contains(postgresSQL, "IS DISTINCT FROM") || !strings.Contains(postgresSQL, "clock_timestamp()") {
		t.Error("missing native UUID/time/null-safe change detection")
	}
	if !strings.Contains(postgresSQL, `idem_key VARCHAR(128) COLLATE "C"`) {
		t.Error("core idempotency keys must retain byte-sensitive equality")
	}
	if !strings.Contains(postgresSQL, "deterministic = false") {
		t.Error("missing explicit MySQL-compatible text equality collation")
	}
	indexRE := regexp.MustCompile(`(?:CREATE INDEX ([a-z_0-9]+)|CONSTRAINT ([a-z_0-9]+) UNIQUE)`)
	seen := map[string]bool{}
	for _, match := range indexRE.FindAllStringSubmatch(postgresSQL, -1) {
		name := match[1]
		if name == "" {
			name = match[2]
		}
		if len(name) > 63 || seen[name] {
			t.Errorf("invalid/colliding index name %q", name)
		}
		seen[name] = true
	}
}
