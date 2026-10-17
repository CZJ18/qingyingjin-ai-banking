package aa

import (
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"testing"
)

func TestSingleManagerModeRules(t *testing.T) {
	in := Input{Title: "Dinner", PaymentMethod: "wechat_manual", TotalMinor: 100, Mode: "equal", AlreadyPaid: true, RecipientHint: "Owner", Items: []LineItem{{Name: "Dinner", AmountMinor: 100}}, Participants: []ShareInput{{Name: "Friend"}}}
	if _, e := Allocate(in); e != nil {
		t.Fatal(e)
	}
	if newManagement(in) != "single_manager" {
		t.Fatal("new manual bill should default single manager")
	}
	if management("") != "collaborative" {
		t.Fatal("old stored bill changed mode")
	}
	in.ManagementMode = "single_manager"
	in.PaymentMethod = "simulation"
	in.AccountID = 1
	if _, e := Allocate(in); entity.Code(e) != "INVALID_ARGUMENT" {
		t.Fatal("single management cannot execute simulated funds", e)
	}
	b := Bill{ID: uuid.NewString(), OwnerID: 1, PaymentMethod: "wechat_manual", ManagementMode: "single_manager", Status: "open", Shares: []Share{{ID: uuid.NewString(), UserID: 1, Status: "self"}, {ID: uuid.NewString(), Status: "awaiting_receipt", AmountMinor: 50}}}
	input := ReviewInput{ParticipantID: b.Shares[1].ID, Purpose: "confirm_received", ExpectedVersion: 1}
	if _, e := externalShare(&b, 1, input); e != nil {
		t.Fatal("owner must not need participant login or self-report", e)
	}
	if _, e := externalShare(&b, 2, input); entity.Code(e) != "FORBIDDEN" {
		t.Fatal("nonowner confirmation", e)
	}
	input.Purpose = "report_paid"
	if _, e := externalShare(&b, 1, input); entity.Code(e) != "STATE_CONFLICT" {
		t.Fatal("owner impersonated payer", e)
	}
	p := b.Shares[1]
	p.External = &ExternalRecord{ReceivedAt: 100}
	p.Status = "receipt_confirmed"
	if !validShareState(b, p) {
		t.Fatal("single manager receipt must not invent payer report")
	}
	b.ManagementMode = "collaborative"
	if validShareState(b, p) {
		t.Fatal("collaborative receipt missing payer accepted")
	}
	before := reviewDigest(b, p, 1, "confirm_received")
	b.ManagementMode = "single_manager"
	if before == reviewDigest(b, p, 1, "confirm_received") {
		t.Fatal("management mode not bound in receipt digest")
	}
	in.ManagementMode = "unknown"
	if _, e := Allocate(in); entity.Code(e) != "INVALID_ARGUMENT" {
		t.Fatal("unknown management mode accepted", e)
	}
}
