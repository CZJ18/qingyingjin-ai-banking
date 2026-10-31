package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/service"
)

func TestBirthdaysRequireHumanBeforeDatabaseAccess(t *testing.T) {
	for _, kind := range []string{"agent", "tool", "user"} {
		for _, method := range []string{"GET", "PUT"} {
			r := gin.New()
			group := r.Group("/api/v1", func(c *gin.Context) { c.Set("actor", identity.Actor{Kind: kind, UserID: "1"}) })
			registerBirthdays(group, &service.Core{})
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(method, "/api/v1/notifications/birthdays", strings.NewReader(`{}`)))
			want := 403
			if kind == "user" {
				want = 503
			}
			if w.Code != want {
				t.Fatalf("%s %s: got %d want %d", kind, method, w.Code, want)
			}
		}
	}
}
