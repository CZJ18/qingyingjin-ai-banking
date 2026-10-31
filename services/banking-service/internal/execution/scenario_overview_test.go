package execution

import (
	"context"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/scenariooverview"
	"github.com/qingyingjin/banking-service/internal/service"
	"testing"
)

func TestScenarioOverviewDeniesUnownedOrUnscopedActorsBeforeSQL(t *testing.T) {
	s := New(nil, nil)
	in := scenariooverview.Input{Scenario: "cards", AccountID: "1"}
	m := service.Meta{UserID: 42, TaskID: "owned-task"}
	for _, a := range []identity.Actor{{Kind: "agent", AgentType: "billing", UserID: "42", TaskID: m.TaskID, Scopes: []string{"execution:read"}}, {Kind: "agent", AgentType: "supervisor", UserID: "43", TaskID: m.TaskID, Scopes: []string{"execution:read"}}, {Kind: "agent", AgentType: "supervisor", UserID: "42", TaskID: "other-task", Scopes: []string{"execution:read"}}, {Kind: "agent", AgentType: "supervisor", UserID: "42", TaskID: m.TaskID}, {Kind: "tool", UserID: "42", Scopes: []string{"execution:read"}}} {
		if _, e := s.ScenarioOverview(context.Background(), a, m, in); entity.Code(e) != "FORBIDDEN" {
			t.Fatalf("actor %+v got %v", a, e)
		}
	}
	a := identity.Actor{Kind: "agent", AgentType: "supervisor", UserID: "42", TaskID: m.TaskID, Scopes: []string{"execution:read"}}
	if _, e := s.ScenarioOverview(context.Background(), a, m, in); entity.Code(e) != "DEPENDENCY_UNAVAILABLE" {
		t.Fatal(e)
	}
}
