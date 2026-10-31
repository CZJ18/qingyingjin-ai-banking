package bankingscope

import (
	"context"
	"os"
)

// DedicatedMode closes direct provider/embedding exits for the dedicated
// Banking runtime. Malformed or whitespace-only nonempty configuration still
// activates the guard: an invalid scope config must never reopen paid egress.
func DedicatedMode() bool { return os.Getenv("BANKING_COZE_WORKFLOW_DIGESTS") != "" }

func DenyDirectEgress(ctx context.Context) bool { return DedicatedMode() || Present(ctx) }
