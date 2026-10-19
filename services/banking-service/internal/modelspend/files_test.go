package modelspend

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPolicyFileRejectsUnknownDuplicateTrailingAndMissingFields(t *testing.T) {
	raw, _ := json.Marshal(syntheticPolicy())
	p, e := ParsePolicy(raw)
	if e != nil || p.ProjectID != "synthetic-project" {
		t.Fatal(p, e)
	}
	for _, v := range []string{`{}`, string(raw) + `{}`, `[]`, strings.Replace(string(raw), `"project_id":`, `"unknown":1,"project_id":`, 1), strings.Replace(string(raw), `"project_id":`, `"project_id":"forged","project_id":`, 1)} {
		if _, e := ParsePolicy([]byte(v)); e == nil {
			t.Fatal("policy accepted ambiguous input")
		}
	}
	if _, e := ParsePolicy([]byte(strings.Replace(string(raw), `"project_id"`, `"Project_ID"`, 1))); e == nil {
		t.Fatal("case alias accepted as exact policy field")
	}
}
func TestBillImportRequiresEvidenceAndRejectsAmbiguousRows(t *testing.T) {
	digest := strings.Repeat("a", 64)
	raw := `{"schema_version":1,"project_id":"synthetic-project","currency":"USD","bill_id":"synthetic-bill","evidence_sha256":"` + digest + `","rows":[{"line_id":"1","attempt_id":"id","provider_id":"synthetic-response","actual_cost":"0.000001","confirm_no_active_run":true}]}`
	rows, e := ParseBill([]byte(raw), digest)
	if e != nil || len(rows) != 1 || rows[0].EvidenceDigest != digest || rows[0].BillID != "synthetic-bill" {
		t.Fatal(rows, e)
	}
	for _, v := range []string{strings.Replace(raw, `"schema_version":1`, `"schema_version":2`, 1), strings.Replace(raw, `"actual_cost":"0.000001"`, `"actual_cost":0.000001`, 1), strings.Replace(raw, `"line_id":"1"`, `"line_id":"1","line_id":"2"`, 1), strings.Replace(raw, `"rows":[`, `"extra":true,"rows":[`, 1), raw + `[]`, strings.Replace(raw, `"rows":[`, `"rows":[],"rows":[`, 1)} {
		if _, e := ParseBill([]byte(v), digest); e == nil {
			t.Fatal("bill accepted ambiguous input")
		}
	}
	if _, e := ParseBill([]byte(raw), strings.Repeat("b", 64)); e == nil {
		t.Fatal("evidence mismatch")
	}
	if _, e := ParseBill([]byte(strings.Replace(raw, `"line_id"`, `"Line_ID"`, 1)), digest); e == nil {
		t.Fatal("case alias accepted in nested bill row")
	}
}
