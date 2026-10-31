package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/qingyingjin/banking-service/internal/entity"
)

func TestProjectMonetaryQuotaIs429(t *testing.T) {
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	coreError(ctx, entity.Fail("MODEL_SPEND_QUOTA_EXCEEDED", "project monetary allowance exhausted"))
	if recorder.Code != 429 || !strings.Contains(recorder.Body.String(), `"code":"MODEL_SPEND_QUOTA_EXCEEDED"`) {
		t.Fatalf("project budget lost status/code: %d %s", recorder.Code, recorder.Body.String())
	}
}

func TestApprovalFailureCodesKeepTheirStatus(t *testing.T) {
	for code, status := range map[string]int{"MODEL_APPROVAL_REQUIRED": 403, "MODEL_APPROVAL_LIMIT_EXCEEDED": 429, "MODEL_EXECUTION_CONFIRMATION_REQUIRED": 503} {
		recorder := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(recorder)
		coreError(ctx, entity.Fail(code, "approval admission refused"))
		if recorder.Code != status || !strings.Contains(recorder.Body.String(), code) {
			t.Fatal(recorder.Code, recorder.Body.String())
		}
	}
}
