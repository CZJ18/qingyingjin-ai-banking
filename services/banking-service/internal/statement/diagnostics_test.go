package statement

import (
	"encoding/csv"
	"strings"
	"testing"

	"github.com/qingyingjin/banking-service/internal/entity"
)

func diagnosticInput(row []string) Input {
	var content strings.Builder
	w := csv.NewWriter(&content)
	_ = w.Write([]string{"id", "at", "amount", "direction", "currency", "status", "merchant", "kind"})
	_ = w.Write([]string{"first", "2026-09-01 08:00:00", "1.00", "expense", "CNY", "posted", "fixture", "external"})
	_ = w.Write(row)
	w.Flush()
	return Input{CSV: content.String(), Timezone: "Asia/Shanghai", Columns: map[string]string{
		"external_id": "id", "occurred_at": "at", "amount": "amount", "direction": "direction", "currency": "currency", "status": "status", "merchant": "merchant", "kind": "kind",
	}}
}

func TestCSVActionableDiagnosticsDoNotEchoFinancialData(t *testing.T) {
	for _, tc := range []struct {
		name         string
		index        int
		value, field string
	}{
		{"amount", 2, "PRIVATE_AMOUNT_9", "金额"},
		{"posting", 5, "PRIVATE_STATUS_9", "入账状态"},
		{"timestamp", 1, "PRIVATE_TIME_9", "交易时间"},
		{"direction", 3, "PRIVATE_DIRECTION_9", "收支方向"},
		{"currency", 4, "PRIVATE_CURRENCY_9", "币种"},
		{"internal_transfer", 7, "internal_transfer", "交易类型"},
		{"external_id", 0, strings.Repeat("PRIVATE_ID_", 14), "交易编号"},
		{"merchant", 6, strings.Repeat("PRIVATE_MERCHANT_", 9), "商户名称"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			row := []string{"PRIVATE_ID_9", "2026-09-02 08:00:00", "2.00", "expense", "CNY", "posted", "PRIVATE_MERCHANT_9", "external"}
			row[tc.index] = tc.value
			_, err := Parse(diagnosticInput(row))
			if entity.Code(err) != "INVALID_ARGUMENT" {
				t.Fatalf("expected existing error code, got %v", err)
			}
			if !strings.Contains(err.Error(), "第 3 条记录") || !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("missing actionable row/field: %v", err)
			}
			for _, private := range []string{"PRIVATE_ID_9", "PRIVATE_MERCHANT_9", tc.value} {
				if strings.Contains(err.Error(), private) {
					t.Fatalf("error disclosed statement value: %v", err)
				}
			}
		})
	}
}

func TestCSVStructuralDiagnosticsRetainStrictLimits(t *testing.T) {
	base := diagnosticInput([]string{"second", "2026-09-02 08:00:00", "2.00", "expense", "CNY", "posted", "fixture", "external"})
	t.Run("missing_required_mapping", func(t *testing.T) {
		in := base
		in.Columns = map[string]string{}
		for k, v := range base.Columns {
			in.Columns[k] = v
		}
		delete(in.Columns, "currency")
		_, err := Parse(in)
		if err == nil || !strings.Contains(err.Error(), "字段映射") || !strings.Contains(err.Error(), "币种") {
			t.Fatalf("missing mapping guidance: %v", err)
		}
	})
	t.Run("extra_columns", func(t *testing.T) {
		in := base
		in.CSV = strings.TrimSuffix(in.CSV, "\n") + ",PRIVATE_EXTRA_9\n"
		_, err := Parse(in)
		if err == nil || !strings.Contains(err.Error(), "第 3 条记录") || !strings.Contains(err.Error(), "列数") || strings.Contains(err.Error(), "PRIVATE_EXTRA_9") {
			t.Fatalf("bad structural diagnostic: %v", err)
		}
	})
	t.Run("invalid_utf8", func(t *testing.T) {
		in := base
		in.CSV += "\xff"
		_, err := Parse(in)
		if err == nil || !strings.Contains(err.Error(), "UTF-8") {
			t.Fatalf("missing encoding guidance: %v", err)
		}
	})
	t.Run("conflicting_row", func(t *testing.T) {
		in := diagnosticInput([]string{"first", "2026-09-01 08:00:00", "2.00", "expense", "CNY", "posted", "fixture", "external"})
		_, err := Parse(in)
		if entity.Code(err) != "IDEMPOTENCY_CONFLICT" || !strings.Contains(err.Error(), "第 3 条记录") || !strings.Contains(err.Error(), "交易编号") {
			t.Fatalf("conflict lost: %v", err)
		}
	})
	t.Run("multiline_merchant_record_number", func(t *testing.T) {
		in := diagnosticInput([]string{"second", "2026-09-02 08:00:00", "bad", "expense", "CNY", "posted", "PRIVATE\nMERCHANT", "external"})
		_, err := Parse(in)
		if err == nil || !strings.Contains(err.Error(), "第 3 条记录（表头算第 1 条）") || strings.Contains(err.Error(), "PRIVATE") {
			t.Fatalf("ambiguous record number: %v", err)
		}
	})
}
