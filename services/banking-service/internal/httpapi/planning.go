package httpapi

import (
	"github.com/gin-gonic/gin"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/planning"
	"strconv"
)

func registerPlanning(user, tools *gin.RouterGroup, s *planning.Service) {
	ready := func(c *gin.Context) {
		if !s.Configured() {
			respond(c, 503, "DEPENDENCY_UNAVAILABLE", "planning repository unavailable", nil)
			c.Abort()
			return
		}
		c.Next()
	}
	g := user.Group("/financial-plans", ready)
	g.POST("", func(c *gin.Context) {
		input(c, func(in planning.CreateInput) (any, error) { return s.Create(c.Request.Context(), meta(c), in) }, 201)
	})
	g.GET("/:id", func(c *gin.Context) {
		id, e := pathID(c)
		if e != nil {
			coreError(c, e)
			return
		}
		out, e := s.Get(c.Request.Context(), meta(c).UserID, id)
		finish(c, out, e, 200)
	})
	g.POST("/:id/replan", func(c *gin.Context) {
		id, e := pathID(c)
		if e != nil {
			coreError(c, e)
			return
		}
		out, e := s.Replan(c.Request.Context(), meta(c), id)
		finish(c, out, e, 200)
	})
	g.GET("/:id/versions", func(c *gin.Context) {
		id, e := pathID(c)
		if e != nil {
			coreError(c, e)
			return
		}
		limit, e := strconv.Atoi(c.DefaultQuery("limit", "100"))
		if e != nil {
			coreError(c, entity.Fail("INVALID_ARGUMENT", "invalid limit"))
			return
		}
		offset, e := strconv.Atoi(c.DefaultQuery("offset", "0"))
		if e != nil {
			coreError(c, entity.Fail("INVALID_ARGUMENT", "invalid offset"))
			return
		}
		out, e := s.Versions(c.Request.Context(), meta(c).UserID, id, limit, offset)
		finish(c, out, e, 200)
	})
	g.PUT("/:id/settings", func(c *gin.Context) {
		id, e := pathID(c)
		if e != nil {
			coreError(c, e)
			return
		}
		input(c, func(in planning.UpdateInput) (any, error) { return s.Update(c.Request.Context(), meta(c), id, in) }, 200)
	})
	g.POST("/:id/decisions", func(c *gin.Context) {
		id, e := pathID(c)
		if e != nil {
			coreError(c, e)
			return
		}
		input(c, func(in planning.DecisionInput) (any, error) { return s.Decide(c.Request.Context(), meta(c), id, in) }, 200)
	})
	user.POST("/replan-events", ready, func(c *gin.Context) {
		input(c, func(in planning.EventInput) (any, error) {
			return s.Publish(c.Request.Context(), meta(c), c.GetHeader("Idempotency-Key"), in)
		}, 201)
	})
	user.PUT("/savings/goals/:id/settings", ready, func(c *gin.Context) {
		id, e := pathID(c)
		if e != nil {
			coreError(c, e)
			return
		}
		input(c, func(in planning.GoalUpdateInput) (any, error) {
			return s.UpdateGoal(c.Request.Context(), meta(c), id, in)
		}, 200)
	})
	for _, name := range []string{"get_financial_plan", "compare_financial_plans", "replan_financial_plan"} {
		tools.POST("/savings/"+name, ready, func(c *gin.Context) {
			input(c, func(in planning.IDInput) (any, error) {
				id, e := planning.ID(in.PlanID)
				if e != nil {
					return nil, e
				}
				if name == "replan_financial_plan" {
					return s.Replan(c.Request.Context(), meta(c), id)
				}
				return s.Get(c.Request.Context(), meta(c).UserID, id)
			}, 200)
		})
	}
}
