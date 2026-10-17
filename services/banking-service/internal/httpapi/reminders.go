package httpapi

import (
	"github.com/gin-gonic/gin"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/service"
)

func registerRenewals(user *gin.RouterGroup, core *service.Core) {
	group := user.Group("/notifications/renewals", func(c *gin.Context) {
		if c.MustGet("actor").(identity.Actor).Kind != "user" {
			respond(c, 403, "FORBIDDEN", "renewal notifications belong to the user", nil)
			c.Abort()
			return
		}
		if !core.Configured() {
			respond(c, 503, "DEPENDENCY_UNAVAILABLE", "notification database unavailable", nil)
			c.Abort()
			return
		}
		c.Next()
	})
	group.GET("", func(c *gin.Context) { v, e := core.Renewals(c.Request.Context(), meta(c).UserID); finish(c, v, e, 200) })
	group.PUT("/settings", func(c *gin.Context) {
		var in struct {
			Enabled  *bool `json:"enabled"`
			LeadDays *int  `json:"lead_days"`
			Expected int64 `json:"expected_version"`
		}
		if e := decode(c, &in); e != nil {
			coreError(c, e)
			return
		}
		if in.Enabled == nil || in.LeadDays == nil {
			respond(c, 400, "INVALID_ARGUMENT", "enabled, lead_days and expected_version required", nil)
			return
		}
		v, e := core.RenewalSettings(c.Request.Context(), meta(c).UserID, *in.Enabled, *in.LeadDays, in.Expected)
		finish(c, v, e, 200)
	})
	group.POST("/:id/read", func(c *gin.Context) {
		var in struct {
			Expected int64 `json:"expected_version"`
		}
		if e := decode(c, &in); e != nil {
			coreError(c, e)
			return
		}
		v, e := core.ReadRenewal(c.Request.Context(), meta(c).UserID, c.Param("id"), in.Expected)
		finish(c, v, e, 200)
	})
}
