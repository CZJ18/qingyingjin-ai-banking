package main

import (
	"errors"
	"github.com/qingyingjin/banking-service/internal/coze"
)

// Selects only the prompt adapter. Monetary, data and execution approvals
// remain mandatory and a changed request cannot reuse an old body digest.
func directSupervisorWorkflow(profile string) (string, error) {
	switch profile {
	case "", "billing":
		return "direct_provider", nil
	case "scenarios":
		return coze.DirectScenarioPlannerWorkflow, nil
	default:
		return "", errors.New("BANKING_DIRECT_PLANNER_PROFILE must be billing or explicitly scenarios")
	}
}
