package modelgateway

import (
	"bytes"
	"testing"
)

func TestRequestRejectsInvalidUTF8BeforeSupplierDispatch(t *testing.T) {
	// encoding/json replaces malformed bytes with U+FFFD, but the gateway
	// forwards the caller's original bytes. Both sides must inspect valid text.
	for _, invalid := range [][]byte{{0xff}, {0xc0, 0xaf}, {0xe2, 0x82}, {0xed, 0xa0, 0x80}} {
		raw := bytes.Replace([]byte(validBody), []byte("hello"), invalid, 1)
		if _, err := parseRequest(raw, policy()); err == nil {
			t.Errorf("accepted malformed UTF-8 content %x", invalid)
		}
	}
	p := policy()
	p.ContextTokens = 512 // UTF-8 validation is independent of the tiny admission fixture.
	for _, valid := range []string{"农业🌾", "\uFFFD"} {
		raw := bytes.Replace([]byte(validBody), []byte("hello"), []byte(valid), 1)
		if _, err := parseRequest(raw, p); err != nil {
			t.Errorf("rejected valid UTF-8 content: %v", err)
		}
	}
}
