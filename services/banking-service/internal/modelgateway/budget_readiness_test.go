package modelgateway

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/qingyingjin/banking-service/internal/modelspend"
)

type readinessBudget struct {
	memoryBudget
	approval modelspend.ApprovalWindow
	reads    int
}

func (b *readinessBudget) UserBudget(context.Context, int64) (modelspend.UserWindow, error) {
	b.reads++
	return modelspend.UserWindow{Currency: "CNY", LimitUnits: 10000000000}, nil
}
func (b *readinessBudget) ApprovalBudget(context.Context) (modelspend.ApprovalWindow, error) {
	b.reads++
	return b.approval, nil
}

func TestBudgetSnapshotCannotAdvertiseExpiredOrReboundScopeAsReady(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*gateway, *readinessBudget)
		status int
		code   string
		reads  int
	}{
		{"valid", func(*gateway, *readinessBudget) {}, 200, "", 2},
		{"expired approval", func(g *gateway, _ *readinessBudget) {
			g.cfg.Approval.ValidUntil = time.Now().Add(-time.Second).UTC().Format(time.RFC3339)
		}, 403, "MODEL_APPROVAL_REQUIRED", 0},
		{"expired price policy", func(g *gateway, _ *readinessBudget) {
			g.cfg.Policy.ValidUntil = time.Now().Add(-time.Second).UTC().Format(time.RFC3339)
		}, 503, "POLICY_UNAVAILABLE", 0},
		{"wrong ledger approval", func(_ *gateway, b *readinessBudget) { b.approval.ApprovalSHA256 = strings.Repeat("f", 64) }, 503, "BUDGET_UNAVAILABLE", 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := policy()
			a := constructorApproval(p)
			b := &readinessBudget{approval: modelspend.ApprovalWindow{ApprovalSHA256: a.Fingerprint(), Currency: p.Currency, RemainingBudget: "1.00000000", MaxRequestRisk: "0.00100000", RemainingRequests: 1, RemainingConcurrent: 1}}
			g := &gateway{cfg: Config{Policy: p, Approval: &a, RunID: a.RunID, GatewayToken: token, VerifyIdentity: func(context.Context, string) (int64, error) { return 42, nil }}, budget: b}
			tc.change(g, b)
			r := httptest.NewRequest("GET", "/v1/budget", nil)
			r.Header.Set("Authorization", "Bearer "+token)
			r.Header.Set("X-Banking-Authorization", "Bearer synthetic-user")
			w := httptest.NewRecorder()
			g.ServeHTTP(w, r)
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &body)
			if w.Code != tc.status || body.Error.Code != tc.code || b.reads != tc.reads {
				t.Fatalf("status=%d code=%s ledger_reads=%d body=%s", w.Code, body.Error.Code, b.reads, w.Body.String())
			}
			if len(b.attempts) != 0 || len(b.reports) != 0 || b.unknown != 0 {
				t.Fatal("readiness changed monetary state")
			}
		})
	}
}
