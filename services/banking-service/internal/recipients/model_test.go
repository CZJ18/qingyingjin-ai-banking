package recipients

import (
	"github.com/qingyingjin/banking-service/internal/entity"
	"testing"
)

func TestExactRecipientQueries(t *testing.T) {
	for _, raw := range []string{"13800138000", "+8613800138000", " 13800138000 "} {
		v, e := NormalizeQuery(SearchInput{Kind: "phone", Query: raw})
		if e != nil || v != "+8613800138000" {
			t.Fatal(v, e)
		}
	}
	v, e := NormalizeQuery(SearchInput{Kind: "name", Query: " 张三 "})
	if e != nil || v != "张三" {
		t.Fatal(v, e)
	}
}
func TestInvalidRecipientQueries(t *testing.T) {
	for _, in := range []SearchInput{{Kind: "phone", Query: "138****8000"}, {Kind: "phone", Query: "138"}, {Kind: "name", Query: ""}, {Kind: "name", Query: "张"}, {Kind: "all", Query: "张三"}} {
		if _, e := NormalizeQuery(in); entity.Code(e) != "INVALID_ARGUMENT" {
			t.Fatal(in, e)
		}
	}
}
func TestPeerPaymentValidation(t *testing.T) {
	in := PaymentInput{RecipientID: 2, RecipientVersion: 1, SourceAccountID: 3, AmountMinor: 20000, Description: "聚餐费用"}
	if e := ValidatePayment(1, in); e != nil {
		t.Fatal(e)
	}
	for _, n := range []int64{0, -1, entity.MaxBalance + 1} {
		v := in
		v.AmountMinor = n
		if e := ValidatePayment(1, v); entity.Code(e) != "INVALID_ARGUMENT" {
			t.Fatal(e)
		}
	}
	in.RecipientID = 1
	if e := ValidatePayment(1, in); entity.Code(e) != "INVALID_ARGUMENT" {
		t.Fatal(e)
	}
}
func TestRecipientFingerprintBindsIdentity(t *testing.T) {
	a := Recipient{ID: 2, DisplayName: "张三", PhoneMasked: "138****8000", Version: 1, AccountName: "生活账户", AccountMaskedNumber: "SIM ****1234", PhoneVerificationMethod: "development"}
	b := a
	if Fingerprint(a) != Fingerprint(b) {
		t.Fatal("unstable")
	}
	b.DisplayName = "李四"
	if Fingerprint(a) == Fingerprint(b) {
		t.Fatal("unbound name")
	}
	b = a
	b.Version++
	if Fingerprint(a) == Fingerprint(b) {
		t.Fatal("unbound version")
	}
}
