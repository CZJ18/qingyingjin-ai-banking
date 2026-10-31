package httpapi

import (
	"github.com/gin-gonic/gin"
	"github.com/qingyingjin/banking-service/internal/birthdays"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/service"
)

func registerBirthdays(user *gin.RouterGroup, core *service.Core) {
	g := user.Group("/notifications/birthdays", func(c *gin.Context) {
		if c.MustGet("actor").(identity.Actor).Kind != "user" {
			respond(c, 403, "FORBIDDEN", "birthday calendar requires the user", nil)
			c.Abort()
			return
		}
		if !core.Configured() {
			respond(c, 503, "DEPENDENCY_UNAVAILABLE", "calendar database unavailable", nil)
			c.Abort()
			return
		}
		c.Next()
	})
	g.GET("", func(c *gin.Context) {
		v, e := core.Birthdays(c.Request.Context(), meta(c).UserID)
		finish(c, v, e, 200)
	})
	g.PUT("", func(c *gin.Context) {
		var in birthdays.Input
		if e := decode(c, &in); e != nil {
			coreError(c, e)
			return
		}
		v, e := core.BirthdaySettings(c.Request.Context(), meta(c).UserID, in)
		finish(c, v, e, 200)
	})
}
