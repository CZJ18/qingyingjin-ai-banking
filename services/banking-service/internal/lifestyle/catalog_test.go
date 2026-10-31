package lifestyle

import (
	"context"
	"github.com/qingyingjin/banking-service/internal/entity"
	"testing"
)

func TestGoalCatalogRejectsInvalidScopeBeforeStorage(t *testing.T) {
	for _, in := range []struct {
		user          int64
		limit, offset int
	}{{0, 10, 0}, {42, 0, 0}, {42, 101, 0}, {42, 10, -1}, {42, 10, 1000001}} {
		_, e := New(nil).Goals(context.Background(), in.user, in.limit, in.offset)
		if entity.Code(e) != "INVALID_ARGUMENT" {
			t.Fatalf("invalid scope not rejected: %v", e)
		}
	}
	_, e := New(nil).Goals(context.Background(), 42, 10, 0)
	if entity.Code(e) != "DEPENDENCY_UNAVAILABLE" {
		t.Fatalf("nil database not refused: %v", e)
	}
}
