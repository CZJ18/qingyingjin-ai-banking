package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/qingyingjin/banking-service/internal/modelgateway"
	"github.com/qingyingjin/banking-service/internal/modelspend"
	"github.com/qingyingjin/banking-service/internal/outbound"
)

func TestPreflightWithoutNewApprovalStopsBeforeSecretConfiguration(t *testing.T) {
	old := os.Stdout
	r, w, e := os.Pipe()
	if e != nil {
		t.Fatal(e)
	}
	os.Stdout = w
	err := run([]string{"--preflight", "--run-id", "new-unapproved-run", "--env-file", "must-not-be-loaded.env"})
	w.Close()
	os.Stdout = old
	raw, _ := io.ReadAll(r)
	r.Close()
	if err == nil || !bytes.Contains(raw, []byte("FRESH_EXPLICIT_APPROVAL_REQUIRED")) || !bytes.Contains(raw, []byte("NOT_INSPECTED")) || bytes.Contains(raw, []byte("must-not-be-loaded")) {
		t.Fatal(err, string(raw))
	}
}

func TestRuntimePreflightUsesOnlyAuthenticatedReadAndRejectsDrift(t *testing.T) {
	canvas := []byte(`{"explicit":"protocol fixture canvas; never a published real model"}`)
	digest := fmt.Sprintf("%x", sha256.Sum256(canvas))
	model := "expected-model"
	gets := 0
	pins := map[string]string{"123": digest}
	coze := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gets++
		if r.Method != "GET" || r.URL.Path != "/v1/banking/model-scope" || r.Header.Get("Authorization") != "Bearer explicit-scope-fixture-credential" {
			t.Error("unexpected dispatch")
		}
		json.NewEncoder(w).Encode(map[string]any{"mode": "banking-task-scope-v1", "workflow_pins": pins, "gateway_origin": "http://127.0.0.1:18160", "model": model, "max_output_tokens": 8})
	}))
	defer coze.Close()
	directory := t.TempDir()
	env := filepath.Join(directory, "scope.env")
	file := filepath.Join(directory, "canvas.json")
	os.WriteFile(env, []byte("BANKING_COZE_BASE_URL="+coze.URL+"\nBANKING_COZE_PAT=explicit-scope-fixture-credential\n"), 0600)
	os.WriteFile(file, canvas, 0600)
	a := modelspend.Approval{WorkflowID: "123", WorkflowSHA256: digest, Model: model, MaxOutputTokens: 8}
	cfg := modelgateway.Config{WorkflowPolicies: map[string]string{"123": digest}}
	if e := reviewRuntime(context.Background(), a, cfg, env, file, "http://127.0.0.1:18160", outbound.TestPolicy()); e != nil {
		t.Fatal(e)
	}
	model = "changed-model"
	if reviewRuntime(context.Background(), a, cfg, env, file, "http://127.0.0.1:18160", outbound.TestPolicy()) == nil {
		t.Fatal("runtime drift accepted")
	}
	if gets != 2 {
		t.Fatal("unexpected request count", gets)
	}
	model = a.Model
	a.AdditionalWorkflows = map[string]string{"456": digest}
	cfg.WorkflowPolicies["456"] = digest
	if reviewRuntime(context.Background(), a, cfg, env, file, "http://127.0.0.1:18160", outbound.TestPolicy()) == nil || gets != 2 {
		t.Fatal("missing additional Canvas accepted or contacted runtime")
	}
	bundle, _ := json.Marshal(map[string]string{"123": string(canvas), "456": string(canvas)})
	os.WriteFile(file, bundle, 0600)
	if reviewRuntime(context.Background(), a, cfg, env, file, "http://127.0.0.1:18160", outbound.TestPolicy()) == nil {
		t.Fatal("unpublished additional workflow accepted")
	}
	pins["456"] = digest
	if e := reviewRuntime(context.Background(), a, cfg, env, file, "http://127.0.0.1:18160", outbound.TestPolicy()); e != nil {
		t.Fatal(e)
	}
}

func TestPermissionReviewBindsKeyApprovalAndEvidence(t *testing.T) {
	a := modelspend.Approval{ValidUntil: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), ProviderOrigin: "https://api.deepseek.com/v1", Model: "synthetic-model"}
	evidence := []byte("explicit synthetic test permission receipt; not real supplier evidence")
	key := "explicit-synthetic-supplier-key"
	r := permissionReview{ApprovalSHA256: a.Fingerprint(), CredentialSHA256: fmt.Sprintf("%x", sha256.Sum256([]byte(key))), ProviderOrigin: a.ProviderOrigin, Model: a.Model, Permission: "chat.completions", EvidenceSHA256: fmt.Sprintf("%x", sha256.Sum256(evidence)), ValidUntil: a.ValidUntil}
	raw, _ := json.Marshal(r)
	directory := t.TempDir()
	review := filepath.Join(directory, "review.json")
	receipt := filepath.Join(directory, "receipt")
	os.WriteFile(review, raw, 0600)
	os.WriteFile(receipt, evidence, 0600)
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	if e := reviewPermission(a, key, review, hash, receipt); e != nil {
		t.Fatal(e)
	}
	if reviewPermission(a, strings.Repeat("x", 32), review, hash, receipt) == nil {
		t.Fatal("credential changed without new review")
	}
	os.WriteFile(receipt, []byte("changed"), 0600)
	if reviewPermission(a, key, review, hash, receipt) == nil {
		t.Fatal("changed evidence accepted")
	}
}
