package modelspend

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var decimal = regexp.MustCompile(`^(0|[1-9][0-9]{0,10})(\.[0-9]{1,8})?$`)
var identifier = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,63}$`)
var modelName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,127}$`)
var evidenceRef = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:/#-]{0,255}$`)

// ParseMoney converts exact currency-major decimals into 1e-8 currency units.
// No exponent, rounding, implicit currency, or floating point conversion.
func ParseMoney(raw string) (int64, error) {
	if !decimal.MatchString(raw) {
		return 0, ErrPolicy
	}
	parts := strings.Split(raw, ".")
	fraction := ""
	if len(parts) == 2 {
		fraction = parts[1]
	}
	n, ok := new(big.Int).SetString(parts[0]+fraction+strings.Repeat("0", 8-len(fraction)), 10)
	if !ok || !n.IsInt64() {
		return 0, ErrPolicy
	}
	return n.Int64(), nil
}
func FormatMoney(units int64) string {
	if units < 0 {
		return "invalid"
	}
	return fmt.Sprintf("%d.%08d", units/CurrencyScale, units%CurrencyScale)
}
func (p Policy) Check() error {
	if !identifier.MatchString(p.ProjectID) || !modelName.MatchString(p.Model) || !identifier.MatchString(p.PriceVersion) || !evidenceRef.MatchString(p.PriceSource) || !regexp.MustCompile(`^[A-Z]{3}$`).MatchString(p.Currency) {
		return ErrPolicy
	}
	until, e := time.Parse(time.RFC3339, p.ValidUntil)
	if e != nil {
		return ErrPolicy
	}
	from, e := time.Parse(time.RFC3339, p.ValidFrom)
	if e != nil || !from.Before(until) {
		return ErrPolicy
	}
	if len(p.PriceEvidenceSHA256) != 64 || strings.ToLower(p.PriceEvidenceSHA256) != p.PriceEvidenceSHA256 {
		return ErrPolicy
	}
	if _, e := hex.DecodeString(p.PriceEvidenceSHA256); e != nil {
		return ErrPolicy
	}
	limit, e := ParseMoney(p.Limit)
	if e != nil || limit <= 0 {
		return ErrPolicy
	}
	input, e := ParseMoney(p.InputPrice)
	if e != nil || input <= 0 {
		return ErrPolicy
	}
	output, e := ParseMoney(p.OutputPrice)
	if e != nil || output <= 0 {
		return ErrPolicy
	}
	cache, e := ParseMoney(p.CacheHitPrice)
	if e != nil || cache > input {
		return ErrPolicy
	}
	if p.ContextTokens <= 0 || p.ContextTokens > 1_000_000_000 || p.MaxOutputTokens <= 0 || p.MaxOutputTokens > p.ContextTokens {
		return ErrPolicy
	}
	_, e = priceSum([]int64{p.ContextTokens, p.MaxOutputTokens}, []int64{input, output})
	return e
}
func (p Policy) Validate(now time.Time) error {
	if e := p.Check(); e != nil {
		return e
	}
	until, _ := time.Parse(time.RFC3339, p.ValidUntil)
	from, _ := time.Parse(time.RFC3339, p.ValidFrom)
	if now.Before(from) || !now.Before(until) {
		return ErrPolicy
	}
	return nil
}
func (p Policy) Hash() (string, error) {
	if e := p.Check(); e != nil {
		return "", e
	}
	raw, _ := json.Marshal(p)
	return fmt.Sprintf("%x", sha256.Sum256(raw)), nil
}
func (p Policy) ReserveUnits() (int64, error) {
	if e := p.Check(); e != nil {
		return 0, e
	}
	input, _ := ParseMoney(p.InputPrice)
	output, _ := ParseMoney(p.OutputPrice)
	return priceSum([]int64{p.ContextTokens, p.MaxOutputTokens}, []int64{input, output})
}
func (p Policy) Estimate(u Usage) (int64, error) {
	if e := p.Check(); e != nil {
		return 0, e
	}
	if u.PromptTokens < 0 || u.CompletionTokens < 0 || u.TotalTokens < 0 || u.PromptTokens > 1_000_000_000_000 || u.CompletionTokens > 1_000_000_000_000 || u.TotalTokens != u.PromptTokens+u.CompletionTokens {
		return 0, ErrPolicy
	}
	if u.PromptTokens > p.ContextTokens || u.CompletionTokens > p.MaxOutputTokens || u.TotalTokens > p.ContextTokens {
		return 0, ErrBoundExceeded
	}
	input, _ := ParseMoney(p.InputPrice)
	output, _ := ParseMoney(p.OutputPrice)
	cache, _ := ParseMoney(p.CacheHitPrice)
	if (u.CacheHitTokens == nil) != (u.CacheMissTokens == nil) {
		return 0, ErrPolicy
	}
	if u.CacheHitTokens != nil {
		hit, miss := *u.CacheHitTokens, *u.CacheMissTokens
		if hit < 0 || miss < 0 || hit > u.PromptTokens || miss != u.PromptTokens-hit {
			return 0, ErrPolicy
		}
		return priceSum([]int64{hit, miss, u.CompletionTokens}, []int64{cache, input, output})
	}
	// Unknown cache breakdown gets the expensive uncached input estimate.
	return priceSum([]int64{u.PromptTokens, u.CompletionTokens}, []int64{input, output})
}
func priceSum(tokens, prices []int64) (int64, error) {
	sum := new(big.Int)
	for i, n := range tokens {
		sum.Add(sum, new(big.Int).Mul(big.NewInt(n), big.NewInt(prices[i])))
	}
	// Reserve/estimate at the smallest accounting unit, conservatively rounded up.
	sum.Add(sum, big.NewInt(999_999))
	sum.Quo(sum, big.NewInt(1_000_000))
	if !sum.IsInt64() {
		return 0, ErrPolicy
	}
	return sum.Int64(), nil
}

// CanonicalToken accepts JSON integer token text, never floating point values.
func CanonicalToken(raw string) (int64, error) {
	v, e := strconv.ParseInt(raw, 10, 64)
	if e != nil || v < 0 || strconv.FormatInt(v, 10) != raw {
		return 0, ErrPolicy
	}
	return v, nil
}
