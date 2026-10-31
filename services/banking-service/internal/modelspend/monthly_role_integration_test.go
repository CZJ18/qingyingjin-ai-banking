//go:build integration

package modelspend

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/platform"
)

func TestSQLMonthlyRestrictedPostgresGatewayRole(t *testing.T) {
	s, admin := spendHarness(t, 100)
	if admin.DB.Dialector.Name() != "postgres" {
		t.Skip("PostgreSQL least-privilege contract; MySQL role contract is separate")
	}
	ctx := WithUser(context.Background(), 42)
	if e := s.ProvisionUserLimit(ctx, 42, FormatMoney(s.reserveUnits)); e != nil {
		t.Fatal(e)
	}
	values, e := godotenv.Read(os.Getenv("BANKING_SPEND_TEST_ENV"))
	if e != nil {
		t.Fatal("fixture configuration unavailable")
	}
	cfg, e := config.Load(func(k string) string { return values[k] })
	if e != nil {
		t.Fatal("invalid fixture")
	}
	role := "monthly_gateway_" + strings.ReplaceAll(uuid.NewString(), "-", "")[:12]
	password := uuid.NewString() + uuid.NewString()
	if _, e := admin.SQL.Exec("CREATE ROLE " + role + " LOGIN PASSWORD '" + password + "' NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS"); e != nil {
		t.Fatal("cannot provision isolated restricted role")
	}
	defer func() {
		if _, e := admin.SQL.Exec("DROP OWNED BY " + role); e != nil {
			t.Error("cannot remove isolated role grants")
		}
		if _, e := admin.SQL.Exec("DROP ROLE " + role); e != nil {
			t.Error("cannot remove isolated role")
		}
	}()
	for _, grant := range []string{
		"GRANT USAGE ON SCHEMA public TO ",
		"GRANT SELECT ON banking_model_user_limits,banking_schema_migrations,banking_storage_control TO ",
		"GRANT SELECT,INSERT,UPDATE ON banking_model_user_windows,banking_model_spend_pools,banking_model_spend_attempts TO ",
		"GRANT SELECT,INSERT ON banking_model_attempt_owners,banking_model_spend_events TO ",
	} {
		if _, e := admin.SQL.Exec(grant + role); e != nil {
			t.Fatal("cannot apply documented gateway grants")
		}
	}
	cfg.PostgresUser, cfg.PostgresPassword = role, password
	runtime, e := platform.Open(cfg)
	if e != nil {
		t.Fatal("restricted role connection unavailable")
	}
	defer runtime.Close()
	var mayUpdate, super, bypass bool
	if e := runtime.SQL.QueryRow("SELECT has_table_privilege(current_user,'banking_model_user_limits','UPDATE'),rolsuper,rolbypassrls FROM pg_roles WHERE rolname=current_user").Scan(&mayUpdate, &super, &bypass); e != nil || mayUpdate || super || bypass {
		t.Fatal("gateway role is not restricted")
	}
	gateway, e := New(runtime.DB, s.policy)
	if e != nil {
		t.Fatal(e)
	}
	a, e := gateway.Reserve(ctx, "restricted-gateway", strings.Repeat("a", 64))
	if e != nil {
		t.Fatal("documented restricted gateway role cannot reserve", e)
	}
	if e := gateway.Unknown(ctx, a.ID); e != nil {
		t.Fatal("restricted role cannot preserve unknown outcome", e)
	}
	w, e := gateway.UserBudget(ctx, 42)
	if e != nil || w.HeldUnits != s.reserveUnits || w.UsedUnits != 0 {
		t.Fatal("restricted role monetary persistence", w, e)
	}
	t.Log("actual nonowner PostgreSQL role: no limits UPDATE, no superuser/BYPASSRLS; reservation and unknown hold persisted")
}
