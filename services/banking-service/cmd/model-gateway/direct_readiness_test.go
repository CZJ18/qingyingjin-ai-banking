package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"github.com/qingyingjin/banking-service/internal/modelspend"
	"os"
	"strings"
	"testing"
)

func TestDirectManifestPinsExactRequestWithoutCozeAuthority(t *testing.T) {
	m, args, file := readinessFixture(t)
	paths := map[string]string{}
	for n := 0; n < len(args); n++ {
		if strings.HasPrefix(args[n], "--") && n+1 < len(args) && !strings.HasPrefix(args[n+1], "--") {
			paths[args[n]] = args[n+1]
		}
	}
	raw, _ := os.ReadFile(paths["--approval-file"])
	var a modelspend.Approval
	json.Unmarshal(raw, &a)
	a.IntegrationMode = "direct_provider"
	a.DirectRequestSHA256 = strings.Repeat("d", 64)
	a.WorkflowID = ""
	a.WorkflowSHA256 = ""
	a.AdditionalWorkflows = nil
	write := func(path string, value any) string {
		b, _ := json.Marshal(value)
		if e := os.WriteFile(path, b, 0600); e != nil {
			t.Fatal(e)
		}
		return fmt.Sprintf("%x", sha256.Sum256(b))
	}
	approvalSHA := write(paths["--approval-file"], a)
	raw, _ = os.ReadFile(paths["--execution-plan-file"])
	var plan preflightReport
	json.Unmarshal(raw, &plan)
	plan.ExecutionPlan = &a
	plan.Approval.ApprovalSHA256 = a.Fingerprint()
	planSHA := write(paths["--execution-plan-file"], plan)
	for n, v := range args {
		switch v {
		case "--approval-sha256":
			args[n+1] = approvalSHA
		case "--execution-plan-sha256", "--confirm-execution-plan-sha256":
			args[n+1] = planSHA
		}
	}
	m.IntegrationMode = "direct_provider"
	m.DirectRequestSHA256 = a.DirectRequestSHA256
	m.CozeOrigin = ""
	m.ProtectedScopeMode = ""
	m.AllowedOrigins = []string{"https://api.deepseek.com", m.GatewayOrigin}
	for role := range m.Workflows {
		m.Workflows[role] = readinessWorkflow{}
	}
	write(file, m)
	r, e := captureReadiness(t, append(args, "--readiness-manifest-file", file))
	if e == nil || r["status"] != "PARTIAL" {
		t.Fatal(r, e)
	}
	m.DirectRequestSHA256 = strings.Repeat("e", 64)
	write(file, m)
	r, e = captureReadiness(t, append(args, "--readiness-manifest-file", file))
	if e == nil || r["status"] != "FAIL" {
		t.Fatal("direct request drift accepted", r, e)
	}
}
