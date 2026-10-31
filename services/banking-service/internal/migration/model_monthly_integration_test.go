//go:build integration

package migration

import (
	"context"
	"fmt"
	"testing"
)

func TestMonthly021RerunPreservesEveryHistoricalChecksum(t *testing.T) {
	db, _ := spendSchemaFixture(t)
	history := func() string {
		rows, e := db.Query("SELECT version,checksum FROM banking_schema_migrations ORDER BY version")
		if e != nil {
			t.Fatal(e)
		}
		defer rows.Close()
		all := ""
		for rows.Next() {
			var version int
			var hash string
			if e := rows.Scan(&version, &hash); e != nil {
				t.Fatal(e)
			}
			all += fmt.Sprintf("%d:%s\n", version, hash)
		}
		if e := rows.Err(); e != nil {
			t.Fatal(e)
		}
		return all
	}
	before := history()
	if e := Apply(context.Background(), db); e != nil {
		t.Fatal("full migration replay", e)
	}
	if after := history(); after != before {
		t.Fatal("migration rewrote history")
	}
	t.Log("001..021 replayed against real SQL; all checksums unchanged")
}

func TestMonthly021SQLConstraintsAndImmutablePolicies(t *testing.T) {
	db, dialect := spendSchemaFixture(t)
	tx, e := db.Begin()
	if e != nil {
		t.Fatal(e)
	}
	defer tx.Rollback()
	limitID := "monthly-schema-limit"
	if _, e := tx.Exec(spendBind("INSERT INTO banking_model_user_limits(id,project_id,user_id,currency,limit_units,created_at) VALUES(?, 'monthly-schema-project',42,'USD',100,1)", dialect), limitID); e != nil {
		t.Fatal(e)
	}
	for i, statement := range []string{
		"UPDATE banking_model_user_limits SET limit_units=200 WHERE id='monthly-schema-limit'",
		"DELETE FROM banking_model_user_limits WHERE id='monthly-schema-limit'",
		"INSERT INTO banking_model_user_windows(id,limit_id,user_id,period,currency,limit_units,used_units,held_units,frozen,created_at,updated_at) VALUES('bad-window','monthly-schema-limit',42,'2026-10','USD',100,0,-1,false,1,1)",
	} {
		save := fmt.Sprintf("monthly_case_%d", i)
		if _, e := tx.Exec("SAVEPOINT " + save); e != nil {
			t.Fatal(e)
		}
		if _, e := tx.Exec(statement); e == nil {
			t.Fatal("database accepted unsafe policy/money mutation", i)
		}
		if _, e := tx.Exec("ROLLBACK TO SAVEPOINT " + save); e != nil {
			t.Fatal(e)
		}
	}
	if _, e := tx.Exec("UPDATE banking_storage_control SET mode='frozen' WHERE id=1"); e != nil {
		t.Fatal(e)
	}
	if _, e := tx.Exec("INSERT INTO banking_model_user_windows(id,limit_id,user_id,period,currency,limit_units,used_units,held_units,frozen,created_at,updated_at) VALUES('frozen-window','monthly-schema-limit',42,'2026-10','USD',100,0,1,false,1,1)"); e == nil {
		t.Fatal("frozen storage accepted monthly allocation")
	}
}
