package main

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qingyingjin/banking-service/internal/modelspend"
)

// The approval file is intentionally sorted and indented like independently
// reviewed operator material. Its byte digest differs from the ledger identity.
func TestPermissionReviewConfirmedApprovalSource(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	a := modelspend.Approval{
		ID: "synthetic-permission-review", Simulation: true, RunID: "synthetic-permission-run",
		ProviderOrigin: "http://127.0.0.1:19090/v1", Model: "synthetic-permission-model",
		ProjectID: "synthetic-permission-project", Database: "synthetic_permission_test",
		Currency: "USD", Amount: "0.01", PolicySHA256: strings.Repeat("a", 64),
		Scope: "synthetic account 1 read-only billing", UserID: 11,
		TaskRequestSHA256: strings.Repeat("b", 64), WorkflowID: "123",
		WorkflowSHA256: strings.Repeat("c", 64),
		ValidFrom:      now.Add(-time.Minute).Format(time.RFC3339), ValidUntil: now.Add(time.Hour).Format(time.RFC3339),
		MaxRequests: 2, MaxConcurrent: 1, ContextTokens: 4096, MaxOutputTokens: 512,
	}
	dir := t.TempDir()
	write := func(name string, raw []byte) string {
		t.Helper()
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	digest := func(raw []byte) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }
	canonical, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &fields); err != nil {
		t.Fatal(err)
	}
	raw, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	approvalPath, approvalSHA := write("synthetic-approval.json", raw), digest(raw)
	if approvalSHA == a.Fingerprint() {
		t.Fatal("fixture must reproduce file-byte and canonical fingerprint difference")
	}
	if _, err := modelspend.ReadApproval(approvalPath, approvalSHA, a.RunID, now); err != nil {
		t.Fatal("independently bound synthetic approval must be valid", err)
	}
	key := "explicit-synthetic-supplier-key"
	evidence := []byte("synthetic permission statement, not supplier evidence")
	evidencePath := write("synthetic-evidence.txt", evidence)
	r := permissionReview{
		ApprovalSHA256: approvalSHA, CredentialSHA256: digest([]byte(key)),
		ProviderOrigin: a.ProviderOrigin, Model: a.Model, Permission: "chat.completions",
		EvidenceSHA256: digest(evidence), ValidUntil: a.ValidUntil,
	}
	reviewRaw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	reviewPath := write("synthetic-permission-review.json", reviewRaw)
	check := func(t *testing.T, reviewApprovalSHA string, source []string, wantOK bool) {
		t.Helper()
		r.ApprovalSHA256 = reviewApprovalSHA
		reviewRaw, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		write("synthetic-permission-review.json", reviewRaw)
		err = reviewPermission(a, key, reviewPath, digest(reviewRaw), evidencePath, source...)
		if (err == nil) != wantOK {
			t.Fatalf("permission binding accepted=%v, want %v: %v", err == nil, wantOK, err)
		}
	}
	t.Run("confirmed formatted file", func(t *testing.T) {
		check(t, approvalSHA, []string{approvalPath, approvalSHA}, true)
	})
	t.Run("legacy canonical fingerprint", func(t *testing.T) {
		check(t, a.Fingerprint(), nil, true)
	})
	t.Run("canonical fingerprint with confirmed file", func(t *testing.T) {
		check(t, a.Fingerprint(), []string{approvalPath, approvalSHA}, true)
	})
	t.Run("raw digest without verified source", func(t *testing.T) {
		check(t, approvalSHA, nil, false)
	})
	t.Run("missing source file", func(t *testing.T) {
		check(t, approvalSHA, []string{filepath.Join(dir, "missing.json"), approvalSHA}, false)
	})
	t.Run("wrong independently confirmed digest", func(t *testing.T) {
		check(t, approvalSHA, []string{approvalPath, strings.Repeat("d", 64)}, false)
	})
	t.Run("different approval with matching file digest", func(t *testing.T) {
		other := a
		other.UserID++
		otherRaw, err := json.MarshalIndent(other, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		otherPath := write("different-approval.json", otherRaw)
		check(t, digest(otherRaw), []string{otherPath, digest(otherRaw)}, false)
	})
	t.Run("strict approval JSON", func(t *testing.T) {
		invalid := append([]byte(`{"unrecognized_field":true,`), raw[1:]...)
		invalidPath := write("invalid-approval.json", invalid)
		check(t, digest(invalid), []string{invalidPath, digest(invalid)}, false)
	})
	t.Run("source path without independent digest", func(t *testing.T) {
		check(t, approvalSHA, []string{approvalPath}, false)
	})
	t.Run("arbitrary matching review digest cannot authorize", func(t *testing.T) {
		check(t, strings.Repeat("d", 64), []string{approvalPath, strings.Repeat("d", 64)}, false)
	})
	t.Run("changed original bytes invalidate confirmation", func(t *testing.T) {
		changedPath := write("reformatted-approval.json", canonical)
		check(t, approvalSHA, []string{changedPath, approvalSHA}, false)
	})
}
