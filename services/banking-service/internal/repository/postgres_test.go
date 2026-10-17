package repository

import (
	"fmt"
	"github.com/qingyingjin/banking-service/internal/entity"
	"testing"
)

type postgresStateError struct{ state string }

func (e postgresStateError) Error() string    { return "sensitive database detail omitted" }
func (e postgresStateError) SQLState() string { return e.state }

func TestPostgresUniqueAndConcurrentErrors(t *testing.T) {
	for _, tc := range []struct{ state, code string }{{"23505", "IDEMPOTENCY_CONFLICT"}, {"40001", "VERSION_CONFLICT"}, {"40P01", "VERSION_CONFLICT"}} {
		if got := entity.Code(DBError(fmt.Errorf("wrapped: %w", postgresStateError{tc.state}))); got != tc.code {
			t.Fatalf("state %s: %s", tc.state, got)
		}
	}
}
