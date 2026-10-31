package statement

import (
	"testing"
	"time"
)

func TestCSVExactAmountsTimezoneStatusAndProvenance(t *testing.T) {
	input := Input{CSV: "编号,日期,金额,方向,币种,商户,分类,状态\na,2026-10-01 08:30:00,0.29,expense,CNY, Stream ,订阅,posted\nb,2026-10-01 09:00:00,12,income,CNY,Salary,other,pending\na,2026-10-01 08:30:00,0.29,expense,CNY, Stream ,订阅,posted\n", Timezone: "Asia/Shanghai", Columns: map[string]string{"external_id": "编号", "occurred_at": "日期", "amount": "金额", "direction": "方向", "currency": "币种", "merchant": "商户", "category": "分类", "status": "状态"}}
	got, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Records) != 1 || got.Skipped != 2 || got.Records[0].AmountMinor != 29 || got.Records[0].MerchantName != "Stream" {
		t.Fatalf("%+v", got)
	}
	instant, _ := time.Parse(time.RFC3339, "2026-10-01T00:30:00Z")
	if got.Records[0].OccurredAt != instant.UnixMilli() || got.Records[0].Source.RowNumber != 2 || len(got.FileSHA256) != 64 || got.Records[0].Source.Kind != "unverified_csv_upload" {
		t.Fatal(got)
	}
	if got.FinancialExecutionEnabled {
		t.Fatal("preview granted authority")
	}
}

func TestCSVRejectsAmbiguousOrUnsafeFinancialInput(t *testing.T) {
	base := func(s string) Input {
		return Input{CSV: s, Timezone: "Asia/Shanghai", Columns: map[string]string{"external_id": "id", "occurred_at": "at", "amount": "amount", "direction": "direction", "currency": "currency", "status": "status"}}
	}
	for _, row := range []string{"a,2026-10-01 00:00:00,0.001,expense,CNY,posted", "a,2026-10-01 00:00:00,1e3,expense,CNY,posted", "a,2026-10-01 00:00:00,1,expense,USD,posted", "a,2026-10-01 00:00:00,1,expense,CNY,unknown", "a,2026-02-30 00:00:00,1,expense,CNY,posted", "a,2026-10-01 00:00:00,-1,expense,CNY,posted"} {
		if _, err := Parse(base("id,at,amount,direction,currency,status\n" + row + "\n")); err == nil {
			t.Fatal("unsafe row accepted", row)
		}
	}
	in := base("id,at,amount,direction,currency,status\na,2026-10-01 00:00:00,1,expense,CNY,posted\na,2026-10-01 00:00:00,2,expense,CNY,posted\n")
	if _, err := Parse(in); err == nil {
		t.Fatal("conflicting duplicate accepted")
	}
	in = base("id,id\na,a\n")
	if _, err := Parse(in); err == nil {
		t.Fatal("duplicate header accepted")
	}
}

func TestRetainedSourceRecomputesNormalizedFinancialFields(t *testing.T) {
	input := Input{CSV: "id,at,amount,direction,currency,status\na,2026-10-01 00:00:00,0.29,expense,CNY,posted\n", Timezone: "Asia/Shanghai", Columns: map[string]string{"external_id": "id", "occurred_at": "at", "amount": "amount", "direction": "direction", "currency": "currency", "status": "status"}}
	preview, err := Parse(input)
	if err != nil {
		t.Fatal(err)
	}
	record := preview.Records[0]
	if err := ValidateRecord(record); err != nil {
		t.Fatal(err)
	}
	record.AmountMinor++
	if err := ValidateRecord(record); err == nil {
		t.Fatal("source/normalized amount mismatch accepted")
	}
	record = preview.Records[0]
	record.OccurredAt++
	if err := ValidateRecord(record); err == nil {
		t.Fatal("source/normalized timestamp mismatch accepted")
	}
	record = preview.Records[0]
	record.Direction = "income"
	if err := ValidateRecord(record); err == nil {
		t.Fatal("source/normalized direction mismatch accepted")
	}
}
