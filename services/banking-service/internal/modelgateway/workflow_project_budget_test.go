package modelgateway

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/qingyingjin/banking-service/internal/modelspend"
)

type grantBudget struct {
	memoryBudget
	pool      modelspend.Pool
	err       error
	userCalls int
}

func (b *grantBudget) ProjectBudget(context.Context) (modelspend.Pool, error) { return b.pool, b.err }
func (b *grantBudget) UserBudget(context.Context, int64) (modelspend.UserWindow, error) {
	b.userCalls++
	return modelspend.UserWindow{LimitUnits: 1000000000}, nil
}

func TestWorkflowGrantChecksProjectBudgetBeforeMinting(t *testing.T) {
	p := policy()
	hash, _ := p.Hash()
	bound, _ := p.ReserveUnits()
	limit, _ := modelspend.ParseMoney(p.Limit)
	base := modelspend.Pool{ProjectID: p.ProjectID, Currency: p.Currency, PolicyHash: hash, LimitUnits: limit}
	for _, tc := range []struct {
		name   string
		change func(*grantBudget)
		status int
		code   string
	}{
		{"available", func(*grantBudget) {}, 200, ""},
		{"held exhaustion", func(b *grantBudget) { b.pool.HeldUnits = limit }, 429, "MODEL_SPEND_QUOTA_EXCEEDED"},
		{"settled exhaustion", func(b *grantBudget) { b.pool.UsedUnits = limit }, 429, "MODEL_SPEND_QUOTA_EXCEEDED"},
		{"short by one", func(b *grantBudget) { b.pool.HeldUnits = limit - bound + 1 }, 429, "MODEL_SPEND_QUOTA_EXCEEDED"},
		{"exact remaining", func(b *grantBudget) { b.pool.HeldUnits = limit - bound }, 200, ""},
		{"frozen", func(b *grantBudget) { b.pool.Frozen = true }, 429, "MODEL_SPEND_QUOTA_EXCEEDED"},
		{"read failure", func(b *grantBudget) { b.err = errors.New("private db detail") }, 503, "BUDGET_UNAVAILABLE"},
		{"wrong policy", func(b *grantBudget) { b.pool.PolicyHash = strings.Repeat("f", 64) }, 503, "BUDGET_UNAVAILABLE"},
		{"negative accounting", func(b *grantBudget) { b.pool.UsedUnits = -1 }, 503, "BUDGET_UNAVAILABLE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &grantBudget{pool: base}
			tc.change(b)
			g := &gateway{cfg: Config{Policy: p, GatewayToken: token, WorkflowPolicies: map[string]string{"123": strings.Repeat("a", 64)}, VerifyTask: func(context.Context, string) (TaskIdentity, error) {
				return TaskIdentity{UserID: 42, TaskID: "task-1", Version: 1, RequestDigest: strings.Repeat("b", 64), LeaseFingerprint: strings.Repeat("c", 64)}, nil
			}}, budget: b, policyHash: hash}
			r := httptest.NewRequest("POST", "/v1/workflow-grants", strings.NewReader(`{"workflow_id":"123","input_sha256":"`+strings.Repeat("d", 64)+`"}`))
			r.Header.Set("X-Banking-Authorization", "Bearer synthetic-task")
			w := httptest.NewRecorder()
			g.mintWorkflow(w, r)
			if w.Code != tc.status {
				t.Fatalf("status=%d want=%d body=%s", w.Code, tc.status, w.Body.String())
			}
			if tc.code != "" {
				var got struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				_ = json.Unmarshal(w.Body.Bytes(), &got)
				if got.Error.Code != tc.code {
					t.Fatalf("code=%q want=%q", got.Error.Code, tc.code)
				}
				if strings.Contains(w.Body.String(), "capability") || len(b.attempts) > 0 || b.userCalls != 0 {
					t.Fatal("rejected project minted a grant or reserved money")
				}
			}
		})
	}
}
