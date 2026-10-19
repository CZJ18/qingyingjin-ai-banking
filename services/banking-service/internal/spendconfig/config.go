// Package spendconfig loads separate model gateway/operator configurations.
package spendconfig

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"fmt"
	"net"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
	"github.com/qingyingjin/banking-service/internal/config"
	"github.com/qingyingjin/banking-service/internal/modelspend"
	"github.com/qingyingjin/banking-service/internal/outbound"
	"github.com/qingyingjin/banking-service/internal/platform"
)

var errConfig = errors.New("invalid model spend configuration")
var safeName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$`)

type Loaded struct {
	Env      map[string]string
	Database config.Config
	Policy   modelspend.Policy
	Outbound outbound.Policy
	Dispatch bool
}

func ReviewPrice(p modelspend.Policy, evidence []byte, confirmed bool) error {
	if !confirmed || len(evidence) == 0 || len(evidence) > 16<<20 || strings.HasPrefix(strings.ToLower(p.PriceSource), "synthetic:") || fmt.Sprintf("%x", sha256.Sum256(evidence)) != p.PriceEvidenceSHA256 {
		return errConfig
	}
	return nil
}

func Authorize(env map[string]string, presented string) error {
	expected := env["BANKING_MODEL_SPEND_ADMIN_TOKEN"]
	a, b := sha256.Sum256([]byte(expected)), sha256.Sum256([]byte(presented))
	if len(expected) < 32 || len(expected) > 512 || len(presented) < 32 || subtle.ConstantTimeCompare(a[:], b[:]) != 1 || !safeName.MatchString(env["BANKING_MODEL_SPEND_OPERATOR"]) {
		return errConfig
	}
	for _, name := range []string{"BANKING_USER_JWT_SECRET", "BANKING_TOOL_TOKEN", "BANKING_COZE_PAT", "BANKING_MODEL_GATEWAY_TOKEN", "BANKING_MODEL_SUPPLIER_API_KEY"} {
		if expected == env[name] {
			return errConfig
		}
	}
	return nil
}
func ValidateListener(environment, addr, cert, key string) error {
	if environment != "production" && environment != "development" && environment != "test" {
		return errConfig
	}
	if (cert == "") != (key == "") || environment == "production" && cert == "" {
		return errConfig
	}
	_, port, e := net.SplitHostPort(addr)
	if e != nil {
		return errConfig
	}
	n, e := strconv.Atoi(port)
	if e != nil || n < 1 || n > 65535 {
		return errConfig
	}
	return nil
}

// Load never opens a database and never merges process-level database settings.
// The explicit protected file owns the target; --expected-database is mandatory.
func Load(envPath, policyPath, expectedDatabase string, current bool) (Loaded, error) {
	var out Loaded
	if envPath == "" || policyPath == "" || expectedDatabase == "" {
		return out, errConfig
	}
	env, e := godotenv.Read(envPath)
	if e != nil {
		return out, errConfig
	}
	raw, e := os.ReadFile(policyPath)
	if e != nil || len(raw) > 16*1024 {
		return out, errConfig
	}
	p, e := modelspend.ParsePolicy(raw)
	if e != nil {
		return out, errConfig
	}
	if current && p.Validate(time.Now()) != nil {
		return out, errConfig
	}
	op, e := outbound.Parse(env["BANKING_ENVIRONMENT"], env["BANKING_HTTP_ALLOWED_ORIGINS"])
	if e != nil {
		return out, errConfig
	}
	c := config.Config{DatabaseDriver: env["BANKING_DATABASE_DRIVER"], CheckTimeout: 5 * time.Second, RedisAddr: "127.0.0.1:6379"}
	if c.DatabaseDriver == "" {
		c.DatabaseDriver = "mysql"
	}
	prefix := "BANKING_MYSQL_"
	if c.DatabaseDriver == "postgres" {
		prefix = "BANKING_POSTGRES_"
	} else if c.DatabaseDriver != "mysql" {
		return out, errConfig
	}
	host, name, user, password := env[prefix+"HOST"], env[prefix+"DATABASE"], env[prefix+"USER"], env[prefix+"PASSWORD"]
	port, e := strconv.Atoi(env[prefix+"PORT"])
	if e != nil || port < 1 || port > 65535 || strings.TrimSpace(host) == "" || !safeName.MatchString(name) || name != expectedDatabase || user == "" || password == "" {
		return out, errConfig
	}
	if c.DatabaseDriver == "postgres" {
		tls := env[prefix+"SSLMODE"]
		if tls != "disable" && tls != "verify-full" {
			return out, errConfig
		}
		if op.Environment == "production" && tls != "verify-full" {
			return out, errConfig
		}
		c.PostgresHost, c.PostgresPort, c.PostgresDatabase, c.PostgresUser, c.PostgresPassword, c.PostgresSSLMode = host, port, name, user, password, tls
	} else {
		tls := env[prefix+"TLS"]
		if tls == "" {
			tls = "false"
		}
		if tls != "true" && tls != "false" || op.Environment == "production" && tls != "true" {
			return out, errConfig
		}
		c.MySQLHost, c.MySQLPort, c.MySQLDatabase, c.MySQLUser, c.MySQLPassword, c.MySQLTLS = host, port, name, user, password, tls
	}
	out = Loaded{Env: env, Database: c, Policy: p, Outbound: op, Dispatch: current}
	return out, nil
}

// Open performs read-only target/schema checks; it never applies migrations.
func Open(ctx context.Context, c Loaded) (*platform.Connections, error) {
	conn, e := platform.Open(c.Database)
	if e != nil {
		return nil, errConfig
	}
	query := "SELECT DATABASE()"
	expected := c.Database.MySQLDatabase
	if c.Database.DatabaseDriver == "postgres" {
		query = "SELECT current_database()"
		expected = c.Database.PostgresDatabase
	}
	var actual string
	if e = conn.SQL.QueryRowContext(ctx, query).Scan(&actual); e != nil || actual != expected {
		conn.Close()
		return nil, errConfig
	}
	if e = Check(ctx, c, conn); e != nil {
		conn.Close()
		return nil, errConfig
	}
	return conn, nil
}

// Check is read-only. It validates real ledger columns, active authority and an
// existing policy binding rather than trusting a migration MAX marker alone.
func Check(ctx context.Context, c Loaded, conn *platform.Connections) error {
	if conn == nil || conn.SQL == nil {
		return errConfig
	}
	var version int
	if conn.SQL.QueryRowContext(ctx, "SELECT MAX(version) FROM banking_schema_migrations").Scan(&version) != nil || version != 20 {
		return errConfig
	}
	for _, query := range []string{"SELECT project_id,currency,policy_hash,limit_units,used_units,held_units,frozen FROM banking_model_spend_pools WHERE 1=0", "SELECT id,project_id,reserved_units,actual_units,response_body FROM banking_model_spend_attempts WHERE 1=0", "SELECT id,project_id,attempt_id,dedupe_key,amount_units,snapshot_json FROM banking_model_spend_events WHERE 1=0"} {
		rows, e := conn.SQL.QueryContext(ctx, query)
		if e != nil {
			return errConfig
		}
		if rows.Close() != nil {
			return errConfig
		}
	}
	hash, e := c.Policy.Hash()
	if e != nil {
		return errConfig
	}
	limit, _ := modelspend.ParseMoney(c.Policy.Limit)
	var pools []modelspend.Pool
	if conn.DB.WithContext(ctx).Where("project_id=?", c.Policy.ProjectID).Limit(1).Find(&pools).Error != nil {
		return errConfig
	}
	if len(pools) > 0 {
		p := pools[0]
		if p.Currency != c.Policy.Currency || p.PolicyHash != hash || p.LimitUnits != limit || p.UsedUnits < 0 || p.HeldUnits < 0 || c.Dispatch && p.Frozen {
			return errConfig
		}
	}
	if c.Dispatch {
		var mode string
		if c.Policy.Validate(time.Now()) != nil || conn.SQL.QueryRowContext(ctx, "SELECT mode FROM banking_storage_control WHERE id=1").Scan(&mode) != nil || mode != "active" {
			return errConfig
		}
	}
	return nil
}
