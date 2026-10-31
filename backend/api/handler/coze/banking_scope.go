package coze

import (
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/coze-dev/coze-studio/backend/pkg/bankingscope"
	"strings"
)

func takeBankingScopeHeaders(c *app.RequestContext) (capability, digest string, scoped bool) {
	// The actual workflow handlers must require admission even when a caller
	// omits both headers on a dedicated Banking instance.
	scoped = bankingscope.DedicatedMode()
	c.Request.Header.VisitAll(func(k, v []byte) {
		switch {
		case strings.EqualFold(string(k), bankingscope.CapabilityHeader):
			capability = string(v)
			scoped = true
		case strings.EqualFold(string(k), bankingscope.DigestHeader):
			digest = string(v)
			scoped = true
		}
	})
	c.Request.Header.Del(bankingscope.CapabilityHeader)
	c.Request.Header.Del(bankingscope.DigestHeader)
	return
}
