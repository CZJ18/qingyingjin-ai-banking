//go:build integration

package migration

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/platform"
	"os"
	"strings"
	"testing"
)

func TestSkillsDDLRecovery(t *testing.T) {
	if p := os.Getenv("BANKING_TEST_ENV_FILE"); p != "" {
		if e := godotenv.Load(p); e != nil {
			t.Fatal(e)
		}
	}
	cfg, e := config.Load(os.Getenv)
	if e != nil {
		t.Fatal(e)
	}
	db, e := platform.Open(cfg)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	ctx := context.Background()
	conn, e := db.SQL.Conn(ctx)
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	// Isolated fixture table; never remove production fields or migration records.
	name := "banking_skill_test_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	if _, e = conn.ExecContext(ctx, "CREATE TABLE "+name+" (id BIGINT PRIMARY KEY) ENGINE=InnoDB"); e != nil {
		t.Fatal(e)
	}
	defer conn.ExecContext(ctx, "DROP TABLE "+name)
	first := fmt.Sprintf("ALTER TABLE %s ADD COLUMN skill_id VARCHAR(64) NOT NULL DEFAULT ''", name)
	second := fmt.Sprintf("ALTER TABLE %s ADD COLUMN skill_version BIGINT NOT NULL DEFAULT 0", name)
	if e = applySkillColumn(ctx, conn, first); e != nil {
		t.Fatal(e)
	}
	// Simulate restart after the first independently committed ALTER.
	for range 2 {
		for _, sql := range []string{first, second} {
			if e = applySkillColumn(ctx, conn, sql); e != nil {
				t.Fatal(e)
			}
		}
	}
	var count int
	if e = conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? AND column_name IN ('skill_id','skill_version')", name).Scan(&count); e != nil || count != 2 {
		t.Fatal(count, e)
	}
}
