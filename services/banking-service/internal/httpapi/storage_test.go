package httpapi

import (
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgconn"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFrozenStorageIsUnavailableWithoutDatabaseDetail(t *testing.T) {
	router := gin.New()
	router.GET("/test", func(c *gin.Context) {
		coreError(c, &pgconn.PgError{Code: "55000", Message: "BANKING_STORAGE_FROZEN", Detail: "private financial database detail"})
	})
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest("GET", "/test", nil))
	if w.Code != 503 || !strings.Contains(w.Body.String(), `"code":"STORAGE_FROZEN"`) || strings.Contains(w.Body.String(), "private financial") {
		t.Fatalf("unsafe migration response status=%d body=%s", w.Code, w.Body.String())
	}
}
