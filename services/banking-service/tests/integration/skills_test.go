//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/routers/legacy"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/api"
	"github.com/qingyingjin/banking-service/internal/billing"
	"github.com/qingyingjin/banking-service/internal/httpapi"
	"github.com/qingyingjin/banking-service/internal/orchestrator"
	"github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/risk"
	"github.com/qingyingjin/banking-service/internal/service"
	"github.com/qingyingjin/banking-service/internal/skills"
	"github.com/qingyingjin/banking-service/internal/subscription"
	"io"
	"log/slog"
	"net/http/httptest"
	"testing"
	"time"
)

func TestSkillsPersistentBindingsAndRoleIsolation(t *testing.T) {
	h := harness(t)
	ctx := context.Background()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bills := billing.New(billing.NewRepository(h.conn.DB), nil)
	subs := subscription.New(repository.New(h.conn.DB), risk.SimulationGateway{}, subscription.LocalSimulator{})
	tools := httptest.NewServer(httpapi.NewWithSubscriptions(h.auth, log, nil, time.Second, h.svc, nil, bills, subs))
	defer tools.Close()
	executor, err := orchestrator.NewHTTPExecutor(tools.URL, h.auth, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	jobs := orchestrator.New(h.conn.DB, executor, executor)
	defer h.conn.DB.Model(&orchestrator.Job{}).Where("user_id=?", h.uid).Update("status", "cancelled")
	m := service.Meta{UserID: h.uid, TraceID: uuid.NewString()}
	submit := func() orchestrator.View {
		v, e := jobs.Submit(ctx, m, uuid.NewString(), orchestrator.SubmitInput{Tasks: []orchestrator.TaskSpec{{Key: "bill", AgentType: "billing", Tool: "get_transactions", Parameters: json.RawMessage(`{"from":"2026-01-01T00:00:00Z","to":"2026-02-01T00:00:00Z"}`)}}})
		if e != nil {
			t.Fatal(e)
		}
		return v
	}
	v := submit()
	if v.Tasks[0].SkillID != "billing_analysis" || v.Tasks[0].SkillVersion != 1 {
		t.Fatal("not persisted", v.Tasks)
	}
	// A historical legal binding must normalize before a real protected HTTP call.
	h.conn.DB.Model(&orchestrator.AgentTask{}).Where("id=?", v.Tasks[0].ID).Updates(map[string]any{"skill_id": "", "skill_version": 0})
	if _, err = jobs.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	recovered := orchestrator.New(h.conn.DB, executor, executor)
	got, err := recovered.Get(ctx, h.uid, v.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Job.Status != "succeeded" || got.Tasks[0].SkillID != "billing_analysis" || got.Tasks[0].SkillVersion != 1 {
		t.Fatal("legacy binding recovery", got)
	}
	trace, err := recovered.Trace(ctx, h.uid, v.Job.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(trace.Executions) != 1 || !json.Valid(trace.Executions[0].Input) {
		t.Fatal("missing runtime evidence")
	}
	var input map[string]any
	json.Unmarshal(trace.Executions[0].Input, &input)
	if input["skill_id"] != "billing_analysis" || input["rule_version"] != "1" {
		t.Fatal("missing skill evidence", input)
	}
	// Reading an old terminal task adds metadata without scheduling it again.
	h.conn.DB.Model(&orchestrator.AgentTask{}).Where("id=?", v.Tasks[0].ID).Updates(map[string]any{"skill_id": "", "skill_version": 0})
	terminal, e := recovered.Get(ctx, h.uid, v.Job.ID)
	if e != nil {
		t.Fatal(e)
	}
	if terminal.Tasks[0].SkillID != "billing_analysis" || terminal.Tasks[0].Attempts != 1 || terminal.Tasks[0].Status != "succeeded" {
		t.Fatal("terminal compatibility", terminal)
	}
	invalid := submit()
	h.conn.DB.Model(&orchestrator.AgentTask{}).Where("id=?", invalid.Tasks[0].ID).Update("skill_version", 99)
	if _, err = recovered.ProcessOne(ctx); err != nil {
		t.Fatal(err)
	}
	bad, err := recovered.Get(ctx, h.uid, invalid.Job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if bad.Tasks[0].Status != "review_required" || bad.Tasks[0].Attempts != 0 {
		t.Fatal("corrupt binding executed", bad)
	}
	h.server.Close()
	h.server = tools
	doc, e := openapi3.NewLoader().LoadFromData(api.OpenAPI)
	if e != nil {
		t.Fatal(e)
	}
	doc.Servers = openapi3.Servers{{URL: tools.URL}}
	h.contract, e = legacy.NewRouter(doc)
	if e != nil {
		t.Fatal(e)
	}
	token, err := h.auth.IssueAgent(fmt.Sprint(h.uid), "billing", "signed-skill-task", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if status, r, err := h.raw("POST", "/api/v1/subscriptions/detect_subscriptions", "", map[string]any{}, token); err != nil || status != 403 || r.Code != "FORBIDDEN" {
		t.Fatal(status, r.Code, err)
	}
	if status, r, err := h.raw("POST", "/api/v1/billing/get_transactions", "", map[string]any{"from": "2026-01-01T00:00:00Z", "to": "2026-02-01T00:00:00Z"}, token); err != nil || status != 200 || r.Code != "OK" {
		t.Fatal(status, r.Code, err)
	}
	if status, r, err := h.raw("GET", "/api/v1/skills", "", nil, token); err != nil || status != 200 {
		t.Fatal(status, r.Code, err)
	} else {
		catalog := parse[[]skills.Definition](t, r)
		if len(catalog) != 1 || catalog[0].ID != "billing_analysis" {
			t.Fatal(catalog)
		}
	}
}
