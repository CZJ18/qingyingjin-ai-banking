package lifestyle

import (
	"context"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/coze"
	"github.com/qingyingjin/banking-service/internal/entity"
	"testing"
)

func specimen() (Spec, string) {
	return Spec{SchemaVersion: "2.0", Currency: "CNY", PeriodMonths: 3, ExpenseReductionMonthlyMinor: 100000, SavingsMonthlyMinor: 20000, HealthBackground: "减掉10斤", AdjustableCategories: []string{"shopping"}, ProtectedCategories: []string{}, ProhibitedOperations: []string{}, Evidence: map[string]string{"period_months": "三个月", "expense_reduction_monthly_minor": "每月少花1000元", "savings_monthly_minor": "每月储蓄200元", "adjustable:shopping": "可以调整shopping"}}, "三个月减掉10斤，每月少花1000元，每月储蓄200元，可以调整shopping"
}
func TestV2SourceRoles(t *testing.T) {
	s, text := specimen()
	raw, _ := json.Marshal(s)
	parsed, e := DecodeSpec(raw, text)
	if e != nil || parsed.SavingsMonthlyMinor != 20000 || parsed.ExpenseReductionMonthlyMinor != 100000 {
		t.Fatalf("%+v %v", parsed, e)
	}
	for _, mutation := range []func(*Spec){func(s *Spec) { s.SavingsMonthlyMinor = 100000 }, func(s *Spec) { s.ExpenseReductionMonthlyMinor = 20000 }, func(s *Spec) { s.PeriodMonths = 6 }, func(s *Spec) { s.Evidence["adjustable:shopping"] = "三个月" }} {
		s, text := specimen()
		mutation(&s)
		raw, _ := json.Marshal(s)
		if _, e := DecodeSpec(raw, text); e == nil {
			t.Fatalf("unsupported source accepted %s", raw)
		}
	}
}
func TestV2UnsafeWeightAndFood(t *testing.T) {
	for _, tail := range []string{"减掉10斤奖励100元", "不吃饭省钱", "限制饮食"} {
		s, text := specimen()
		raw, _ := json.Marshal(s)
		if _, e := DecodeSpec(raw, text+tail); e == nil {
			t.Fatal(tail)
		}
	}
}
func TestV2AdjustableRequiresAffirmativeEvidence(t *testing.T) {
	for _, tc := range []struct {
		phrase string
		allow  bool
	}{
		{"可以调整shopping", true},
		{"可调整shopping", true},
		{"不可以调整shopping", false},
		{"不可调整shopping", false},
		{"不能允许调整shopping", false},
		{"不愿意减少shopping", false},
		{"不想减少shopping", false},
	} {
		t.Run(tc.phrase, func(t *testing.T) {
			s, text := specimen()
			text = text[:len(text)-len("可以调整shopping")] + tc.phrase
			s.Evidence["adjustable:shopping"] = tc.phrase
			raw, _ := json.Marshal(s)
			_, err := DecodeSpec(raw, text)
			if (err == nil) != tc.allow {
				t.Fatalf("allow=%v, error=%v", tc.allow, err)
			}
		})
	}
}
func TestV2RejectUnknownAndFloat(t *testing.T) {
	s, text := specimen()
	raw, _ := json.Marshal(s)
	var v map[string]any
	json.Unmarshal(raw, &v)
	v["savings_monthly_minor"] = 2.5
	raw, _ = json.Marshal(v)
	if _, e := DecodeSpec(raw, text); e == nil {
		t.Fatal("float accepted")
	}
	delete(v, "savings_monthly_minor")
	v["confirmed"] = true
	raw, _ = json.Marshal(v)
	if _, e := DecodeSpec(raw, text); e == nil {
		t.Fatal("unknown accepted")
	}
}

type fakeRunner struct{ data string }

func (f fakeRunner) RunSyncWorkflow(context.Context, coze.RunRequest) (coze.RunResponse, error) {
	zero := int64(0)
	return coze.RunResponse{Code: &zero, ExecuteID: "test-fixture", Data: f.data}, nil
}
func TestV2ParserMissingAndInvalid(t *testing.T) {
	s := New(nil)
	_, e := s.Parse(context.Background(), ParseInput{Text: "生活目标"}, "test")
	if entity.Code(e) != "COZE_UNAVAILABLE" {
		t.Fatal(e)
	}
	s.Parser = &Parser{Runner: fakeRunner{`{"output":"{}"}`}, WorkflowID: "1"}
	if _, e = s.Parse(context.Background(), ParseInput{Text: "生活目标"}, "test"); entity.Code(e) != "MODEL_OUTPUT_INVALID" {
		t.Fatal(e)
	}
}
func TestV2ParserFixture(t *testing.T) {
	spec, text := specimen()
	raw, _ := json.Marshal(spec)
	end, _ := json.Marshal(map[string]string{"output": string(raw)})
	s := New(nil)
	s.Parser = &Parser{Runner: fakeRunner{string(end)}, WorkflowID: "1"}
	r, e := s.Parse(context.Background(), ParseInput{Text: text}, "test")
	if e != nil || r.Spec.ExpenseReductionMonthlyMinor != 100000 {
		t.Fatalf("%+v %v", r, e)
	}
}
