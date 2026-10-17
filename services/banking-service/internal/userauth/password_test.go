package userauth

import (
	"strings"
	"testing"
)

func TestPhoneNormalizationAndMask(t *testing.T) {
	for _, in := range []string{"13800138000", "+8613800138000", " 13800138000 "} {
		got, err := normalizePhone(in)
		if err != nil || got != "+8613800138000" || maskPhone(got) != "138****8000" {
			t.Fatalf("normalization failed: %q", in)
		}
	}
	for _, in := range []string{"", "123", "8613800138000", "138-0013-8000", "12800138000", "+1 13800138000"} {
		if _, err := normalizePhone(in); err == nil {
			t.Fatalf("accepted invalid phone %q", in)
		}
	}
}

func TestPasswordHashUsesRandomSaltAndRejectsMalformedHashes(t *testing.T) {
	p := "密码足够长 123"
	a, err := hashPassword(p)
	if err != nil {
		t.Fatal(err)
	}
	b, err := hashPassword(p)
	if err != nil {
		t.Fatal(err)
	}
	if a == b || strings.Contains(a, p) || !strings.HasPrefix(a, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatal("unsafe password encoding")
	}
	if !passwordMatches(a, p) || passwordMatches(a, p+" ") {
		t.Fatal("password comparison failed")
	}
	for _, bad := range []string{"", "plain-password", strings.Replace(a, "m=19456", "m=999999999", 1), strings.Replace(a, "v=19", "v=18", 1), a + "$extra"} {
		if passwordMatches(bad, p) {
			t.Fatal("malformed hash accepted")
		}
	}
}

func TestPasswordValidationUnicodeAndWhitespace(t *testing.T) {
	for _, p := range []string{"12345678", strings.Repeat("密", 128), " pass word "} {
		if err := validatePassword(p); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"", "1234567", strings.Repeat("a", 129), string([]byte{255, 255, 255, 255, 255, 255, 255, 255})} {
		if err := validatePassword(p); err == nil {
			t.Fatal("invalid password accepted")
		}
	}
}
