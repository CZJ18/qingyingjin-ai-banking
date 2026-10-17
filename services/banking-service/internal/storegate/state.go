// Package storegate owns operator-only storage authority. No HTTP or Agent tool
// exposes these controls. Database triggers enforce the same state for old workers.
package storegate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"time"
)

var ErrFrozen = errors.New("BANKING_STORAGE_FROZEN")

type State struct {
	Mode           string `json:"mode"`
	Generation     int64  `json:"generation"`
	SnapshotDigest string `json:"snapshot_digest"`
}

func Status(ctx context.Context, db *sql.DB) (State, error) {
	var s State
	err := db.QueryRowContext(ctx, "SELECT mode,generation,snapshot_digest FROM banking_storage_control WHERE id=1").Scan(&s.Mode, &s.Generation, &s.SnapshotDigest)
	return s, err
}

func CheckActive(ctx context.Context, db *sql.DB) error {
	s, err := Status(ctx, db)
	if err != nil {
		return err
	}
	if s.Mode != "active" {
		return ErrFrozen
	}
	return nil
}

func validTransition(from, to string) bool {
	return from == "active" && to == "frozen" || from == "frozen" && (to == "active" || to == "standby") || from == "standby" && to == "active"
}

// Change must be called only after the operator verifies any peer snapshot.
// Its row lock waits for all writers that hold trigger SHARE locks to finish.
// An optional generation binds that verification to the target state, including
// an otherwise idempotent activation of the same snapshot.
func Change(ctx context.Context, db *sql.DB, dialect, expected, next, digest string, expectedGeneration ...int64) (State, error) {
	var s State
	if len(expectedGeneration) > 1 {
		return s, fmt.Errorf("at most one expected storage generation is allowed")
	}
	if !validTransition(expected, next) {
		return s, fmt.Errorf("invalid storage transition")
	}
	if next == "active" && len(digest) != 64 {
		return s, fmt.Errorf("verified snapshot digest required")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return s, err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, "SELECT mode,generation,snapshot_digest FROM banking_storage_control WHERE id=1 FOR UPDATE").Scan(&s.Mode, &s.Generation, &s.SnapshotDigest)
	if err != nil {
		return s, err
	}
	if len(expectedGeneration) == 1 && s.Generation != expectedGeneration[0] {
		return s, fmt.Errorf("storage generation changed; verify snapshot again before retry")
	}
	if s.Mode == next && (next != "active" || s.SnapshotDigest == digest) {
		return s, tx.Commit()
	}
	if s.Mode != expected {
		return s, fmt.Errorf("storage state changed; read status before retry")
	}
	q := "UPDATE banking_storage_control SET mode=?,generation=generation+1,snapshot_digest=?,updated_at=? WHERE id=1"
	a := "INSERT INTO banking_storage_transitions(id,from_mode,to_mode,generation,snapshot_digest,created_at) VALUES(?,?,?,?,?,?)"
	if dialect == "postgres" {
		q = "UPDATE banking_storage_control SET mode=$1,generation=generation+1,snapshot_digest=$2,updated_at=$3 WHERE id=1"
		a = "INSERT INTO banking_storage_transitions(id,from_mode,to_mode,generation,snapshot_digest,created_at) VALUES($1,$2,$3,$4,$5,$6)"
	}
	now := time.Now().UnixMilli()
	if _, err = tx.ExecContext(ctx, q, next, digest, now); err != nil {
		return s, err
	}
	if _, err = tx.ExecContext(ctx, a, uuid.NewString(), s.Mode, next, s.Generation+1, digest, now); err != nil {
		return s, err
	}
	s.Mode, s.Generation, s.SnapshotDigest = next, s.Generation+1, digest
	return s, tx.Commit()
}
