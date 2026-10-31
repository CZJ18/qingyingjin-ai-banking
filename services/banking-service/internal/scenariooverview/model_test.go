package scenariooverview

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestScenarioOverviewOutputRejectsIdentityRangeAndSensitiveFields(t *testing.T) {
	amount := int64(0)
	valid := Output{Scenario: "transfer", AccountID: "1", UserID: "42", IsSimulated: true, Items: []Item{{ID: "1", Kind: "source_account", Status: "active", AmountMinor: &amount, Currency: "CNY"}}, Limitations: []string{"Synthetic isolated data; explicit authorization still required."}}
	in := Input{Scenario: "transfer", AccountID: "1"}
	if e := valid.ValidateFor(in, 42); e != nil {
		t.Fatal(e)
	}
	cases := []func(*Output){func(v *Output) { v.UserID = "43" }, func(v *Output) { v.AccountID = "2" }, func(v *Output) { v.IsSimulated = false }, func(v *Output) { v.Items = nil }, func(v *Output) { v.Items = make([]Item, 51) }, func(v *Output) { v.Limitations = nil }, func(v *Output) { v.Truncated = true }, func(v *Output) { v.Items[0].Kind = "execute" }, func(v *Output) { v.Items[0].Label = "PRIVATE_NAME" }, func(v *Output) { n := int64(-1); v.Items[0].AmountMinor = &n }}
	for i, change := range cases {
		raw, _ := json.Marshal(valid)
		var v Output
		json.Unmarshal(raw, &v)
		change(&v)
		if e := v.ValidateFor(in, 42); e == nil {
			t.Errorf("unsafe output %d accepted", i)
		}
	}
	raw, _ := json.Marshal(valid)
	for _, private := range []string{"phone", "name", "card_number", "api_key", "nonce", "authorization"} {
		if strings.Contains(string(raw), `"`+private+`"`) {
			t.Errorf("private field %s leaked", private)
		}
	}
}
func TestScenarioOverviewInputIsExactBoundedContract(t *testing.T) {
	for _, s := range []string{"transfer", "cards", "investment", "subscriptions", "birthday"} {
		if e := (Input{Scenario: s, AccountID: "9223372036854775807"}).Validate(); e != nil {
			t.Fatal(s, e)
		}
	}
	for _, id := range []string{"", "0", "-1", "01", "+1", "1 ", "9223372036854775808"} {
		if e := (Input{Scenario: "cards", AccountID: id}).Validate(); e == nil {
			t.Errorf("bad ID accepted: %q", id)
		}
	}
}

func TestScenarioOverviewDecodeRejectsMissingAndExtraFields(t *testing.T) {
	in := Input{Scenario: "cards", AccountID: "1"}
	valid := `{"scenario":"cards","account_id":"1","user_id":"42","is_simulated":true,"items":[],"limitations":["Synthetic isolated records only."],"truncated":false}`
	if _, e := DecodeOutput([]byte(valid), in, 42); e != nil {
		t.Fatal(e)
	}
	for _, bad := range []string{strings.Replace(valid, `,"truncated":false`, "", 1), strings.Replace(valid, `"truncated":false`, `"truncated":null`, 1), strings.Replace(valid, `"truncated":false`, `"truncated":"false"`, 1), strings.Replace(valid, `"items":[]`, `"items":[{"id":"c","kind":"card","status":"active","phone":"PRIVATE_PHONE"}]`, 1), valid + `{}`, strings.Replace(valid, `"truncated":false`, `"truncated":false,"authorization_id":"PRIVATE_AUTH"`, 1)} {
		if _, e := DecodeOutput([]byte(bad), in, 42); e == nil {
			t.Error("malformed scenario result accepted")
		}
	}
}

func TestScenarioOverviewKeepsExistingSubscriptionCadence(t *testing.T) {
	amount := int64(100)
	for _, interval := range []string{"weekly", "monthly", "quarterly", "annual"} {
		v := Output{Scenario: "subscriptions", AccountID: "1", UserID: "42", IsSimulated: true, Items: []Item{{ID: "7", Kind: "subscription", Status: "candidate", Currency: "CNY", Version: 1, AmountMinor: &amount, BillingInterval: interval}}, Limitations: []string{"Candidate pattern only."}}
		if e := v.ValidateFor(Input{Scenario: "subscriptions", AccountID: "1"}, 42); e != nil {
			t.Errorf("existing cadence %s rejected: %v", interval, e)
		}
	}
}
