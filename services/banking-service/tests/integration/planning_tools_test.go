//go:build integration

package integration

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/httpapi"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/planning"
	"github.com/qingyingjin/banking-service/internal/service"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestAdaptiveSavingsToolIdentityMySQL(t *testing.T) {
	h := harness(t)
	ctx := context.Background()
	a := h.account(t, 100000)
	m := service.Meta{UserID: h.uid, TraceID: "tool-fixture"}
	g, e := h.svc.CreateGoal(ctx, m, uuid.NewString(), service.GoalInput{Name: "tool fixture", TargetMinor: 100000, MonthlyMinor: 20000, StartsAt: time.Now().Add(-time.Hour).UnixMilli(), EndsAt: time.Now().AddDate(0, 3, 0).UnixMilli()})
	if e != nil {
		t.Fatal(e)
	}
	p := planning.New(h.conn.DB)
	v, e := p.Create(ctx, m, planning.CreateInput{Name: "tools", Settings: planning.Settings{AccountIDs: []int64{a.ID}, SavingsGoalIDs: []int64{g.ID}, LivingRemainingMinor: cents(30000), EssentialRemainingMinor: cents(20000)}})
	if e != nil {
		t.Fatal(e)
	}
	// Fixture binding is explicit server configuration, never an Agent user_id.
	auth := identity.New(h.cfg.JWTSecret, h.cfg.JWTIssuer, h.cfg.JWTAudience, h.cfg.ToolToken, fmt.Sprint(h.uid), "")
	server := httptest.NewServer(httpapi.NewWithPlanning(auth, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, time.Second, h.svc, nil, nil, nil, p))
	defer server.Close()
	for _, name := range []string{"get_financial_plan", "compare_financial_plans", "replan_financial_plan"} {
		body, _ := json.Marshal(planning.IDInput{PlanID: fmt.Sprint(v.Plan.ID)})
		req, _ := http.NewRequest("POST", server.URL+"/tools/v1/savings/"+name, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Banking-Tool-Key", h.cfg.ToolToken)
		req.Header.Set("X-Task-ID", "supervisor-test")
		response, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		raw, e := io.ReadAll(response.Body)
		response.Body.Close()
		if e != nil || response.StatusCode != 200 {
			t.Fatal(response.StatusCode, string(raw), e)
		}
		var out struct {
			Data planning.View `json:"data"`
		}
		if e = json.Unmarshal(raw, &out); e != nil || out.Data.Plan.UserID != h.uid || len(out.Data.Result.Options) != 3 || out.Data.FinancialExecutionEnabled {
			t.Fatal(e, string(raw))
		}
	}
	for _, path := range []string{fmt.Sprintf("/tools/v1/financial-plans/%d/decisions", v.Plan.ID), fmt.Sprintf("/tools/v1/financial-plans/%d/settings", v.Plan.ID)} {
		req, _ := http.NewRequest("POST", server.URL+path, nil)
		req.Header.Set("X-Banking-Tool-Key", h.cfg.ToolToken)
		res, e := http.DefaultClient.Do(req)
		if e != nil {
			t.Fatal(e)
		}
		res.Body.Close()
		if res.StatusCode != 404 {
			t.Fatal("sensitive tool route exposed")
		}
	}
	h.balance(t, a.ID, 100000)
	h.balance(t, g.AccountID, 0)
}
