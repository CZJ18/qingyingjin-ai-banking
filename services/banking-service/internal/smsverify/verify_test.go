package smsverify

import (
	"context"
	"strings"
	"testing"

	"github.com/qingyingjin/banking-service/internal/entity"
)

type testSender struct{}

func (testSender) Mode() string                                           { return "development" }
func (testSender) SendCode(context.Context, string, string, string) error { return nil }

func TestDisabledVerificationFailsClosed(t *testing.T) {
	for _, v := range []*Verifier{nil, New(nil, nil, ""), New(nil, testSender{}, strings.Repeat("s", 32))} {
		if v.Enabled() {
			t.Fatal("unconfigured verifier enabled")
		}
		if _, err := v.Issue(context.Background(), "+8613800138000", RegisterPhone, "unit", Binding{}); entity.Code(err) != "SMS_UNAVAILABLE" {
			t.Fatal("missing provider must fail closed")
		}
	}
}

func TestOTPGenerationAndBinding(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 32; i++ {
		code, err := generateCode()
		if err != nil || len(code) != 6 || strings.Trim(code, "0123456789") != "" {
			t.Fatal("OTP must have six random decimal digits")
		}
		seen[code] = true
	}
	if len(seen) < 30 {
		t.Fatal("OTP generation is not random")
	}
	v := New(nil, testSender{}, strings.Repeat("s", 32))
	a := v.digest("+8613800138000", RegisterPhone, "challenge-a", "012345")
	for _, b := range []string{v.digest("+8613800138001", RegisterPhone, "challenge-a", "012345"), v.digest("+8613800138000", ResetPassword, "challenge-a", "012345"), v.digest("+8613800138000", RegisterPhone, "challenge-b", "012345"), v.digest("+8613800138000", RegisterPhone, "challenge-a", "012346")} {
		if a == b {
			t.Fatal("OTP is not bound to exact phone, purpose, challenge and value")
		}
	}
}
