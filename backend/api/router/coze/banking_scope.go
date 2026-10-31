package coze

import (
	"github.com/cloudwego/hertz/pkg/route"
	"github.com/coze-dev/coze-studio/backend/api/handler/banking"
)

// Called by the actual generated router registration. Global OpenAPI middleware
// authenticates this exact path using the same PAT mechanism as workflow/run.
func registerBankingModelScope(root *route.RouterGroup) {
	root.GET("/v1/banking/model-scope", banking.ModelScope)
}
