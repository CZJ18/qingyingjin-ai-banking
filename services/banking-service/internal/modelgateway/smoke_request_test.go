package modelgateway

import (
	"strings"
	"testing"
)

func TestExplicitNonThinkingSmokeProtocolRemainsBounded(t *testing.T) {
	body := strings.Replace(validBody, `"max_tokens":8`, `"max_tokens":8,"thinking":{"type":"disabled"}`, 1)
	if _, e := parseRequest([]byte(body), policy()); e != nil {
		t.Fatal("official non-thinking request refused", e)
	}
	for _, bad := range []string{strings.Replace(body, `"disabled"`, `"enabled"`, 1), strings.Replace(body, `"disabled"`, `false`, 1), strings.Replace(body, `"type":"disabled"`, `"type":"disabled","unreviewed":1`, 1)} {
		if _, e := parseRequest([]byte(bad), policy()); e == nil {
			t.Fatal("unreviewed thinking control accepted")
		}
	}
}
