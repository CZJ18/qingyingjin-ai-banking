package cards

import (
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/entity"
	"testing"
)

func TestCardApplicationControlsAndLoss(t *testing.T) {
	p := input()
	p.Action = "apply"
	p.CardType = "credit"
	p.CreditLimitMinor = 100000
	portfolio, err := Apply(nil, p, "new", 1)
	if err != nil {
		t.Fatalf("explicit simulated card application must work: %v", err)
	}
	apply := func(raw string, now int64) {
		t.Helper()
		var in Input
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatal(err)
		}
		next, err := Apply(portfolio, in, "", now)
		if err != nil {
			t.Fatal(err)
		}
		portfolio = next
	}
	apply(`{"action":"adjust_limit","card_id":"new","credit_limit_minor":200000}`, 2)
	if portfolio[0].CreditLimitMinor != 200000 || portfolio[0].CreditUsedMinor != 0 {
		t.Fatal("metadata limit adjustment must preserve used credit")
	}
	apply(`{"action":"set_transaction_limit","card_id":"new","transaction_limit_minor":5000}`, 3)
	raw, _ := json.Marshal(portfolio[0])
	var saved map[string]any
	json.Unmarshal(raw, &saved)
	if saved["transaction_limit_minor"] != float64(5000) {
		t.Fatal("limit must be persisted")
	}
	apply(`{"action":"set_default","card_id":"new"}`, 4)
	apply(`{"action":"lock","card_id":"new"}`, 5)
	if portfolio[0].Status != "locked" || portfolio[0].Default {
		t.Fatal("lock must disable the card and default preference")
	}
	apply(`{"action":"unlock","card_id":"new"}`, 6)
	if portfolio[0].Status != "active" {
		t.Fatal("unlock failed")
	}
	apply(`{"action":"report_lost","card_id":"new"}`, 7)
	if portfolio[0].Status != "lost" {
		t.Fatal("loss must persist")
	}
	if _, err := Apply(portfolio, Input{Action: "unlock", CardID: "new"}, "", 8); entity.Code(err) != "STATE_CONFLICT" {
		t.Fatal("lost card cannot be silently unlocked")
	}
	apply(`{"action":"unbind","card_id":"new"}`, 9)
	if portfolio[0].Status != "unbound" {
		t.Fatal("retain unbound lost-card history")
	}
}

func TestCardLimitCannotFallBelowUsedCredit(t *testing.T) {
	p := input()
	p.CardType = "credit"
	p.CreditLimitMinor = 10000
	a, e := Apply(nil, p, "new", 1)
	if e != nil {
		t.Fatal(e)
	}
	a[0].CreditUsedMinor = 6000
	if _, e = Apply(a, Input{Action: "adjust_limit", CardID: "new", CreditLimitMinor: 5000}, "", 2); entity.Code(e) != "STATE_CONFLICT" {
		t.Fatalf("lower than used credit must be rejected: %v", e)
	}
}

func TestCardControlInputsAreStrict(t *testing.T) {
	for _, raw := range []string{
		`{"action":"adjust_limit","card_id":"new"}`,
		`{"action":"adjust_limit","card_id":"new","credit_limit_minor":0}`,
		`{"action":"set_transaction_limit","card_id":"new"}`,
		`{"action":"set_transaction_limit","card_id":"new","transaction_limit_minor":-1}`,
		`{"action":"lock","card_id":"new","credit_limit_minor":10}`,
		`{"action":"adjust_limit","card_id":"new","account_id":"5"}`,
	} {
		var in Input
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatal(err)
		}
		if Validate(in) == nil {
			t.Fatalf("extraneous or missing controls accepted: %s", raw)
		}
	}
}

func TestCardPaymentLimitsAndInactiveStates(t *testing.T) {
	limit := int64(5000)
	c := Card{AccountID: 5, Status: "active", CardType: "debit", IsSimulated: true, TransactionLimitMinor: &limit}
	if err := CheckPayment(c, 5, 5000); err != nil {
		t.Fatal(err)
	}
	if err := CheckPayment(c, 5, 5001); entity.Code(err) != "CARD_LIMIT_EXCEEDED" {
		t.Fatal("limit bypass", err)
	}
	limit = 0
	if err := CheckPayment(c, 5, 1); entity.Code(err) != "CARD_LIMIT_EXCEEDED" {
		t.Fatal("zero must block", err)
	}
	for _, status := range []string{"locked", "lost", "unbound"} {
		c.Status = status
		if err := CheckPayment(c, 5, 1); entity.Code(err) != "CARD_UNAVAILABLE" {
			t.Fatal(status, err)
		}
	}
	c.Status = "active"
	if err := CheckPayment(c, 6, 1); entity.Code(err) != "CARD_UNAVAILABLE" {
		t.Fatal("account mismatch", err)
	}
}

func TestLockedCardCanBeReportedLostWithoutUnlocking(t *testing.T) {
	a, e := Apply(nil, input(), "new", 1)
	if e != nil {
		t.Fatal(e)
	}
	a, e = Apply(a, Input{Action: "lock", CardID: "new"}, "", 2)
	if e != nil {
		t.Fatal(e)
	}
	a, e = Apply(a, Input{Action: "report_lost", CardID: "new"}, "", 3)
	if e != nil || a[0].Status != "lost" {
		t.Fatalf("reporting a locked card lost must not require re-enabling it: %v", e)
	}
}
