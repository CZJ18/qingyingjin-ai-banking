package main

import (
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/qingyingjin/banking-service/internal/coze"
	"github.com/qingyingjin/banking-service/internal/skills"
)

func TestExecutionPlanAllowsZeroToolsOnlyForExactFixedSmoke(t *testing.T) {
	a, _, _ := newRealApprovalForStartup(t)
	a.IntegrationMode = "direct_provider"
	a.WorkflowID = ""
	a.WorkflowSHA256 = ""
	raw, _ := coze.DirectSmokeRequest(a.Model, a.MaxOutputTokens)
	a.DirectRequestSHA256 = fmt.Sprintf("%x", sha256.Sum256(raw))
	i := &executionInstructions{TaskRequestSHA256: a.TaskRequestSHA256, TestInput: coze.DirectSmokeInput, DeterministicTools: []skills.Request{}, ExpectedDataScope: "no financial data; fixed text only", Verification: []string{"verify response and supplier usage"}, FailureHandling: []string{"preserve unknown; no replay"}}
	if i.check(a) != nil {
		t.Fatal("fixed smoke incorrectly requires a financial tool")
	}
	i.TestInput = "custom input"
	if i.check(a) == nil {
		t.Fatal("unbound smoke input")
	}
	i.TestInput = coze.DirectSmokeInput
	a.DirectRequestSHA256 = ""
	if i.check(a) == nil {
		t.Fatal("unbound smoke request")
	}
	a.IntegrationMode = "coze"
	if i.check(a) == nil {
		t.Fatal("Coze accepted no-tool plan")
	}
}
