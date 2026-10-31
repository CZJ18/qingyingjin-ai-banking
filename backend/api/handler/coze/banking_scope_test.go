package coze

import (
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/coze-dev/coze-studio/backend/pkg/bankingscope"
	"testing"
)

func TestBankingHeadersAreConsumedWithoutChangingBody(t *testing.T) {
	c := app.NewContext(0)
	c.Request.Header.Set(bankingscope.CapabilityHeader, "bgc1.opaque")
	c.Request.Header.Set(bankingscope.DigestHeader, "pinned")
	c.Request.SetBodyString(`{"parameters":"{\"user_id\":\"untrusted\"}"}`)
	before := string(c.Request.Body())
	cap, digest, scoped := takeBankingScopeHeaders(c)
	if !scoped || cap != "bgc1.opaque" || digest != "pinned" {
		t.Fatal("lost scoped headers")
	}
	if len(c.GetHeader(bankingscope.CapabilityHeader)) != 0 || len(c.GetHeader(bankingscope.DigestHeader)) != 0 {
		t.Fatal("credential retained in request")
	}
	if string(c.Request.Body()) != before {
		t.Fatal("credential inserted into parameters")
	}
}

func TestEmptyBankingHeaderStillRequiresScopeAdmission(t *testing.T) {
	c := app.NewContext(0)
	c.Request.Header.Set(bankingscope.CapabilityHeader, "")
	_, _, scoped := takeBankingScopeHeaders(c)
	if !scoped {
		t.Fatal("empty scoped header could use direct provider path")
	}
}

func TestDedicatedRuntimeRequiresScopeEvenWithoutHeaders(t *testing.T) {
	for _, mode := range []string{"invalid", " ", `{"42":"pinned"}`} {
		t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", mode)
		c := app.NewContext(0)
		capability, digest, required := takeBankingScopeHeaders(c)
		if !required || capability != "" || digest != "" {
			t.Fatal("unscoped Banking workflow could reach normal dispatch")
		}
	}
	t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", "")
	_, _, required := takeBankingScopeHeaders(app.NewContext(0))
	if required {
		t.Fatal("general runtime scope requirement changed")
	}
}
