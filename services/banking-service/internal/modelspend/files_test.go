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

func TestBillImportValidatesEveryRowBeforeReturningSettlements(t *testing.T) {
	digest := strings.Repeat("a", 64)
	base := billEnvelope{SchemaVersion: 1, ProjectID: "synthetic-project", Currency: "USD", BillID: "synthetic-bill", EvidenceDigest: digest}
	first := billLine{LineID: "1", AttemptID: "attempt-1", ProviderID: "provider-1", ActualCost: "0", ConfirmNoActiveRun: true}
	second := billLine{LineID: "2", AttemptID: "attempt-2", ProviderID: "provider-2", ActualCost: "0.00000001"}
	for _, tc := range []struct {
		name   string
		mutate func(*billEnvelope)
	}{
		{"lowercase currency", func(b *billEnvelope) { b.Currency = "usd" }},
		{"invalid currency", func(b *billEnvelope) { b.Currency = "U$D" }},
		{"oversized attempt", func(b *billEnvelope) { b.Rows[1].AttemptID = strings.Repeat("a", 37) }},
		{"invalid provider", func(b *billEnvelope) { b.Rows[1].ProviderID = "provider\n2" }},
		{"oversized provider", func(b *billEnvelope) { b.Rows[1].ProviderID = strings.Repeat("a", 129) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bill := base
			bill.Rows = []billLine{first, second}
			tc.mutate(&bill)
			raw, err := json.Marshal(bill)
			if err != nil {
				t.Fatal(err)
			}
			if rows, err := ParseBill(raw, digest); err == nil || len(rows) != 0 {
				t.Fatal("invalid reviewed bill returned importable rows", rows, err)
			}
		})
	}

	base.Rows = []billLine{first, second}
	raw, err := json.Marshal(base)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := ParseBill(raw, digest)
	if err != nil || len(rows) != 2 || rows[0].ActualCost != "0" || rows[1].ActualCost != "0.00000001" || !rows[0].ConfirmNoActiveRun || rows[1].ConfirmNoActiveRun {
		t.Fatal("valid independent settlements changed", rows, err)
	}
}

func TestBillImportRejectsRepeatedAttemptAndProviderAttribution(t *testing.T) {
	digest := strings.Repeat("a", 64)
	for _, duplicate := range []string{"attempt", "provider"} {
		t.Run(duplicate, func(t *testing.T) {
			bill := billEnvelope{SchemaVersion: 1, ProjectID: "synthetic-project", Currency: "USD", BillID: "synthetic-bill", EvidenceDigest: digest,
				Rows: []billLine{
					{LineID: "1", AttemptID: "attempt-1", ProviderID: "provider-1", ActualCost: "0.000001"},
					{LineID: "2", AttemptID: "attempt-2", ProviderID: "provider-2", ActualCost: "0.000002"},
				}}
			if duplicate == "attempt" {
				bill.Rows[1].AttemptID = bill.Rows[0].AttemptID
			} else {
				bill.Rows[1].ProviderID = bill.Rows[0].ProviderID
			}
			raw, err := json.Marshal(bill)
			if err != nil {
				t.Fatal(err)
			}
			if rows, err := ParseBill(raw, digest); err == nil || len(rows) != 0 {
				t.Fatal("bill returned conflicting supplier attribution for import", rows, err)
			}
		})
	}
}
