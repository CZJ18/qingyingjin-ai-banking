package statement

import "testing"

func TestLocalizedStatementValues(t *testing.T) {
	in := Input{CSV: "编号,日期,金额,方向,币种,状态\nA,2026-09-01 09:00:00,10.25,支出,人民币,已入账\nB,2026-09-02T09:00:00+08:00,100.00,收入,CNY,已入账\nC,2026-09-03 09:00:00,9.00,支出,人民币,待入账\nD,2026-09-04 09:00:00,8.00,支出,人民币,已冲正\n", Timezone: "Asia/Shanghai", Columns: map[string]string{"external_id": "编号", "occurred_at": "日期", "amount": "金额", "direction": "方向", "currency": "币种", "status": "状态"}}
	preview, err := Parse(in)
	if err != nil || len(preview.Records) != 2 || preview.Skipped != 2 {
		t.Fatalf("explicit localized values rejected: %+v %v", preview, err)
	}
	if preview.Records[0].AmountMinor != 1025 || preview.Records[0].Direction != "expense" || preview.Records[1].Direction != "income" || preview.Records[0].Currency != "CNY" {
		t.Fatal("localized values changed financial meaning")
	}
	for _, r := range preview.Records {
		if r.EffectMode != "record_only" || ValidateRecord(r) != nil {
			t.Fatal("normalization broke retained source validation")
		}
	}
	for _, change := range []string{"unknown_status", "成功", "退款", "USD"} {
		bad := in
		bad.CSV = "编号,日期,金额,方向,币种,状态\nE,2026-09-01 09:00:00,10.25,支出,人民币," + change + "\n"
		if change == "USD" {
			bad.CSV = "编号,日期,金额,方向,币种,状态\nE,2026-09-01 09:00:00,10.25,支出,USD,已入账\n"
		}
		if _, e := Parse(bad); e == nil {
			t.Fatalf("ambiguous or unsupported value accepted: %s", change)
		}
	}
}
