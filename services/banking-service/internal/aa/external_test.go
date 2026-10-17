package aa

import (
	"github.com/qingyingjin/banking-service/internal/entity"
	"testing"
)

func TestWeChatBillDetails(t *testing.T) {
	in := Input{Title: "聚餐", TotalMinor: 10000, Mode: "equal", AlreadyPaid: true, PaymentMethod: "wechat_manual", RecipientHint: "小林（请在微信核实）", Items: []LineItem{{Name: "餐费", AmountMinor: 8000}, {Name: "交通", AmountMinor: 2000}}, Participants: []ShareInput{{Name: "小陈"}}}
	if amounts, e := Allocate(in); e != nil || amounts[1] != 5000 {
		t.Fatal(amounts, e)
	}
	in.Items[1].AmountMinor = 1999
	if _, e := Allocate(in); entity.Code(e) != "INVALID_ARGUMENT" {
		t.Fatal("incorrect detail sum accepted", e)
	}
	in.Items = nil
	if _, e := Allocate(in); entity.Code(e) != "INVALID_ARGUMENT" {
		t.Fatal("wechat bill without details", e)
	}
}

func TestWeChatConsentBindsDetailsAndPurpose(t *testing.T) {
	b := Bill{ID: "bill", OwnerID: 1, Title: "聚餐", TotalMinor: 100, PaymentMethod: "wechat_manual", RecipientHint: "小林", Items: []LineItem{{Name: "餐费", AmountMinor: 100}}}
	p := Share{ID: "share", UserID: 2, AmountMinor: 50}
	a := reviewDigest(b, p, 2, "report_paid")
	if a == reviewDigest(b, p, 1, "confirm_received") {
		t.Fatal("purpose or actor not bound")
	}
	b.RecipientHint = "其他人"
	if a == reviewDigest(b, p, 2, "report_paid") {
		t.Fatal("recipient not bound")
	}
	b.RecipientHint = "小林"
	b.Items[0].Name = "其他费用"
	if a == reviewDigest(b, p, 2, "report_paid") {
		t.Fatal("detail not bound")
	}
}
