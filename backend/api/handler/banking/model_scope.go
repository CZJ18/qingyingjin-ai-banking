package banking

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/coze-dev/coze-studio/backend/pkg/bankingscope"
)

// ModelScope describes only the patched runtime's protected reviewed-mode config.
// This metadata is not a grant and never bypasses the gateway or graph checks.
func ModelScope(_ context.Context, c *app.RequestContext) {
	c.Header("Cache-Control", "no-store")
	view, err := bankingscope.ConfigView()
	if err != nil {
		c.JSON(503, map[string]string{"error": "banking task scope unavailable"})
		return
	}
	c.JSON(200, view)
}
