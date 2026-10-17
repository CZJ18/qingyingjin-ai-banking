package execution

import (
	"github.com/qingyingjin/banking-service/internal/cards"
	"testing"
)

func TestCardParametersRejectPANAndInjectedAuthority(t *testing.T) {
	for _, raw := range []string{`{"action":"bind","card_number":"4111111111111111"}`, `{"action":"bind","cvv":"123"}`, `{"action":"unbind","card_id":"x","user_id":"42"}`, `{"action":"bind","credit_limit_minor":1.5}`, `{"action":"bind","confirmed":true}`} {
		var p cards.Input
		if strict([]byte(raw), &p) == nil {
			t.Fatalf("accepted forbidden card fields: %s", raw)
		}
	}
}
