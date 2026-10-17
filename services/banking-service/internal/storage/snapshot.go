package storage

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type queryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type column struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Nullable bool   `json:"nullable"`
	Identity bool   `json:"-"`
}

func identifier(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r != '_' && (r < 'a' || r > 'z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func quote(s, dialect string) string {
	if dialect == "mysql" {
		return "`" + s + "`"
	}
	return `"` + s + `"`
}

func placeholder(n int, dialect string) string {
	if dialect == "mysql" {
		return "?"
	}
	return fmt.Sprintf("$%d", n)
}

func validDialect(s string) bool { return s == "mysql" || s == "postgres" }

func columns(ctx context.Context, q queryer, table, dialect string) ([]column, error) {
	query := "SELECT column_name,data_type,is_nullable,extra FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name=? ORDER BY column_name"
	if dialect == "postgres" {
		query = "SELECT column_name,data_type,is_nullable,CASE WHEN is_identity='YES' OR column_default LIKE 'nextval(%' THEN 'auto_increment' ELSE '' END FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1 ORDER BY column_name"
	}
	rows, err := q.QueryContext(ctx, query, table)
	if err != nil {
		return nil, fmt.Errorf("inspect columns for %s: %w", table, err)
	}
	defer rows.Close()
	var out []column
	for rows.Next() {
		var c column
		var typ, nullable, extra string
		if e := rows.Scan(&c.Name, &typ, &nullable, &extra); e != nil {
			return nil, e
		}
		if !identifier(c.Name) {
			return nil, fmt.Errorf("unsafe column identifier")
		}
		c.Kind, err = kind(typ)
		if err != nil {
			return nil, err
		}
		c.Nullable, c.Identity = nullable == "YES", strings.Contains(extra, "auto_increment")
		out = append(out, c)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("missing registered business table %s", table)
	}
	return out, nil
}

type rowDigest struct {
	key    string
	digest [32]byte
}

// Sort hashes in Go, rather than depending on the databases' different text
// collations. This keeps only primary keys and 32-byte digests in memory.
func digestTable(cols []column, rows []rowDigest) string {
	sort.Slice(rows, func(i, j int) bool { return rows[i].key < rows[j].key })
	h := sha256.New()
	header, _ := json.Marshal(cols)
	h.Write(header)
	h.Write([]byte{'\n'})
	for _, r := range rows {
		h.Write(r.digest[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}

func scanTable(ctx context.Context, q queryer, table, dialect string, cols []column, copyRow func([]any) error) (TableDigest, error) {
	result := TableDigest{Table: table}
	names := make([]string, len(cols))
	pk := primaryKey(table)
	pkIndex := make([]int, len(pk))
	for i := range pkIndex {
		pkIndex[i] = -1
	}
	for i, c := range cols {
		names[i] = quote(c.Name, dialect)
		for j, name := range pk {
			if c.Name == name {
				pkIndex[j] = i
			}
		}
	}
	for _, i := range pkIndex {
		if i < 0 {
			return result, fmt.Errorf("missing registered primary key in %s", table)
		}
	}
	rows, err := q.QueryContext(ctx, "SELECT "+strings.Join(names, ",")+" FROM "+quote(table, dialect))
	if err != nil {
		return result, fmt.Errorf("read %s: %w", table, err)
	}
	defer rows.Close()
	digests := []rowDigest{}
	for rows.Next() {
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range ptrs {
			ptrs[i] = &raw[i]
		}
		if e := rows.Scan(ptrs...); e != nil {
			return result, e
		}
		cells := make([]cell, len(cols))
		args := make([]any, len(cols))
		for i, c := range cols {
			cells[i], args[i], err = normalize(raw[i], c.Kind)
			if err != nil {
				return result, fmt.Errorf("normalize %s.%s: %w", table, c.Name, err)
			}
			if cells[i].Kind == "null" && !c.Nullable {
				return result, fmt.Errorf("unexpected NULL in %s.%s", table, c.Name)
			}
		}
		key := make([]cell, len(pkIndex))
		for i, j := range pkIndex {
			key[i] = cells[j]
		}
		keyJSON, _ := json.Marshal(key)
		rowJSON, _ := json.Marshal(cells)
		digests = append(digests, rowDigest{key: string(keyJSON), digest: sha256.Sum256(rowJSON)})
		if copyRow != nil {
			if e := copyRow(args); e != nil {
				return result, fmt.Errorf("insert %s: %w", table, e)
			}
		}
		result.Rows++
	}
	if err = rows.Err(); err != nil {
		return result, err
	}
	result.SHA256 = digestTable(cols, digests)
	return result, nil
}

func sameColumns(source, target []column) bool {
	if len(source) != len(target) {
		return false
	}
	for i, c := range source {
		if c.Name != target[i].Name || c.Kind != target[i].Kind || c.Nullable != target[i].Nullable || c.Identity != target[i].Identity {
			return false
		}
	}
	return true
}

func inspect(ctx context.Context, q queryer, dialect string) ([]TableDigest, error) {
	results := make([]TableDigest, 0, len(Tables))
	for _, table := range Tables {
		cols, e := columns(ctx, q, table, dialect)
		if e != nil {
			return results, e
		}
		value, e := scanTable(ctx, q, table, dialect, cols, nil)
		if e != nil {
			return results, e
		}
		results = append(results, value)
	}
	return results, nil
}
