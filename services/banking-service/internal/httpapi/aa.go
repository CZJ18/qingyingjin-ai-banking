package httpapi

import (
	"github.com/gin-gonic/gin"
	"github.com/qingyingjin/banking-service/internal/aa"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/service"
	"strconv"
)

func registerAA(user *gin.RouterGroup, core *service.Core) {
	g := user.Group("/aa", func(c *gin.Context) {
		if c.MustGet("actor").(identity.Actor).Kind != "user" {
			respond(c, 403, "FORBIDDEN", "AA requires a human user", nil)
			c.Abort()
			return
		}
		if !core.Configured() {
			respond(c, 503, "DEPENDENCY_UNAVAILABLE", "AA database unavailable", nil)
			c.Abort()
			return
		}
		c.Next()
	})
	g.GET("/bills", func(c *gin.Context) {
		limit, e := strconv.Atoi(c.DefaultQuery("limit", "50"))
		offset, e2 := strconv.Atoi(c.DefaultQuery("offset", "0"))
		if e != nil || e2 != nil || limit < 1 || limit > 50 || offset < 0 || offset > 150 {
			respond(c, 400, "INVALID_ARGUMENT", "limit 1..50 and offset 0..150 required", nil)
			return
		}
		v, e := core.AA().List(c.Request.Context(), meta(c).UserID, limit, offset)
		finish(c, v, e, 200)
	})
	g.POST("/bills", func(c *gin.Context) {
		var in aa.Input
		if e := decode(c, &in); e != nil {
			coreError(c, e)
			return
		}
		v, e := core.AA().Create(c.Request.Context(), meta(c).UserID, c.GetHeader("Idempotency-Key"), in)
		finish(c, v, e, 201)
	})
	g.GET("/bills/:owner/:id", func(c *gin.Context) {
		raw := c.Param("owner")
		owner, e := strconv.ParseInt(raw, 10, 64)
		if e != nil || owner <= 0 || strconv.FormatInt(owner, 10) != raw {
			respond(c, 400, "INVALID_ARGUMENT", "canonical owner ID required", nil)
			return
		}
		v, e := core.AA().Get(c.Request.Context(), meta(c).UserID, owner, c.Param("id"))
		finish(c, v, e, 200)
	})
	g.POST("/invitations/preview", func(c *gin.Context) {
		var in struct {
			Invitation string `json:"invitation"`
		}
		if e := decode(c, &in); e != nil {
			coreError(c, e)
			return
		}
		v, e := core.AA().Preview(c.Request.Context(), meta(c).UserID, in.Invitation)
		finish(c, v, e, 200)
	})
	g.POST("/join", func(c *gin.Context) {
		var in struct {
			Invitation    string `json:"invitation"`
			ParticipantID string `json:"participant_id"`
		}
		if e := decode(c, &in); e != nil {
			coreError(c, e)
			return
		}
		v, e := core.AA().Join(c.Request.Context(), meta(c).UserID, in.Invitation, in.ParticipantID)
		finish(c, v, e, 200)
	})
	g.POST("/bills/:id/cancel", func(c *gin.Context) {
		var in struct {
			ExpectedVersion int64 `json:"expected_version"`
		}
		if e := decode(c, &in); e != nil {
			coreError(c, e)
			return
		}
		if in.ExpectedVersion < 1 {
			respond(c, 400, "INVALID_ARGUMENT", "expected_version required", nil)
			return
		}
		v, e := core.AA().Cancel(c.Request.Context(), meta(c).UserID, c.Param("id"), in.ExpectedVersion)
		finish(c, v, e, 200)
	})
	for _, action := range []string{"review", "confirm"} {
		action := action
		g.POST("/wechat/:owner/:id/"+action, func(c *gin.Context) {
			raw := c.Param("owner")
			owner, e := strconv.ParseInt(raw, 10, 64)
			if e != nil || owner <= 0 || strconv.FormatInt(owner, 10) != raw {
				respond(c, 400, "INVALID_ARGUMENT", "canonical owner ID required", nil)
				return
			}
			if action == "review" {
				var in aa.ReviewInput
				if e := decode(c, &in); e != nil {
					coreError(c, e)
					return
				}
				v, e := core.AA().PrepareManual(c.Request.Context(), meta(c).UserID, owner, c.Param("id"), in, meta(c).TraceID)
				finish(c, v, e, 200)
			} else {
				var in aa.ConfirmInput
				if e := decode(c, &in); e != nil {
					coreError(c, e)
					return
				}
				v, e := core.AA().ConfirmManual(c.Request.Context(), meta(c).UserID, owner, c.Param("id"), in, meta(c).TraceID)
				finish(c, v, e, 200)
			}
		})
	}
}
