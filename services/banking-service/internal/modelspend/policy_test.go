package modelspend

import (
	"math"
	"strings"
	"testing"
	"time"
)

func syntheticPolicy() Policy {
	return Policy{ProjectID: "synthetic-project", Currency: "USD", Model: "synthetic-model", PriceVersion: "synthetic-v1", PriceSource: "synthetic:test-only", PriceEvidenceSHA256: strings.Repeat("a", 64), ValidFrom: time.Now().Add(-time.Hour).UTC().Format(time.RFC3339), ValidUntil: time.Now().Add(time.Hour).UTC().Format(time.RFC3339), Limit: "10", InputPrice: "1", CacheHitPrice: "0.1", OutputPrice: "2", ContextTokens: 1000, MaxOutputTokens: 100}
}
func TestPriceSnapshotHasEvidenceAndEffectiveStart(t *testing.T) {
	p := syntheticPolicy()
	p.PriceEvidenceSHA256 = ""
	if p.Check() == nil {
		t.Fatal("price evidence digest missing")
	}
	p = syntheticPolicy()
	p.ValidFrom = time.Now().Add(time.Minute).UTC().Format(time.RFC3339)
	if p.Check() != nil || p.Validate(time.Now()) == nil {
		t.Fatal("future price used before effective start")
	}
	p.ValidFrom = p.ValidUntil
	if p.Check() == nil {
		t.Fatal("empty effective interval")
	}
}
func TestMoneyParsingIsExactAndBounded(t *testing.T) {
	for raw, want := range map[string]int64{"0": 0, "0.00000001": 1, "1.23000000": 123000000, "92233720368.54775807": math.MaxInt64} {
		got, e := ParseMoney(raw)
		if e != nil || got != want {
			t.Fatalf("%q: %v %v", raw, got, e)
		}
		if round, e := ParseMoney(FormatMoney(got)); e != nil || round != want {
			t.Fatal("roundtrip", raw)
		}
	}
	for _, raw := range []string{"", "-1", "NaN", "Infinity", "1e9", "0.000000001", "1.", ".1", "01", " 1", "92233720368.54775808", "999999999999999999999999999999999999"} {
		if _, e := ParseMoney(raw); e == nil {
			t.Fatalf("accepted %q", raw)
		}
	}
}
func TestPolicyHasNoPriceOrLimitDefaults(t *testing.T) {
	p := syntheticPolicy()
	if e := p.Validate(time.Now()); e != nil {
		t.Fatal(e)
	}
	for _, mutate := range []func(*Policy){func(p *Policy) { p.Limit = "" }, func(p *Policy) { p.Currency = "" }, func(p *Policy) { p.InputPrice = "0" }, func(p *Policy) { p.OutputPrice = "" }, func(p *Policy) { p.PriceSource = "" }, func(p *Policy) { p.PriceVersion = "" }, func(p *Policy) { p.CacheHitPrice = "2" }, func(p *Policy) { p.ContextTokens = 0 }, func(p *Policy) { p.MaxOutputTokens = p.ContextTokens + 1 }, func(p *Policy) { p.ProjectID = "../other" }, func(p *Policy) { p.ValidUntil = "invalid" }} {
		bad := p
		mutate(&bad)
		if e := bad.Check(); e == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	expired := p
	expired.ValidFrom = time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	expired.ValidUntil = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	if expired.Check() != nil || expired.Validate(time.Now()) == nil {
		t.Fatal("expiry must block dispatch, not historical reconciliation")
	}
}
func TestWorstCaseReservationAndUsageEstimate(t *testing.T) {
	p := syntheticPolicy()
	held, e := p.ReserveUnits()
	if e != nil || held != 120000 {
		t.Fatal(held, e)
	}
	hit, miss := int64(90), int64(10)
	used, e := p.Estimate(Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110, CacheHitTokens: &hit, CacheMissTokens: &miss})
	if e != nil || used != 3900 {
		t.Fatal(used, e)
	}
	used, e = p.Estimate(Usage{PromptTokens: 100, CompletionTokens: 10, TotalTokens: 110})
	if e != nil || used != 12000 {
		t.Fatal("unknown cache must conservatively estimate all input", used, e)
	}
	for _, u := range []Usage{{PromptTokens: -1}, {PromptTokens: 10, TotalTokens: 11}, {PromptTokens: 1001, TotalTokens: 1001}, {CompletionTokens: 101, TotalTokens: 101}, {PromptTokens: 100, TotalTokens: 100, CacheHitTokens: &hit}, {PromptTokens: 99, TotalTokens: 99, CacheHitTokens: &hit, CacheMissTokens: &miss}} {
		if _, e = p.Estimate(u); e == nil {
			t.Fatalf("accepted %+v", u)
		}
	}
	p.InputPrice = "92233720368.54775807"
	p.ContextTokens = 1_000_000_000
	if _, e = p.ReserveUnits(); e == nil {
		t.Fatal("overflowing reservation accepted")
	}
}
func TestTinyReservationRoundsUpAndPolicyHashBindsPrices(t *testing.T) {
	p := syntheticPolicy()
	p.ContextTokens = 1
	p.MaxOutputTokens = 1
	p.InputPrice = "0.00000001"
	p.CacheHitPrice = "0"
	p.OutputPrice = "0.00000001"
	v, e := p.ReserveUnits()
	if e != nil || v != 1 {
		t.Fatal(v, e)
	}
	a, _ := p.Hash()
	p.PriceVersion = "synthetic-v2"
	b, _ := p.Hash()
	if a == b || len(a) != 64 {
		t.Fatal("policy snapshot not bound")
	}
}
