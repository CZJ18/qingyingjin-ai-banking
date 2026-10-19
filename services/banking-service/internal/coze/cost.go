package coze

import (
	"context"
	"regexp"
)

// Cost is an untrusted provider report, never an invoice or monetary budget.
// Fixed decimal grammar avoids float rounding, exponent overflow and NaN.
var costPattern = regexp.MustCompile(`^(0|[1-9][0-9]{0,17})(\.[0-9]{1,8})?$`)
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

func ValidReportedCost(raw string) bool { return len(raw) <= 64 && costPattern.MatchString(raw) }
func ValidCostMetadata(currency, unit string) bool {
	return currency == "" && unit == "" || currencyPattern.MatchString(currency) && unit == "currency_major"
}

type CostReport struct {
	InvalidFormat                                  bool
	Raw, ExecuteID, Currency, Unit, ResponseDigest string
	Tokens                                         *int64
}
type CostReporter interface {
	RecordReport(context.Context, int64, string, CostReport) error
}
