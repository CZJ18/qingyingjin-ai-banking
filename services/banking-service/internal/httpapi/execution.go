package httpapi

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/execution"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/service"
	"github.com/qingyingjin/banking-service/internal/subscription"
	"strconv"
	"strings"
)

func executionID(raw string) (string, error) {
	id, e := uuid.Parse(raw)
	if e != nil || id.String() != raw {
		return "", entity.Fail("INVALID_ARGUMENT", "canonical operation UUID required")
	}
	return raw, nil
}
func executionDenial(c *gin.Context, s *execution.Service, a *identity.Actor, permission, code string) {
	m := service.Meta{TraceID: c.GetString("trace_id"), TaskID: c.GetString("task_id")}
	if a != nil {
		m.UserID, _ = strconv.ParseInt(a.UserID, 10, 64)
	}
	var auditErr error
	if a != nil && c.Param("id") != "" {
		auditErr = s.DeniedOperation(c.Request.Context(), *a, m, permission, c.Param("id"), code)
	} else {
		auditErr = s.Denied(c.Request.Context(), a, m, permission, code)
	}
	if e := auditErr; e != nil {
		coreError(c, e)
	} else {
		status := 403
		if code == "UNAUTHENTICATED" {
			status = 401
		}
		respond(c, status, code, "execution permission denied", nil)
	}
	c.Abort()
}
func sensitiveExecutionPath(method, path string) bool {
	if strings.Contains(path, "/execution/") || method == "POST" && strings.HasSuffix(path, "/subscriptions/create_cancel_intent") {
		return true
	}
	if method == "PATCH" && strings.HasPrefix(path, "/api/v1/accounts/") && strings.HasSuffix(path, "/status") {
		return true
	}
	if method != "POST" {
		return false
	}
	switch path {
	case "/api/v1/simulation/accounts", "/api/v1/transactions/import", "/api/v1/transfers", "/api/v1/savings/goals", "/api/v1/budgets":
		return true
	}
	return strings.HasPrefix(path, "/api/v1/transfers/") && strings.HasSuffix(path, "/approvals") || strings.HasPrefix(path, "/api/v1/savings/goals/") && (strings.HasSuffix(path, "/deposits") || strings.HasSuffix(path, "/withdrawals")) || strings.HasPrefix(path, "/api/v1/subscription-intents/") && strings.HasSuffix(path, "/confirm") || strings.HasPrefix(path, "/api/v1/subscriptions/") && strings.HasSuffix(path, "/manage")
}
func agentPlanAllowed(c *gin.Context) bool {
	if c.Request.Method == "GET" {
		return true
	}
	if c.Request.Method != "POST" {
		return false
	}
	p := c.FullPath()
	if p == "/api/v1/langgraph/run" || p == "/api/v1/langgraph/plan" {
		return true
	}
	if p == "/api/v1/financial-plans" || p == "/api/v1/financial-plans/:id/replan" || p == "/api/v1/replan-events" || p == "/api/v1/goals" || p == "/api/v1/goals/:id/parse" || p == "/api/v1/orchestrations" {
		return true
	}
	switch p {
	case "/api/v1/billing/get_transactions", "/api/v1/billing/get_account_summary", "/api/v1/billing/analyze_spending", "/api/v1/billing/compare_budget", "/api/v1/billing/detect_anomalies", "/api/v1/billing/generate_billing_report", "/api/v1/billing/query",
		"/api/v1/subscriptions/detect_subscriptions", "/api/v1/subscriptions/get_subscription_details", "/api/v1/subscriptions/get_subscription_status", "/api/v1/subscriptions/analyze_subscription_cost", "/api/v1/subscriptions/suggest_subscription_actions":
		return true
	}
	return false
}
func executionGuard(s *execution.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		a := c.MustGet("actor").(identity.Actor)
		// Agent correlation belongs to the signed task, never a model-supplied header.
		if a.Kind == "agent" {
			c.Set("task_id", a.TaskID)
		}
		p := c.Request.URL.Path
		if strings.HasSuffix(p, "/subscriptions/create_cancel_intent") && c.Request.Method == "POST" {
			var in subscription.IntentInput
			if e := decode(c, &in); e != nil {
				executionInputError(c, s, a, "PREPARE", e)
				return
			}
			out, e := s.PrepareSubscription(c.Request.Context(), a, meta(c), c.GetHeader("Idempotency-Key"), in)
			status := 200
			if a.Kind != "tool" {
				status = 201
			}
			finish(c, out, e, status)
			c.Abort()
			return
		}
		if !strings.Contains(p, "/execution/") && sensitiveExecutionPath(c.Request.Method, p) {
			executionDenial(c, s, &a, "EXECUTE", "GATEWAY_REQUIRED")
			return
		}
		if a.Kind == "agent" && !strings.Contains(p, "/execution/") && !agentPlanAllowed(c) {
			executionDenial(c, s, &a, "EXECUTE", "FORBIDDEN")
			return
		}
		c.Next()
	}
}
func executionInputError(c *gin.Context, s *execution.Service, a identity.Actor, permission string, e error) {
	if auditErr := s.DeniedOperation(c.Request.Context(), a, meta(c), permission, c.Param("id"), entity.Code(e)); auditErr != nil {
		coreError(c, auditErr)
	} else {
		coreError(c, e)
	}
}
func registerExecution(user, tools *gin.RouterGroup, s *execution.Service) {
	if s == nil {
		return
	}
	ready := func(c *gin.Context) {
		if !s.Configured() {
			respond(c, 503, "DEPENDENCY_UNAVAILABLE", "execution repository unavailable", nil)
			c.Abort()
			return
		}
		c.Next()
	}
	actor := func(c *gin.Context) identity.Actor { return c.MustGet("actor").(identity.Actor) }
	human := func(c *gin.Context) {
		a := actor(c)
		if a.Kind != "user" {
			executionDenial(c, s, &a, "EXECUTE", "FORBIDDEN")
			return
		}
		c.Next()
	}
	prepare := func(status int) gin.HandlerFunc {
		return func(c *gin.Context) {
			a := actor(c)
			var in execution.PrepareInput
			if e := decode(c, &in); e != nil {
				executionInputError(c, s, a, "PREPARE", e)
				return
			}
			out, e := s.Prepare(c.Request.Context(), a, meta(c), c.GetHeader("Idempotency-Key"), in)
			finish(c, out, e, status)
		}
	}
	id := func(c *gin.Context) (string, bool) {
		v, e := executionID(c.Param("id"))
		if e != nil {
			executionInputError(c, s, actor(c), "READ", e)
			return "", false
		}
		return v, true
	}
	g := user.Group("/execution", ready)
	g.POST("/operations", prepare(201))
	g.GET("/operations", func(c *gin.Context) {
		resource, e := queryInt(c, "resource_id")
		if raw := c.Query("resource_id"); raw != "" && (resource <= 0 || strconv.FormatInt(resource, 10) != raw) {
			e = entity.Fail("INVALID_ARGUMENT", "positive canonical resource ID required")
		}
		if e != nil {
			executionInputError(c, s, actor(c), "READ", e)
			return
		}
		out, e := s.List(c.Request.Context(), actor(c), meta(c), c.Query("kind"), resource)
		finish(c, out, e, 200)
	})
	g.GET("/operations/:id", func(c *gin.Context) {
		v, ok := id(c)
		if !ok {
			return
		}
		out, e := s.Get(c.Request.Context(), actor(c), meta(c), v)
		finish(c, out, e, 200)
	})
	g.POST("/operations/:id/authorize", human, func(c *gin.Context) {
		v, ok := id(c)
		if !ok {
			return
		}
		var in execution.AuthorizeInput
		if e := decode(c, &in); e != nil {
			executionInputError(c, s, actor(c), "EXECUTE", e)
			return
		}
		out, e := s.Authorize(c.Request.Context(), actor(c), meta(c), v, in)
		finish(c, out, e, 200)
	})
	g.POST("/operations/:id/execute", human, func(c *gin.Context) {
		v, ok := id(c)
		if !ok {
			return
		}
		var in execution.ExecuteInput
		if e := decode(c, &in); e != nil {
			executionInputError(c, s, actor(c), "EXECUTE", e)
			return
		}
		out, e := s.Execute(c.Request.Context(), actor(c), meta(c), v, c.GetHeader("Idempotency-Key"), in)
		finish(c, out, e, 200)
	})
	g.GET("/operations/:id/audits", func(c *gin.Context) {
		v, ok := id(c)
		if !ok {
			return
		}
		out, e := s.Audits(c.Request.Context(), actor(c), meta(c), v)
		finish(c, out, e, 200)
	})
	g.GET("/risk-rules", func(c *gin.Context) {
		out, e := s.Rules(c.Request.Context(), actor(c), meta(c))
		finish(c, out, e, 200)
	})
	t := tools.Group("/execution", ready)
	t.POST("/prepare_operation", prepare(200))
	t.POST("/get_operation", func(c *gin.Context) {
		var in struct {
			ID string `json:"operation_id"`
		}
		if e := decode(c, &in); e != nil {
			executionInputError(c, s, actor(c), "READ", e)
			return
		}
		v, e := executionID(in.ID)
		if e != nil {
			executionInputError(c, s, actor(c), "READ", e)
			return
		}
		out, e := s.Get(c.Request.Context(), actor(c), meta(c), v)
		finish(c, out, e, 200)
	})
}
