package main

import (
	"github.com/qingyingjin/banking-service/internal/coze"
	"testing"
)

func TestDirectPlannerProfileKeepsLegacyDefault(t *testing.T) {
	for _, value := range []string{"", "billing"} {
		id, err := directSupervisorWorkflow(value)
		if err != nil || id != "direct_provider" {
			t.Fatal(value, id, err)
		}
	}
	id, err := directSupervisorWorkflow("scenarios")
	if err != nil || id != coze.DirectScenarioPlannerWorkflow {
		t.Fatal(id, err)
	}
	for _, value := range []string{"true", "*", "Scenarios", " scenarios ", "123"} {
		if _, err := directSupervisorWorkflow(value); err == nil {
			t.Fatal("invalid profile accepted", value)
		}
	}
}
