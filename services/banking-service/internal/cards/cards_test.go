package cards

import (
	"github.com/qingyingjin/banking-service/internal/entity"
	"testing"
)

func input() Input {
	return Input{Action: "bind", AccountID: 5, Issuer: "模拟银行", Name: "日常卡", LastFour: "1234", CardType: "debit"}
}
func TestBindAndDefaultRules(t *testing.T) {
	a, e := Apply(nil, input(), "first", 1)
	if e != nil || len(a) != 1 || a[0].Default {
		t.Fatalf("bind: %v %v", a, e)
	}
	p := input()
	p.AccountID = 6
	p.LastFour = "5678"
	a, e = Apply(a, p, "second", 2)
	if e != nil {
		t.Fatal(e)
	}
	a, e = Apply(a, Input{Action: "set_default", CardID: "first"}, "", 3)
	if e != nil || !a[0].Default {
		t.Fatal(e)
	}
	a, e = Apply(a, Input{Action: "set_default", CardID: "second"}, "", 4)
	if e != nil || a[0].Default || !a[1].Default {
		t.Fatal(e)
	}
	a, e = Apply(a, Input{Action: "unbind", CardID: "second"}, "", 5)
	if e != nil || a[1].Status != "unbound" || a[1].Default || len(a) != 2 {
		t.Fatal(e)
	}
	if _, e = Apply(a, Input{Action: "set_default", CardID: "second"}, "", 6); entity.Code(e) != "STATE_CONFLICT" {
		t.Fatal(e)
	}
}
func TestInvalidCardInputs(t *testing.T) {
	for _, change := range []func(*Input){func(p *Input) { p.LastFour = "1234567812345678" }, func(p *Input) { p.LastFour = "abcd" }, func(p *Input) { p.CardType = "other" }, func(p *Input) { p.Issuer = "" }, func(p *Input) { p.AccountID = 0 }, func(p *Input) { p.CreditLimitMinor = 1 }, func(p *Input) { p.CardType = "credit"; p.CreditLimitMinor = -1 }, func(p *Input) { p.CardID = "injected" }, func(p *Input) { p.Action = "repay" }} {
		p := input()
		change(&p)
		if Validate(p) == nil {
			t.Fatalf("accepted invalid: %+v", p)
		}
	}
}
func TestCreditLimitIsNotAccountMoney(t *testing.T) {
	p := input()
	p.CardType = "credit"
	p.CreditLimitMinor = 200000
	a, e := Apply(nil, p, "credit", 1)
	if e != nil || a[0].CreditLimitMinor != 200000 || a[0].CreditUsedMinor != 0 {
		t.Fatal(e)
	}
	p.CreditLimitMinor = entity.MaxBalance + 1
	if Validate(p) == nil {
		t.Fatal("overflow accepted")
	}
}
func TestDuplicatesAndIsolation(t *testing.T) {
	a, e := Apply(nil, input(), "first", 1)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = Apply(a, input(), "other", 2); entity.Code(e) != "STATE_CONFLICT" {
		t.Fatal(e)
	}
	if _, e = Apply(a, Input{Action: "unbind", CardID: "another-user-card"}, "", 2); entity.Code(e) != "NOT_FOUND" {
		t.Fatal(e)
	}
	if a[0].Status != "active" {
		t.Fatal("input mutated")
	}
}
func TestPortfolioLimitAndStrictAction(t *testing.T) {
	a := make([]Card, 20)
	p := input()
	if _, e := Apply(a, p, "new", 1); entity.Code(e) != "STATE_CONFLICT" {
		t.Fatal(e)
	}
	p = Input{Action: "unbind", CardID: "x", AccountID: 5}
	if Validate(p) == nil {
		t.Fatal("extraneous fields")
	}
}
