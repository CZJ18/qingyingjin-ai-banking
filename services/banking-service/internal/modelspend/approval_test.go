package modelspend

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestNewApprovalRejectsOldRunScopeAndChangedFile(t *testing.T) {
	p := syntheticPolicy()
	hash, _ := p.Hash()
	a := Approval{ID: "synthetic-only", RunID: "new-run", ProviderOrigin: "https://api.deepseek.com/v1", Model: p.Model, ProjectID: p.ProjectID, Database: "isolated_test", Currency: p.Currency, Amount: "1", PolicySHA256: hash, Scope: "SQL-only synthetic contract test", UserID: 42, TaskRequestSHA256: strings.Repeat("b", 64), WorkflowID: "123", WorkflowSHA256: strings.Repeat("c", 64), ValidFrom: p.ValidFrom, ValidUntil: p.ValidUntil, MaxRequests: 1, MaxConcurrent: 1, ContextTokens: p.ContextTokens, MaxOutputTokens: p.MaxOutputTokens}
	raw, _ := json.Marshal(a)
	file := filepath.Join(t.TempDir(), "approval.json")
	os.WriteFile(file, raw, 0600)
	digest := fmt.Sprintf("%x", sha256.Sum256(raw))
	if _, e := ReadApproval(file, digest, "new-run", time.Now()); e != nil {
		t.Fatal(e)
	}
	if _, e := ReadApproval(file, digest, "old-run", time.Now()); e == nil {
		t.Fatal("historical run accepted")
	}
	if a.Validate(p, "new-run", "other_database_test", time.Now()) == nil {
		t.Fatal("database scope changed")
	}
	changed := a
	changed.AdditionalWorkflows = map[string]string{"456": strings.Repeat("d", 64)}
	if changed.Check("new-run", time.Now()) != nil || !changed.AllowsWorkflow("456", strings.Repeat("d", 64)) || changed.AllowsWorkflow("789", strings.Repeat("d", 64)) || changed.AllowsWorkflow("456", a.WorkflowSHA256) {
		t.Fatal("explicit additional workflow scope not enforced")
	}
	changed.AdditionalWorkflows[a.WorkflowID] = a.WorkflowSHA256
	if changed.Check("new-run", time.Now()) == nil {
		t.Fatal("duplicate primary workflow accepted")
	}
	changed = a
	changed.MaxRequests = 0
	if changed.Validate(p, "new-run", a.Database, time.Now()) == nil {
		t.Fatal("unbounded requests")
	}
	os.WriteFile(file, append(raw, ' '), 0600)
	if _, e := ReadApproval(file, digest, "new-run", time.Now()); e == nil {
		t.Fatal("changed approval accepted")
	}
}
