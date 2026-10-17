package userauth

import (
	"context"
	"strings"
	"testing"

	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/identity"
)

func TestVerificationAndResetFailClosedWhenDisabled(t *testing.T) {
	s := New(nil, identity.New(strings.Repeat("u", 32), "i", "a", "t", "42", ""), nil)
	ctx := context.Background()
	if _, err := s.RequestRegistrationCode(ctx, "13800138000", "127.0.0.1"); entity.Code(err) != "SMS_UNAVAILABLE" {
		t.Fatalf("disabled registration verification: %v", err)
	}
	if _, err := s.RequestPasswordReset(ctx, "13800138000", "127.0.0.1"); entity.Code(err) != "SMS_UNAVAILABLE" {
		t.Fatalf("disabled reset request: %v", err)
	}
	if err := s.ResetPassword(ctx, PasswordResetInput{Phone: "13800138000", NewPassword: "new-password-123"}, "127.0.0.1"); entity.Code(err) != "SMS_UNAVAILABLE" {
		t.Fatalf("disabled reset: %v", err)
	}
	if _, err := s.RequestPhoneVerification(ctx, identity.Actor{Kind: "agent", UserID: "42"}, "127.0.0.1"); entity.Code(err) != "FORBIDDEN" {
		t.Fatalf("agent verification request: %v", err)
	}
	if err := s.ConfirmPhoneVerification(ctx, identity.Actor{Kind: "agent", UserID: "42"}, PhoneVerificationInput{}, "127.0.0.1"); entity.Code(err) != "FORBIDDEN" {
		t.Fatalf("agent verification: %v", err)
	}
}
