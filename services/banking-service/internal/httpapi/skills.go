package httpapi

import (
	"github.com/gin-gonic/gin"
	"github.com/qingyingjin/banking-service/internal/execution"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/skills"
	"strings"
)

// skillGuard is enforced even when the execution gateway is not configured.
// Professional route names are derived by the server, never from model input.
func skillGuard(gateway *execution.Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		actor := c.MustGet("actor").(identity.Actor)
		if actor.Kind != "agent" {
			c.Next()
			return
		}
		c.Set("task_id", actor.TaskID)
		path := c.FullPath()
		tool := ""
		professional := strings.HasPrefix(path, "/api/v1/billing/") || strings.HasPrefix(path, "/api/v1/subscriptions/")
		if professional && c.Request.Method == "POST" {
			tool = path[strings.LastIndex(path, "/")+1:]
		}
		if path == "/api/v1/billing/query" || path == "/api/v1/billing/reports/:id" {
			tool = "generate_billing_report"
			professional = true
		}
		if path == "/api/v1/subscriptions" && c.Request.Method == "GET" {
			tool = "analyze_subscription_cost"
			professional = true
		}
		if strings.HasPrefix(path, "/api/v1/subscriptions/:id") && c.Request.Method == "GET" {
			tool = "get_subscription_details"
			professional = true
		}
		if path == "/api/v1/financial-plans/:id" && c.Request.Method == "GET" {
			tool = "get_financial_plan"
			professional = true
		}
		if path == "/api/v1/financial-plans/:id/replan" && c.Request.Method == "POST" {
			tool = "replan_financial_plan"
			professional = true
		}
		if professional && tool != "" {
			b, err := skills.Resolve(skills.Request{Agent: actor.AgentType, Tool: tool})
			if err != nil {
				if gateway != nil && sensitiveExecutionPath(c.Request.Method, c.Request.URL.Path) {
					executionDenial(c, gateway, &actor, "PREPARE", "FORBIDDEN")
					return
				}
				respond(c, 403, "FORBIDDEN", "tool is not allowed for signed specialist", nil)
				c.Abort()
				return
			}
			c.Set("skill_binding", b)
		}
		// Missing gateway configuration cannot restore legacy write privileges.
		if !strings.Contains(path, "/execution/") && c.Request.Method != "GET" && !agentPlanAllowed(c) {
			if gateway != nil && gateway.Configured() && strings.HasSuffix(path, "/subscriptions/create_cancel_intent") {
				c.Next()
				return
			}
			respond(c, 403, "FORBIDDEN", "specialist cannot access this mutation", nil)
			c.Abort()
			return
		}
		c.Next()
	}
}
func registerSkills(user *gin.RouterGroup) {
	user.GET("/skills", func(c *gin.Context) {
		actor := c.MustGet("actor").(identity.Actor)
		catalog := []skills.Definition{}
		for _, d := range skills.Catalog() {
			if actor.Kind != "agent" {
				catalog = append(catalog, d)
				continue
			}
			pairs := []skills.Pair{}
			for _, p := range d.AllowedPairs {
				if p.Agent == actor.AgentType {
					pairs = append(pairs, p)
				}
			}
			if len(pairs) > 0 {
				d.AllowedPairs = pairs
				d.InternalEntry = ""
				catalog = append(catalog, d)
			}
		}
		respond(c, 200, "OK", "registered skills", catalog)
	})
}
