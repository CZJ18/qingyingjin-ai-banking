package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"database/sql/driver"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type ImportOptions struct {
	// AllowBootstrapRules permits replacing only the exact six unmodified rules
	// installed by the baseline migration. Every other business table must be empty.
	AllowBootstrapRules bool
}

func control(ctx context.Context, tx *sql.Tx, dialect string) (string, int64, error) {
	var mode string
	var sequence int64
	err := tx.QueryRowContext(ctx, "SELECT mode,mutation_seq FROM banking_storage_control WHERE id=1 FOR SHARE").Scan(&mode, &sequence)
	return mode, sequence, err
}

func reportDigest(tables []TableDigest) string {
	raw, _ := json.Marshal(tables)
	h := sha256.Sum256(raw)
	return hex.EncodeToString(h[:])
}

func sameData(source, target []TableDigest) bool {
	if len(source) != len(target) {
		return false
	}
	for i := range source {
		if source[i] != target[i] {
			return false
		}
	}
	return true
}

func compareColumnContracts(ctx context.Context, source, target queryer, sourceDialect, targetDialect string) error {
	for _, table := range Tables {
		sc, e := columns(ctx, source, table, sourceDialect)
		if e != nil {
			return e
		}
		tc, e := columns(ctx, target, table, targetDialect)
		if e != nil {
			return e
		}
		if !sameColumns(sc, tc) {
			return fmt.Errorf("column contract mismatch in %s", table)
		}
	}
	return nil
}

// Compare holds storage fences and consistent snapshots in both databases.
// Active databases are rejected: a successful report is intended for cutover
// or rollback decisions, never for claiming two independently changing DBs agree.
func Compare(ctx context.Context, source, target *sql.DB, sourceDialect, targetDialect string) (Report, error) {
	r := Report{SourceDialect: sourceDialect, TargetDialect: targetDialect}
	if !validDialect(sourceDialect) || !validDialect(targetDialect) || source == target {
		return r, fmt.Errorf("distinct databases and supported dialects required")
	}
	s, e := source.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if e != nil {
		return r, e
	}
	defer s.Rollback()
	t, e := target.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if e != nil {
		return r, e
	}
	defer t.Rollback()
	sm, seq, e := control(ctx, s, sourceDialect)
	if e != nil {
		return r, e
	}
	r.SourceSequence = seq
	tm, seq, e := control(ctx, t, targetDialect)
	if e != nil {
		return r, e
	}
	r.TargetSequence = seq
	if sm != "frozen" || tm != "frozen" && tm != "standby" {
		return r, fmt.Errorf("comparison requires frozen source and frozen/standby target")
	}
	if e = compareColumnContracts(ctx, s, t, sourceDialect, targetDialect); e != nil {
		return r, e
	}
	if r.Source, e = inspect(ctx, s, sourceDialect); e != nil {
		return r, e
	}
	if r.Target, e = inspect(ctx, t, targetDialect); e != nil {
		return r, e
	}
	if r.SourceChecks, e = invariants(ctx, s); e != nil {
		return r, e
	}
	if r.TargetChecks, e = invariants(ctx, t); e != nil {
		return r, e
	}
	r.Digest = reportDigest(r.Source)
	r.Passed = sameData(r.Source, r.Target) && checksPass(r.SourceChecks) && checksPass(r.TargetChecks)
	if !r.Passed {
		return r, fmt.Errorf("snapshot reconciliation failed; see aggregate report")
	}
	return r, nil
}

// Import copies an offline frozen snapshot into an isolated standby database
// of the other supported dialect. All target writes commit atomically, including
// exact source outbox history. Existing financial data is never overwritten;
// an identical completed snapshot can only return a fully reverified receipt.
func Import(ctx context.Context, source, target *sql.DB, sourceDialect, targetDialect string, options ...ImportOptions) (Report, error) {
	r := Report{SourceDialect: sourceDialect, TargetDialect: targetDialect}
	if !validDialect(sourceDialect) || !validDialect(targetDialect) || sourceDialect == targetDialect || source == target {
		return r, fmt.Errorf("import requires distinct MySQL/PostgreSQL source and target")
	}
	var opts ImportOptions
	if len(options) > 1 {
		return r, fmt.Errorf("at most one import options value")
	}
	if len(options) == 1 {
		opts = options[0]
	}
	s, e := source.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if e != nil {
		return r, e
	}
	defer s.Rollback()
	sm, seq, e := control(ctx, s, sourceDialect)
	if e != nil {
		return r, e
	}
	r.SourceSequence = seq
	if sm != "frozen" {
		return r, fmt.Errorf("source must be frozen before snapshot import")
	}
	if r.SourceChecks, e = invariants(ctx, s); e != nil {
		return r, e
	}
	if !checksPass(r.SourceChecks) {
		return r, fmt.Errorf("source financial invariants failed; import refused")
	}
	// Pin MySQL's session variable to one operator connection and clear it before
	// returning that connection to the pool, including every failure path.
	targetConn, e := target.Conn(ctx)
	if e != nil {
		return r, e
	}
	defer targetConn.Close()
	if targetDialect == "mysql" {
		defer func() {
			cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := targetConn.ExecContext(cleanup, "SET @banking_migration_import=0"); err != nil {
				// Discard a connection if the operator session flag cannot be reset.
				targetConn.Raw(func(any) error { return driver.ErrBadConn })
			}
		}()
	}
	t, e := targetConn.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if e != nil {
		return r, e
	}
	defer t.Rollback()
	var mode, completedDigest string
	if e = t.QueryRowContext(ctx, "SELECT mode,mutation_seq,snapshot_digest FROM banking_storage_control WHERE id=1 FOR UPDATE").Scan(&mode, &r.TargetSequence, &completedDigest); e != nil {
		return r, e
	}
	if mode != "standby" {
		return r, fmt.Errorf("target must be standby before snapshot import")
	}
	// A committed snapshot receipt permits a read-only retry only after every
	// current row and invariant is reconciled again under the same held fences.
	// A populated target without a receipt, or with any later change, is never
	// overwritten or accepted as the completed import.
	if completedDigest != "" {
		if e = compareColumnContracts(ctx, s, t, sourceDialect, targetDialect); e != nil {
			return r, e
		}
		if r.Source, e = inspect(ctx, s, sourceDialect); e != nil {
			return r, e
		}
		if r.Target, e = inspect(ctx, t, targetDialect); e != nil {
			return r, e
		}
		if r.TargetChecks, e = invariants(ctx, t); e != nil {
			return r, e
		}
		r.Digest = reportDigest(r.Source)
		r.Passed = r.Digest == completedDigest && sameData(r.Source, r.Target) && checksPass(r.SourceChecks) && checksPass(r.TargetChecks)
		if !r.Passed {
			return r, fmt.Errorf("completed snapshot receipt reconciliation failed; target was not changed")
		}
		r.Replayed = true
		return r, nil
	}
	bootstrap := false
	for _, table := range Tables {
		var n int64
		if e = t.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+quote(table, targetDialect)).Scan(&n); e != nil {
			return r, e
		}
		if n == 0 {
			continue
		}
		if table != "banking_risk_rules" || !opts.AllowBootstrapRules || n != 6 {
			return r, fmt.Errorf("target business table %s is not empty", table)
		}
		if e = validateBootstrapRules(ctx, t); e != nil {
			return r, e
		}
		bootstrap = true
	}
	if _, e = t.ExecContext(ctx, "UPDATE banking_storage_control SET mode='loading',updated_at="+placeholder(1, targetDialect)+" WHERE id=1", time.Now().UnixMilli()); e != nil {
		return r, e
	}
	flagSQL := "SELECT set_config('banking.migration_import','on',true)"
	if targetDialect == "mysql" {
		flagSQL = "SET @banking_migration_import=1"
	}
	if _, e = t.ExecContext(ctx, flagSQL); e != nil {
		return r, e
	}
	if bootstrap {
		if _, e = t.ExecContext(ctx, "DELETE FROM banking_risk_rules"); e != nil {
			return r, e
		}
	}
	for _, table := range Tables {
		sc, e := columns(ctx, s, table, sourceDialect)
		if e != nil {
			return r, e
		}
		tc, e := columns(ctx, t, table, targetDialect)
		if e != nil {
			return r, e
		}
		if !sameColumns(sc, tc) {
			return r, fmt.Errorf("column contract mismatch in %s", table)
		}
		names, values := make([]string, len(tc)), make([]string, len(tc))
		for i, c := range tc {
			names[i], values[i] = quote(c.Name, targetDialect), placeholder(i+1, targetDialect)
			if c.Kind == "json" && targetDialect == "postgres" {
				values[i] += "::jsonb"
			}
		}
		statement, e := t.PrepareContext(ctx, "INSERT INTO "+quote(table, targetDialect)+" ("+strings.Join(names, ",")+") VALUES ("+strings.Join(values, ",")+")")
		if e != nil {
			return r, e
		}
		digest, scanErr := scanTable(ctx, s, table, sourceDialect, sc, func(args []any) error { _, e := statement.ExecContext(ctx, args...); return e })
		statement.Close()
		if scanErr != nil {
			return r, scanErr
		}
		r.Source = append(r.Source, digest)
		for _, c := range tc {
			if c.Identity && targetDialect == "postgres" {
				// setval is not transactional, but a failed import leaves empty tables;
				// a later import recalculates every sequence before successful commit.
				q := "SELECT setval(pg_get_serial_sequence($1,$2),COALESCE(MAX(" + quote(c.Name, targetDialect) + "),1),COUNT(*)>0) FROM " + quote(table, targetDialect)
				if _, e = t.ExecContext(ctx, q, table, c.Name); e != nil {
					return r, e
				}
			}
		}
	}
	if r.Target, e = inspect(ctx, t, targetDialect); e != nil {
		return r, e
	}
	if r.TargetChecks, e = invariants(ctx, t); e != nil {
		return r, e
	}
	r.Digest = reportDigest(r.Source)
	if !sameData(r.Source, r.Target) || !checksPass(r.TargetChecks) {
		return r, fmt.Errorf("import reconciliation failed; target transaction rolled back")
	}
	if _, e = t.ExecContext(ctx, "UPDATE banking_storage_control SET mode='standby',generation=generation+1,snapshot_digest="+placeholder(1, targetDialect)+",updated_at="+placeholder(2, targetDialect)+" WHERE id=1", r.Digest, time.Now().UnixMilli()); e != nil {
		return r, e
	}
	if e = t.Commit(); e != nil {
		return r, e
	}
	r.Passed = true
	return r, nil
}

func validateBootstrapRules(ctx context.Context, q queryer) error {
	expected := map[string]struct {
		level       string
		limit       int64
		description string
	}{
		"simulation_only":     {"critical", 0, "Only local simulated financial resources are allowed"},
		"bound_authorization": {"critical", 0, "Human authorization binds canonical operation digest and challenge"},
		"resource_ownership":  {"critical", 0, "All affected resources belong to authenticated owner"},
		"necessary_reserve":   {"high", 0, "Preserve known normal living obligations and safety buffers"},
		"amount_limit":        {"high", 1000000000000, "CNY integer minor units cannot exceed supported ledger limit"},
		"resource_version":    {"high", 0, "Sensitive resource terms and versions must still match"},
	}
	rows, e := q.QueryContext(ctx, "SELECT name,version,enabled,risk_level,limit_minor,description FROM banking_risk_rules")
	if e != nil {
		return e
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var name, level, description string
		var version, limit int64
		var enabled bool
		if e = rows.Scan(&name, &version, &enabled, &level, &limit, &description); e != nil {
			return e
		}
		v, ok := expected[name]
		if !ok || version != 1 || !enabled || level != v.level || limit != v.limit || description != v.description {
			return fmt.Errorf("target bootstrap risk rules differ from pristine baseline")
		}
		count++
	}
	if e = rows.Err(); e != nil {
		return e
	}
	if count != len(expected) {
		return fmt.Errorf("target bootstrap risk rules differ from pristine baseline")
	}
	return nil
}
