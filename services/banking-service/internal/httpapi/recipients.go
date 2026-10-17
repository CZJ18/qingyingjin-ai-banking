package httpapi

import (
	"github.com/gin-gonic/gin"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/recipients"
	"github.com/qingyingjin/banking-service/internal/service"
	"github.com/qingyingjin/banking-service/internal/userauth"
)

func registerRecipients(user *gin.RouterGroup, core *service.Core, auth *userauth.Service) {
	g := user.Group("/recipients", func(c *gin.Context) {
		if c.MustGet("actor").(identity.Actor).Kind != "user" {
			respond(c, 403, "FORBIDDEN", "recipient directory requires a human user", nil)
			c.Abort()
			return
		}
		if !core.Configured() {
			respond(c, 503, "DEPENDENCY_UNAVAILABLE", "recipient database unavailable", nil)
			c.Abort()
			return
		}
		c.Next()
	})
	g.GET("/settings", func(c *gin.Context) {
		v, e := core.Recipients().Settings(c.Request.Context(), meta(c).UserID)
		finish(c, v, e, 200)
	})
	g.PUT("/settings", func(c *gin.Context) {
		var in struct {
			Enabled         *bool  `json:"enabled"`
			AccountID       *int64 `json:"account_id,string"`
			ExpectedVersion int64  `json:"expected_version"`
		}
		if e := decode(c, &in); e != nil {
			coreError(c, e)
			return
		}
		if in.Enabled == nil || in.AccountID == nil {
			respond(c, 400, "INVALID_ARGUMENT", "enabled and account_id required", nil)
			return
		}
		v, e := core.Recipients().Configure(c.Request.Context(), meta(c).UserID, recipients.SettingsInput{Enabled: *in.Enabled, AccountID: *in.AccountID, ExpectedVersion: in.ExpectedVersion})
		finish(c, v, e, 200)
	})
	g.POST("/search", func(c *gin.Context) {
		var in recipients.SearchInput
		if e := decode(c, &in); e != nil {
			coreError(c, e)
			return
		}
		actor := c.MustGet("actor").(identity.Actor)
		if e := auth.AllowRecipientLookup(c.Request.Context(), actor.UserID, c.ClientIP()); e != nil {
			coreError(c, e)
			return
		}
		v, e := core.Recipients().Search(c.Request.Context(), meta(c).UserID, in)
		finish(c, v, e, 200)
	})
}
