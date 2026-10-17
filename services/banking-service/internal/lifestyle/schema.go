package lifestyle

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/goal"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"io"
	"strings"
	"sync"
)

//go:embed goal-v2.schema.json
var GoalSchema []byte
var once sync.Once
var compiled *jsonschema.Schema
var schemaErr error

func decodeSpecShape(raw []byte) (Spec, error) {
	var out Spec
	invalid := func() (Spec, error) {
		return out, entity.Fail("MODEL_OUTPUT_INVALID", "v2 goal must match schema and explicit source evidence")
	}
	once.Do(func() {
		var doc any
		if schemaErr = json.Unmarshal(GoalSchema, &doc); schemaErr != nil {
			return
		}
		c := jsonschema.NewCompiler()
		if schemaErr = c.AddResource("urn:qingrongjin:goal:v2", doc); schemaErr == nil {
			compiled, schemaErr = c.Compile("urn:qingrongjin:goal:v2")
		}
	})
	if schemaErr != nil {
		return out, entity.Fail("DEPENDENCY_UNAVAILABLE", "v2 goal schema unavailable")
	}
	var doc any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var extra any
	if d.Decode(&doc) != nil || d.Decode(&extra) != io.EOF || compiled.Validate(doc) != nil || json.Unmarshal(raw, &out) != nil || ValidateSpec(out) != nil {
		return invalid()
	}
	return out, nil
}

func DecodeSpec(raw []byte, text string) (Spec, error) {
	out, e := decodeSpecShape(raw)
	if e != nil {
		return out, e
	}
	invalid := func() (Spec, error) {
		return out, entity.Fail("MODEL_OUTPUT_INVALID", "v2 goal must match schema and explicit source evidence")
	}
	if strings.TrimSpace(text) == "" || len(text) > 10000 {
		return invalid()
	}
	// Reuse v1's proven period and savings-source validation, without ever
	// converting a spending reduction into a savings target.
	period := out.PeriodMonths
	p := goal.Parsed{GoalType: "healthy_lifestyle", PeriodMonths: &period, Currency: "CNY", Constraints: []goal.Constraint{}, MissingInformation: []string{}, ProhibitedOperations: []string{}, Evidence: map[string]string{"period_months": out.Evidence["period_months"]}}
	if out.SavingsMonthlyMinor > 0 {
		ev := out.Evidence["savings_monthly_minor"]
		if strings.Contains(ev, "少花") || strings.Contains(ev, "减少支出") || strings.Contains(ev, "节省开支") {
			return invalid()
		}
		p.SavingsMonthlyMinor = &out.SavingsMonthlyMinor
		p.Evidence["savings_monthly_minor"] = out.Evidence["savings_monthly_minor"]
	}
	common, _ := json.Marshal(p)
	if _, e := goal.DecodeParsed(common, text); e != nil {
		return invalid()
	}
	if out.ExpenseReductionMonthlyMinor > 0 {
		ev := out.Evidence["expense_reduction_monthly_minor"]
		if ev == "" || !strings.Contains(text, ev) || !strings.Contains(ev, "月") || !(strings.Contains(ev, "少花") || strings.Contains(ev, "减少支出") || strings.Contains(ev, "节省开支")) || !goal.MatchesAmountEvidence(ev, out.ExpenseReductionMonthlyMinor) {
			return invalid()
		}
	}
	if out.HealthBackground != "" && !strings.Contains(text, out.HealthBackground) {
		return invalid()
	}
	for _, list := range []struct {
		prefix string
		values []string
	}{{"adjustable:", out.AdjustableCategories}, {"protected:", out.ProtectedCategories}, {"prohibited:", out.ProhibitedOperations}} {
		for _, v := range list.values {
			ev := out.Evidence[list.prefix+v]
			if ev == "" || !strings.Contains(text, ev) {
				return invalid()
			}
			if list.prefix != "prohibited:" && !strings.Contains(ev, v) {
				return invalid()
			}
			if list.prefix == "adjustable:" {
				positive := strings.Contains(ev, "可以调整") || strings.Contains(ev, "可调整") || strings.Contains(ev, "允许调整") || strings.Contains(ev, "可以减少") || strings.Contains(ev, "愿意减少")
				negative := false
				for _, phrase := range []string{"禁止", "不允许", "不要", "不得", "不可以", "不可", "不能", "不愿", "不想"} {
					negative = negative || strings.Contains(ev, phrase)
				}
				if !positive || negative {
					return invalid()
				}
			}
		}
	}
	if (strings.Contains(text, "体重") || strings.Contains(text, "减重") || strings.Contains(text, "减肥") || strings.Contains(text, "减掉")) && (strings.Contains(text, "奖励") || strings.Contains(text, "惩罚") || strings.Contains(text, "扣钱") || strings.Contains(text, "奖金")) {
		return out, entity.Fail("INVALID_ARGUMENT", "weight-linked financial incentives prohibited")
	}
	// A weight target is background only. Weight rewards, food deprivation and
	// restrictions cannot be compiled into financial suggestions.
	for _, phrase := range []string{"体重奖励", "减重奖励", "体重惩罚", "不吃饭", "限制饮食", "削减必要生活支出", "减少正常饮食开支"} {
		if strings.Contains(text, phrase) && !strings.Contains(text, "禁止"+phrase) && !strings.Contains(text, "不得"+phrase) && !strings.Contains(text, "不要"+phrase) && !strings.Contains(text, "不"+phrase) {
			return out, entity.Fail("INVALID_ARGUMENT", "unsafe health-linked financial constraint")
		}
	}
	for _, phrase := range []string{"禁止转账", "不要转账", "不许转账"} {
		if strings.Contains(text, phrase) && !has(out.ProhibitedOperations, "transfer") {
			out.ProhibitedOperations = append(out.ProhibitedOperations, "transfer")
			out.Evidence["prohibited:transfer"] = phrase
		}
	}
	return out, nil
}
