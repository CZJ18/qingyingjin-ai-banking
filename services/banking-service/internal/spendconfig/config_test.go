package spendconfig

import (
	"crypto/sha256"
	"fmt"
	"github.com/qingyingjin/banking-service/internal/modelspend"
	"strings"
	"testing"
)

func TestPriceReviewRequiresMatchingIndependentEvidence(t *testing.T) {
	raw := []byte("independently checked synthetic price document")
	p := modelspend.Policy{PriceSource: "supplier:verified-reference", PriceEvidenceSHA256: fmt.Sprintf("%x", sha256.Sum256(raw))}
	if ReviewPrice(p, raw, true) != nil {
		t.Fatal("review evidence rejected")
	}
	if ReviewPrice(p, raw, false) == nil || ReviewPrice(p, []byte("other"), true) == nil {
		t.Fatal("unconfirmed or wrong evidence accepted")
	}
	p.PriceSource = "synthetic:test-only"
	if ReviewPrice(p, raw, true) == nil {
		t.Fatal("synthetic policy authorizes paid supplier")
	}
}

func TestOperatorAuthenticationRejectsEmptyAndReusedCredentials(t *testing.T) {
	secret := strings.Repeat("a", 40)
	if Authorize(map[string]string{}, secret) == nil {
		t.Fatal("empty authority")
	}
	env := map[string]string{"BANKING_MODEL_SPEND_ADMIN_TOKEN": secret, "BANKING_MODEL_SPEND_OPERATOR": "synthetic-operator"}
	if e := Authorize(env, secret); e != nil {
		t.Fatal(e)
	}
	if Authorize(env, "wrong") == nil {
		t.Fatal("bad credential")
	}
	for _, name := range []string{"BANKING_USER_JWT_SECRET", "BANKING_TOOL_TOKEN", "BANKING_COZE_PAT", "BANKING_MODEL_GATEWAY_TOKEN", "BANKING_MODEL_SUPPLIER_API_KEY"} {
		env[name] = secret
		if Authorize(env, secret) == nil {
			t.Fatal("credential reused", name)
		}
		delete(env, name)
	}
}
func TestExpectedDatabaseAndTLSMustBeExplicit(t *testing.T) {
	if ValidateListener("production", "127.0.0.1:18160", "", "") == nil {
		t.Fatal("production HTTP listener")
	}
	if ValidateListener("unknown", "127.0.0.1:18160", "", "") == nil {
		t.Fatal("unknown environment")
	}
	if ValidateListener("test", "127.0.0.1:18160", "", "") != nil {
		t.Fatal("local test listener")
	}
	if ValidateListener("production", "0.0.0.0:8092", "cert.pem", "key.pem") != nil {
		t.Fatal("TLS configured")
	}
	if ValidateListener("test", "127.0.0.1:18160", "cert.pem", "") == nil {
		t.Fatal("partial TLS")
	}
}
