package httpapi

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/coze"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/lifestyle"
	"github.com/qingyingjin/banking-service/internal/skills"
	"strconv"
)

func invalidLifestyleID() error {
	return entity.Fail("INVALID_ARGUMENT", "bind exactly one positive goal_id or plan_id")
}
func lifestylePage(c *gin.Context) (int, int, error) {
	limit, e := strconv.Atoi(c.DefaultQuery("limit", "100"))
	if e != nil {
		return 0, 0, entity.Fail("INVALID_ARGUMENT", "invalid limit")
	}
	offset, e := strconv.Atoi(c.DefaultQuery("offset", "0"))
	if e != nil {
		return 0, 0, entity.Fail("INVALID_ARGUMENT", "invalid offset")
	}
	return limit, offset, nil
}
func registerLifestyle(user, tools *gin.RouterGroup, s *lifestyle.Service) {
	ready := func(c *gin.Context) {
		if !s.Configured() {
			respond(c, 503, "DEPENDENCY_UNAVAILABLE", "lifestyle repository unavailable", nil)
			c.Abort()
			return
		}
		c.Next()
	}
	guard := func(c *gin.Context) {
		a := c.MustGet("actor").(identity.Actor)
		if a.Kind == "agent" {
			c.Set("task_id", a.TaskID)
			path := c.FullPath()
			tool := ""
			if c.Request.Method == "GET" && path == "/api/v2/lifestyle-goals/:id" {
				tool = "get_lifestyle_goal"
			}
			if c.Request.Method == "GET" && path == "/api/v2/lifestyle-plans/:id" {
				tool = "get_lifestyle_plan"
			}
			if c.Request.Method == "POST" && path == "/api/v2/lifestyle-goals/:id/compile" {
				tool = "compile_lifestyle_plan"
			}
			binding, e := skills.Resolve(skills.Request{Agent: a.AgentType, Tool: tool})
			if e != nil {
				respond(c, 403, "FORBIDDEN", "lifestyle tools only; users own goals and decisions", nil)
				c.Abort()
				return
			}
			c.Set("skill_binding", binding)
		}
		c.Next()
	}
	g := user.Group("", guard, ready)
	g.POST("/lifestyle-goals/parse", func(c *gin.Context) {
		input(c, func(in lifestyle.ParseInput) (any, error) {
			return s.Parse(coze.WithIdentity(c.Request.Context(), meta(c).UserID, "lifestyle-parse:"+uuid.NewString()), in, c.GetString("trace_id"))
		}, 200)
	})
	g.POST("/lifestyle-goals", func(c *gin.Context) {
		input(c, func(in lifestyle.GoalInput) (any, error) {
			return s.SaveGoal(c.Request.Context(), meta(c), c.GetHeader("Idempotency-Key"), 0, in)
		}, 201)
	})
	g.GET("/lifestyle-goals/:id", func(c *gin.Context) {
		id, e := pathID(c)
		if e != nil {
			coreError(c, e)
			return
		}
		limit, offset, e := lifestylePage(c)
		if e != nil {
			coreError(c, e)
			return
		}
		out, e := s.Goal(c.Request.Context(), meta(c).UserID, id, limit, offset)
		finish(c, out, e, 200)
	})
	g.PUT("/lifestyle-goals/:id", func(c *gin.Context) {
		id, e := pathID(c)
		if e != nil {
			coreError(c, e)
			return
		}
		input(c, func(in lifestyle.GoalInput) (any, error) {
			return s.SaveGoal(c.Request.Context(), meta(c), c.GetHeader("Idempotency-Key"), id, in)
		}, 200)
	})
	g.POST("/lifestyle-goals/:id/compile", func(c *gin.Context) {
		id, e := pathID(c)
		if e != nil {
			coreError(c, e)
			return
		}
		input(c, func(in lifestyle.CompileInput) (any, error) {
			return s.Compile(c.Request.Context(), meta(c), c.GetHeader("Idempotency-Key"), id, in)
		}, 201)
	})
	g.GET("/lifestyle-goals/:id/plans", func(c *gin.Context) {
		id, e := pathID(c)
		if e != nil {
			coreError(c, e)
			return
		}
		limit, offset, e := lifestylePage(c)
		if e != nil {
			coreError(c, e)
			return
		}
		out, e := s.Plans(c.Request.Context(), meta(c).UserID, id, limit, offset)
		finish(c, out, e, 200)
	})
	g.GET("/lifestyle-plans/:id", func(c *gin.Context) {
		id, e := pathID(c)
		if e != nil {
			coreError(c, e)
			return
		}
		out, e := s.Plan(c.Request.Context(), meta(c).UserID, id)
		finish(c, out, e, 200)
	})
	g.POST("/recommendations/:id/decisions", func(c *gin.Context) {
		id, e := pathID(c)
		if e != nil {
			coreError(c, e)
			return
		}
		input(c, func(in lifestyle.DecisionInput) (any, error) {
			return s.Decide(c.Request.Context(), meta(c), c.GetHeader("Idempotency-Key"), id, in)
		}, 200)
	})
	g.GET("/recommendations/:id/decisions", func(c *gin.Context) {
		id, e := pathID(c)
		if e != nil {
			coreError(c, e)
			return
		}
		out, e := s.Decisions(c.Request.Context(), meta(c).UserID, id)
		finish(c, out, e, 200)
	})
	for _, name := range []string{"get_lifestyle_goal", "get_lifestyle_plan"} {
		tools.POST("/lifestyle/"+name, ready, func(c *gin.Context) {
			input(c, func(in lifestyle.IDInput) (any, error) {
				if name == "get_lifestyle_goal" {
					if in.GoalID <= 0 || in.PlanID != 0 {
						return nil, invalidLifestyleID()
					}
					return s.Goal(c.Request.Context(), meta(c).UserID, in.GoalID)
				}
				if in.PlanID <= 0 || in.GoalID != 0 {
					return nil, invalidLifestyleID()
				}
				return s.Plan(c.Request.Context(), meta(c).UserID, in.PlanID)
			}, 200)
		})
	}
	tools.POST("/lifestyle/compile_lifestyle_plan", ready, func(c *gin.Context) {
		input(c, func(in lifestyle.CompileToolInput) (any, error) {
			return s.Compile(c.Request.Context(), meta(c), c.GetHeader("Idempotency-Key"), in.GoalID, in.CompileInput)
		}, 200)
	})
}
