package lifestyle

import (
	"encoding/json"
	"testing"
)

func TestModelSourceUnitConversionNotModelArithmetic(t *testing.T) {
	spec, text := specimen()
	spec.ExpenseReductionMonthlyMinor = 1000
	spec.SavingsMonthlyMinor = 200
	raw, _ := json.Marshal(spec)
	parsed, e := decodeModelSpec(raw, text)
	if e != nil || parsed.ExpenseReductionMonthlyMinor != 100000 || parsed.SavingsMonthlyMinor != 20000 {
		t.Fatal(parsed, e)
	}
	if _, e = DecodeSpec(raw, text); e == nil {
		t.Fatal("manual spec bypassed source validation")
	}
	for _, ev := range []string{"每月少花-1000元", "每月少花1,000元", "每月少花1e3元", "每月少花1.234元", "每月少花100元或200元", "每月少花1000000000000元"} {
		spec, text := specimen()
		spec.Evidence["expense_reduction_monthly_minor"] = ev
		text += ev
		raw, _ := json.Marshal(spec)
		if _, e := decodeModelSpec(raw, text); e == nil {
			t.Fatal(ev)
		}
	}
	spec, text = specimen()
	spec.SavingsMonthlyMinor = 1
	spec.Evidence["savings_monthly_minor"] = "每月储蓄2.50元"
	text += "每月储蓄2.50元"
	raw, _ = json.Marshal(spec)
	parsed, e = decodeModelSpec(raw, text)
	if e != nil || parsed.SavingsMonthlyMinor != 250 {
		t.Fatal(parsed, e)
	}
}
