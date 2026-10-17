package httpapi

import (
	"github.com/gin-gonic/gin"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/goal"
)

func registerGoals(user *gin.RouterGroup, s *goal.Service) {
	group := user.Group("", func(c *gin.Context) {
		if !s.Configured() {
			respond(c, 503, "DEPENDENCY_UNAVAILABLE", "goal compiler database is not configured", nil)
			c.Abort()
			return
		}
		c.Next()
	})
	group.POST("/goals", func(c *gin.Context) {
		input(c, func(in goal.Input) (any, error) {
			return s.Create(c.Request.Context(), meta(c), c.GetHeader("Idempotency-Key"), in)
		}, 201)
	})
	group.GET("/goals/:id", func(c *gin.Context) {
		id, e := pathID(c)
		if e != nil {
			coreError(c, e)
			return
		}
		v, e := queryInt(c, "version")
		if e != nil {
			coreError(c, e)
			return
		}
		out, e := s.View(c.Request.Context(), meta(c).UserID, id, v)
		finish(c, out, e, 200)
	})
	group.PUT("/goals/:id", func(c *gin.Context) {
		id, e := pathID(c)
		if e != nil {
			coreError(c, e)
			return
		}
		input(c, func(in goal.Input) (any, error) { return s.Update(c.Request.Context(), meta(c), id, in) }, 200)
	})
	group.POST("/goals/:id/parse", func(c *gin.Context) {
		id, e := pathID(c)
		if e != nil {
			coreError(c, e)
			return
		}
		input(c, func(in struct {
			ExpectedVersion int64 `json:"expected_version"`
		}) (any, error) {
			return s.Parse(c.Request.Context(), meta(c), id, in.ExpectedVersion)
		}, 200)
	})
	group.GET("/goals/:id/plan", func(c *gin.Context) {
		id, e := pathID(c)
		if e != nil {
			coreError(c, e)
			return
		}
		v, e := queryInt(c, "version")
		if e != nil {
			coreError(c, e)
			return
		}
		out, e := s.View(c.Request.Context(), meta(c).UserID, id, v)
		if e == nil && out.Plan == nil {
			e = entity.Fail("NOT_FOUND", "goal version has no compiled task plan")
		}
		finish(c, out.Plan, e, 200)
	})
	group.GET("/goals/:id/versions", func(c *gin.Context) {
		id, e := pathID(c)
		if e != nil {
			coreError(c, e)
			return
		}
		o, e := paging(c)
		if e != nil {
			coreError(c, e)
			return
		}
		limit := o.Limit
		if limit < 1 {
			limit = 20
		}
		if limit > 100 {
			limit = 100
		}
		out, e := s.Versions(c.Request.Context(), meta(c).UserID, id, limit, o.Offset)
		finish(c, out, e, 200)
	})
	group.GET("/goals/:id/compare", func(c *gin.Context) {
		id, e := pathID(c)
		if e != nil {
			coreError(c, e)
			return
		}
		from, e := queryInt(c, "from")
		if e != nil {
			coreError(c, e)
			return
		}
		to, e := queryInt(c, "to")
		if e != nil {
			coreError(c, e)
			return
		}
		if from < 1 || to < 1 {
			coreError(c, entity.Fail("INVALID_ARGUMENT", "positive from and to versions required"))
			return
		}
		out, e := s.Compare(c.Request.Context(), meta(c).UserID, id, from, to)
		finish(c, out, e, 200)
	})
}
