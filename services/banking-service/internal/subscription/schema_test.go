package subscription

import (
	"bytes"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/entity"
	"testing"
)

func TestSchemaRejectsFabricatedMoneyAndFinancialActions(t *testing.T) {
	for _, raw := range []string{`{"task_type":"transfer","user_id":"999"}`, `{"amount_minor":12.5}`, `{"status":"executed","approval":true}`} {
		if validate("subscription-report", []byte(raw)) == nil {
			t.Fatal("invalid output accepted", raw)
		}
	}
	if _, e := ID("0002"); e == nil {
		t.Fatal("noncanonical ID accepted")
	}
	if _, e := ID("9223372036854775808"); e == nil {
		t.Fatal("overflow accepted")
	}
	if _, e := Forecast(1000000000001, "monthly"); e == nil {
		t.Fatal("out-of-range money accepted")
	}
	if normalizeMerchant(" Gym ") != "gym" {
		t.Fatal("normalization")
	}
}
func TestCalendarClampAndCadences(t *testing.T) {
	// The detector test covers January 31 -> February 28 -> March 31.
	for _, c := range []string{"weekly", "monthly", "quarterly", "annual"} {
		f, e := Forecast(1, c)
		if e != nil || f.AnnualMinor < 1 {
			t.Fatal(c, f, e)
		}
	}
}

func TestNativeCozeIntegerResponseHasNoNullTimes(t *testing.T) {
	// Current exec_tool.go uses json.Number.Int64, and does not handle nullable
	// primitives. Omit unset optional fields rather than inventing a zero date.
	for _, v := range []any{entity.Subscription{}, entity.SubscriptionActionIntent{}} {
		raw, e := json.Marshal(v)
		if e != nil {
			t.Fatal(e)
		}
		if bytes.Contains(raw, []byte(":null")) {
			t.Fatal("native plugin integer formatter cannot accept nil", string(raw))
		}
	}
	at := int64(1770000000000)
	s := entity.Subscription{LastUsedAt: &at, CancelledAt: &at}
	raw, _ := json.Marshal(s)
	var value map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if e := decoder.Decode(&value); e != nil {
		t.Fatal(e)
	}
	for _, name := range []string{"last_used_at", "cancelled_at"} {
		n, ok := value[name].(json.Number)
		if !ok {
			t.Fatal("time is not an integer", name)
		}
		got, e := n.Int64()
		if e != nil || got != at {
			t.Fatal("time precision changed", got, e)
		}
	}
}
