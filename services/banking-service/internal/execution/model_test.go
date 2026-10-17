package execution

import "testing"

func TestStrictFinancialParameters(t *testing.T) {
	for _, raw := range []string{`{"amount_minor":1.5}`, `{"amount_minor":1,"confirmed":true}`, `{"amount_minor":1} {}`, `null`} {
		var in struct {
			Amount int64 `json:"amount_minor"`
		}
		if strict([]byte(raw), &in) == nil {
			t.Fatalf("accepted unsafe parameters: %s", raw)
		}
	}
	var in struct {
		Amount int64 `json:"amount_minor"`
	}
	if strict([]byte(`{"amount_minor":20000}`), &in) != nil || in.Amount != 20000 {
		t.Fatal("integer cents rejected")
	}
}
