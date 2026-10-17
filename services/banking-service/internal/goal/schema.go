package goal

import (
	"bytes"
	"embed"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"io"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"
)

//go:embed schemas/*.json
var Schemas embed.FS
var schemaOnce sync.Once
var compiled map[string]*jsonschema.Schema
var schemaError error

func schemaValidate(name string, raw []byte) error {
	schemaOnce.Do(func() {
		compiled = map[string]*jsonschema.Schema{}
		for _, n := range []string{"goal-parse", "task-plan"} {
			raw, e := Schemas.ReadFile("schemas/" + n + ".schema.json")
			if e != nil {
				schemaError = e
				return
			}
			var doc any
			d := json.NewDecoder(bytes.NewReader(raw))
			d.UseNumber()
			if e = d.Decode(&doc); e != nil {
				schemaError = e
				return
			}
			c := jsonschema.NewCompiler()
			if e = c.AddResource("urn:qingyingjin:"+n+":1", doc); e != nil {
				schemaError = e
				return
			}
			s, e := c.Compile("urn:qingyingjin:" + n + ":1")
			if e != nil {
				schemaError = e
				return
			}
			compiled[n] = s
		}
	})
	if schemaError != nil {
		return entity.Fail("DEPENDENCY_UNAVAILABLE", "goal schema unavailable")
	}
	var doc any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if len(raw) > 65536 || d.Decode(&doc) != nil {
		return entity.Fail("MODEL_OUTPUT_INVALID", "invalid structured JSON")
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return entity.Fail("MODEL_OUTPUT_INVALID", "one JSON object required")
	}
	if err := compiled[name].Validate(doc); err != nil {
		return entity.Fail("MODEL_OUTPUT_INVALID", "JSON Schema validation failed")
	}
	return nil
}

var amountPattern = regexp.MustCompile(`([0-9]+)(?:\.([0-9]{1,2}))?\s*(元|分)`)

func amountIn(evidence string, want int64) bool {
	// Reject partial matches inside negative/scientific/thousands/overprecision
	// notation. These formats need clarification rather than guessed conversion.
	for _, indices := range amountPattern.FindAllStringIndex(evidence, -1) {
		if indices[0] > 0 {
			before, _ := utf8.DecodeLastRuneInString(strings.TrimRight(evidence[:indices[0]], " \t"))
			if strings.ContainsRune("0123456789.,eE-+", before) {
				return false
			}
		}
	}
	if len(amountPattern.FindAllStringIndex(evidence, -1)) != 1 {
		return false
	}
	for _, m := range amountPattern.FindAllStringSubmatch(evidence, -1) {
		whole, e := strconv.ParseInt(m[1], 10, 64)
		if e != nil || whole > 1_000_000_000_000 {
			continue
		}
		var amount int64
		if m[3] == "分" {
			if m[2] != "" {
				continue
			}
			amount = whole
		} else {
			amount = whole * 100
			if m[2] != "" {
				fraction, _ := strconv.ParseInt(m[2], 10, 64)
				if len(m[2]) == 1 {
					fraction *= 10
				}
				amount += fraction
			}
		}
		if amount == want {
			return true
		}
	}
	return false
}
func DecodeParsed(raw []byte, text string) (Parsed, error) {
	var p Parsed
	if err := schemaValidate("goal-parse", raw); err != nil {
		return p, err
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, entity.Fail("MODEL_OUTPUT_INVALID", "integer fields required")
	}
	for field, v := range map[string]*int64{"savings_monthly_minor": p.SavingsMonthlyMinor, "monthly_income_minor": p.MonthlyIncomeMinor, "spending_budget_minor": p.SpendingBudgetMinor, "essential_expenses_minor": p.EssentialExpensesMinor} {
		if v == nil {
			continue
		}
		ev := p.Evidence[field]
		if ev == "" || !strings.Contains(text, ev) || !amountIn(ev, *v) {
			return p, entity.Fail("MODEL_OUTPUT_INVALID", "amount lacks matching source evidence")
		}
		if !strings.Contains(ev, "月") {
			return p, entity.Fail("MODEL_OUTPUT_INVALID", "monthly amount lacks monthly evidence")
		}
		keywords := map[string][]string{"savings_monthly_minor": {"储蓄", "存", "储存", "攒"}, "monthly_income_minor": {"收入", "工资", "薪"}, "spending_budget_minor": {"消费", "支出预算", "生活预算", "开支预算"}, "essential_expenses_minor": {"必要", "必需", "基本生活", "刚性"}}
		matched := false
		for _, word := range keywords[field] {
			if strings.Contains(ev, word) {
				matched = true
			}
		}
		if !matched {
			return p, entity.Fail("MODEL_OUTPUT_INVALID", "amount evidence does not match its financial role")
		}
	}
	if p.SavingsTargetMinor != nil {
		ev := p.Evidence["savings_target_minor"]
		if ev == "" || !strings.Contains(text, ev) || !amountIn(ev, *p.SavingsTargetMinor) || (!strings.Contains(ev, "总") && !strings.Contains(ev, "目标金额") && !strings.Contains(ev, "攒够")) {
			return p, entity.Fail("MODEL_OUTPUT_INVALID", "total savings target lacks matching evidence")
		}
	}
	if p.PeriodMonths != nil {
		ev := p.Evidence["period_months"]
		if ev == "" || !strings.Contains(text, ev) {
			return p, entity.Fail("MODEL_OUTPUT_INVALID", "period lacks source evidence")
		}
		periods := map[string]int64{"一个月": 1, "一个月内": 1, "两个月": 2, "二个月": 2, "三个月": 3, "四个月": 4, "五个月": 5, "六个月": 6, "七个月": 7, "八个月": 8, "九个月": 9, "十个月": 10, "一年": 12, "半年": 6}
		matched := false
		for token, n := range periods {
			index := strings.Index(ev, token)
			boundary := true
			if index > 0 {
				before, _ := utf8.DecodeLastRuneInString(ev[:index])
				boundary = !strings.ContainsRune("一二三四五六七八九十百零两0123456789", before)
			}
			if index >= 0 && boundary && n == *p.PeriodMonths {
				matched = true
			}
		}
		r := regexp.MustCompile(`([0-9]+)\s*(个月|月|年)`)
		for _, m := range r.FindAllStringSubmatch(ev, -1) {
			n, _ := strconv.ParseInt(m[1], 10, 64)
			if m[2] == "年" {
				n *= 12
			}
			if n == *p.PeriodMonths {
				matched = true
			}
		}
		if !matched {
			return p, entity.Fail("MODEL_OUTPUT_INVALID", "period does not match evidence")
		}
	}
	for _, c := range p.Constraints {
		if c.Kind == "safety" || c.Evidence == "" || !strings.Contains(text, c.Evidence) {
			return p, entity.Fail("MODEL_OUTPUT_INVALID", "model constraints must cite user text; safety constraints are server owned")
		}
	}
	for _, op := range p.ProhibitedOperations {
		ev := p.Evidence["prohibited:"+op]
		if ev == "" || !strings.Contains(text, ev) {
			return p, entity.Fail("MODEL_OUTPUT_INVALID", "prohibition lacks source evidence")
		}
	}
	if (strings.Contains(text, "体重") || strings.Contains(text, "减肥") || strings.Contains(text, "减重")) && (strings.Contains(text, "奖励") || strings.Contains(text, "惩罚") || strings.Contains(text, "扣钱") || strings.Contains(text, "奖金")) {
		p.Constraints = append(p.Constraints, Constraint{Kind: "mandatory", Description: "拒绝根据体重变化、限制饮食生成金融奖惩", Evidence: text})
	}
	// Defense against the model omitting explicit prohibitions in the source.
	for op, phrases := range map[string][]string{"transfer": {"禁止转账", "不要转账", "不允许转账", "不得转账"}, "cancel_subscription": {"禁止取消订阅", "不要取消订阅", "不允许取消订阅"}, "all_financial_operations": {"禁止任何金融操作", "不要进行任何金融操作"}} {
		for _, phrase := range phrases {
			if strings.Contains(text, phrase) {
				exists := false
				for _, value := range p.ProhibitedOperations {
					if value == op {
						exists = true
					}
				}
				if !exists {
					p.ProhibitedOperations = append(p.ProhibitedOperations, op)
					p.Evidence["prohibited:"+op] = phrase
				}
				break
			}
		}
	}
	for _, phrase := range []string{"为了存钱不吃饭", "为了储蓄不吃饭", "减少正常饮食开支", "削减必要生活支出", "限制饮食奖励"} {
		if strings.Contains(text, phrase) {
			p.Constraints = append(p.Constraints, Constraint{Kind: "mandatory", Description: "拒绝根据体重变化、限制饮食生成金融奖惩", Evidence: phrase})
			break
		}
	}
	return p, nil
}
