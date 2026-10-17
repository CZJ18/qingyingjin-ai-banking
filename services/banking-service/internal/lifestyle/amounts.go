package lifestyle

import (
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/goal"
	"regexp"
	"strconv"
)

var explicitMoney = regexp.MustCompile(`([0-9]+)(?:\.([0-9]{1,2}))?\s*(元|分)`)

// Model amounts are not used for arithmetic. Convert only the exact evidence
// selected by the model, with integer operations, then revalidate source/role.
// Direct SaveGoal/DecodeSpec still requires an already correct integer-cent spec.
func decodeModelSpec(raw []byte, text string) (Spec, error) {
	spec, e := decodeSpecShape(raw)
	if e != nil {
		return spec, e
	}
	for field, target := range map[string]*int64{"expense_reduction_monthly_minor": &spec.ExpenseReductionMonthlyMinor, "savings_monthly_minor": &spec.SavingsMonthlyMinor} {
		if *target == 0 {
			continue
		}
		evidence := spec.Evidence[field]
		matches := explicitMoney.FindAllStringSubmatch(evidence, -1)
		if len(matches) != 1 {
			return spec, entity.Fail("MODEL_OUTPUT_INVALID", "one explicit source amount required")
		}
		m := matches[0]
		whole, err := strconv.ParseInt(m[1], 10, 64)
		if err != nil || whole > entity.MaxBalance {
			return spec, entity.Fail("MODEL_OUTPUT_INVALID", "source amount outside supported range")
		}
		amount := whole
		if m[3] == "元" {
			if whole > entity.MaxBalance/100 {
				return spec, entity.Fail("MODEL_OUTPUT_INVALID", "source amount overflow")
			}
			amount = whole * 100
			if m[2] != "" {
				fraction, _ := strconv.ParseInt(m[2], 10, 64)
				if len(m[2]) == 1 {
					fraction *= 10
				}
				amount += fraction
			}
		} else if m[2] != "" {
			return spec, entity.Fail("MODEL_OUTPUT_INVALID", "fractional cent prohibited")
		}
		if amount > entity.MaxBalance || !goal.MatchesAmountEvidence(evidence, amount) {
			return spec, entity.Fail("MODEL_OUTPUT_INVALID", "ambiguous or unsupported source amount")
		}
		*target = amount
	}
	normalized, _ := json.Marshal(spec)
	return DecodeSpec(normalized, text)
}
