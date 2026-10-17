package httpapi

import (
	"context"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/api"
	"github.com/qingyingjin/banking-service/internal/billing"
	"github.com/qingyingjin/banking-service/internal/execution"
	"github.com/qingyingjin/banking-service/internal/goal"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/lifestyle"
	"github.com/qingyingjin/banking-service/internal/orchestrator"
	"github.com/qingyingjin/banking-service/internal/planning"
	"github.com/qingyingjin/banking-service/internal/service"
	"github.com/qingyingjin/banking-service/internal/subscription"
	"github.com/qingyingjin/banking-service/internal/userauth"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Checker func(context.Context) error

var correlationPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)

func New(auth *identity.Auth, log *slog.Logger, checks map[string]Checker, timeout time.Duration, cores ...*service.Core) *gin.Engine {
	var core *service.Core
	if len(cores) > 0 {
		core = cores[0]
	}
	return NewWithGoals(auth, log, checks, timeout, core, nil)
}

func NewWithGoals(auth *identity.Auth, log *slog.Logger, checks map[string]Checker, timeout time.Duration, core *service.Core, goals *goal.Service, billings ...*billing.Service) *gin.Engine {
	var bills *billing.Service
	if len(billings) > 0 {
		bills = billings[0]
	}
	return NewWithSubscriptions(auth, log, checks, timeout, core, goals, bills, nil)
}
func NewWithSubscriptions(auth *identity.Auth, log *slog.Logger, checks map[string]Checker, timeout time.Duration, core *service.Core, goals *goal.Service, bills *billing.Service, subs *subscription.Service) *gin.Engine {
	return NewWithPlanning(auth, log, checks, timeout, core, goals, bills, subs, nil)
}
func NewWithPlanning(auth *identity.Auth, log *slog.Logger, checks map[string]Checker, timeout time.Duration, core *service.Core, goals *goal.Service, bills *billing.Service, subs *subscription.Service, plans *planning.Service) *gin.Engine {
	return NewWithOrchestration(auth, log, checks, timeout, core, goals, bills, subs, plans, nil)
}
func NewWithOrchestration(auth *identity.Auth, log *slog.Logger, checks map[string]Checker, timeout time.Duration, core *service.Core, goals *goal.Service, bills *billing.Service, subs *subscription.Service, plans *planning.Service, jobs *orchestrator.Service, gateways ...*execution.Service) *gin.Engine {
	var gateway *execution.Service
	if len(gateways) > 0 {
		gateway = gateways[0]
	}
	return NewWithLifestyle(auth, log, checks, timeout, core, goals, bills, subs, plans, jobs, gateway, nil)
}
func NewWithLifestyle(auth *identity.Auth, log *slog.Logger, checks map[string]Checker, timeout time.Duration, core *service.Core, goals *goal.Service, bills *billing.Service, subs *subscription.Service, plans *planning.Service, jobs *orchestrator.Service, gateway *execution.Service, lives *lifestyle.Service, bridges ...*LangGraphBridge) *gin.Engine {
	return newWithLifestyle(auth, log, checks, timeout, core, goals, bills, subs, plans, jobs, gateway, lives, phoneAuthSettings{}, bridges...)
}

func NewWithPhoneAuthentication(auth *identity.Auth, log *slog.Logger, checks map[string]Checker, timeout time.Duration, core *service.Core, goals *goal.Service, bills *billing.Service, subs *subscription.Service, plans *planning.Service, jobs *orchestrator.Service, gateway *execution.Service, lives *lifestyle.Service, accounts *userauth.Service, secureCookie bool, bridges ...*LangGraphBridge) *gin.Engine {
	return newWithLifestyle(auth, log, checks, timeout, core, goals, bills, subs, plans, jobs, gateway, lives, phoneAuthSettings{service: accounts, secure: secureCookie}, bridges...)
}

func newWithLifestyle(auth *identity.Auth, log *slog.Logger, checks map[string]Checker, timeout time.Duration, core *service.Core, goals *goal.Service, bills *billing.Service, subs *subscription.Service, plans *planning.Service, jobs *orchestrator.Service, gateway *execution.Service, lives *lifestyle.Service, phoneAuth phoneAuthSettings, bridges ...*LangGraphBridge) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	_ = r.SetTrustedProxies(nil)
	r.HandleMethodNotAllowed = true
	r.Use(func(c *gin.Context) {
		start := time.Now()
		requestID := uuid.NewString()
		trace := c.GetHeader("X-Trace-ID")
		if trace == "" {
			trace = requestID
		}
		task := c.GetHeader("X-Task-ID")
		c.Set("request_id", requestID)
		c.Set("trace_id", trace)
		c.Set("task_id", task)
		c.Header("X-Request-ID", requestID)
		c.Header("X-Trace-ID", trace)
		c.Header("Cache-Control", "no-store")
		defer func() {
			route := c.FullPath()
			if route == "" {
				route = "unmatched"
			}
			attrs := []any{"request_id", requestID, "trace_id", c.GetString("trace_id"), "task_id", c.GetString("task_id"), "method", c.Request.Method, "route", route, "status", c.Writer.Status(), "duration_ms", time.Since(start).Milliseconds()}
			if v, ok := c.Get("actor"); ok {
				actor := v.(identity.Actor)
				attrs = append(attrs, "actor_kind", actor.Kind, "user_id", actor.UserID)
			}
			log.Info("http_request", attrs...)
		}()
		if !correlationPattern.MatchString(trace) || (task != "" && !correlationPattern.MatchString(task)) {
			c.Set("trace_id", requestID)
			c.Set("task_id", "")
			c.Header("X-Trace-ID", requestID)
			if gateway != nil && sensitiveExecutionPath(c.Request.Method, c.Request.URL.Path) {
				var a identity.Actor
				var authErr error
				if strings.HasPrefix(c.Request.URL.Path, "/api/v1/") {
					a, authErr = auth.UserContext(c.Request.Context(), strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
				} else {
					a, authErr = auth.Tool(c.GetHeader("X-Banking-Tool-Key"))
				}
				var actorPtr *identity.Actor
				if authErr == nil {
					actorPtr = &a
				}
				m := service.Meta{TraceID: requestID}
				if actorPtr != nil {
					m.UserID, _ = strconv.ParseInt(a.UserID, 10, 64)
				}
				if err := gateway.Denied(c.Request.Context(), actorPtr, m, "EXECUTE", "INVALID_ARGUMENT"); err != nil {
					coreError(c, err)
					c.Abort()
					return
				}
			}
			respond(c, 400, "INVALID_ARGUMENT", "invalid correlation header", nil)
			c.Abort()
			return
		}
		c.Next()
	})
	r.Use(func(c *gin.Context) {
		defer func() {
			if recover() != nil {
				log.Error("http_panic", "request_id", c.GetString("request_id"))
				if !c.Writer.Written() {
					respond(c, 500, "INTERNAL_ERROR", "internal server error", nil)
				}
				c.Abort()
			}
		}()
		c.Next()
	})
	r.GET("/healthz", func(c *gin.Context) { respond(c, 200, "OK", "alive", gin.H{"service": "banking-service"}) })
	r.GET("/readyz", func(c *gin.Context) {
		states := map[string]string{}
		ready := true
		for name, check := range checks {
			ctx, cancel := context.WithTimeout(c.Request.Context(), timeout)
			err := check(ctx)
			cancel()
			if err != nil {
				states[name] = "unavailable"
				ready = false
			} else {
				states[name] = "ready"
			}
		}
		if !ready {
			respond(c, 503, "DEPENDENCY_UNAVAILABLE", "dependencies unavailable", gin.H{"dependencies": states})
			return
		}
		respond(c, 200, "OK", "ready", gin.H{"dependencies": states})
	})
	r.GET("/openapi.yaml", func(c *gin.Context) { c.Data(200, "application/yaml", api.OpenAPI) })
	r.GET("/coze-plugin.openapi.yaml", func(c *gin.Context) { c.Data(200, "application/yaml", api.PluginOpenAPI) })
	registerPhoneAuth(r, auth, phoneAuth)
	userAuth := func(c *gin.Context) {
		raw := c.GetHeader("Authorization")
		if !strings.HasPrefix(raw, "Bearer ") {
			if gateway != nil && sensitiveExecutionPath(c.Request.Method, c.Request.URL.Path) {
				executionDenial(c, gateway, nil, "EXECUTE", "UNAUTHENTICATED")
				return
			}
			respond(c, 401, "UNAUTHENTICATED", "valid user bearer token required", nil)
			c.Abort()
			return
		}
		actor, err := auth.UserContext(c.Request.Context(), strings.TrimPrefix(raw, "Bearer "))
		if err != nil {
			if gateway != nil && sensitiveExecutionPath(c.Request.Method, c.Request.URL.Path) {
				executionDenial(c, gateway, nil, "EXECUTE", "UNAUTHENTICATED")
				return
			}
			respond(c, 401, "UNAUTHENTICATED", "valid user bearer token required", nil)
			c.Abort()
			return
		}
		c.Set("actor", actor)
		c.Next()
	}
	user := r.Group("/api/v1", userAuth)
	userV2 := r.Group("/api/v2", userAuth)
	tools := r.Group("/tools/v1", func(c *gin.Context) {
		actor, err := auth.Tool(c.GetHeader("X-Banking-Tool-Key"))
		if err != nil {
			if gateway != nil && sensitiveExecutionPath(c.Request.Method, c.Request.URL.Path) {
				executionDenial(c, gateway, nil, "PREPARE", "UNAUTHENTICATED")
				return
			}
			respond(c, 401, "UNAUTHENTICATED", "valid tool credential required", nil)
			c.Abort()
			return
		}
		c.Set("actor", actor)
		c.Next()
	})
	user.Use(skillGuard(gateway))
	registerSkills(user)
	var bridge *LangGraphBridge
	if len(bridges) > 0 {
		bridge = bridges[0]
	}
	registerLangGraph(user, auth, bridge, core)
	if gateway != nil && gateway.Configured() {
		user.Use(executionGuard(gateway))
		tools.Use(executionGuard(gateway))
	}
	registerExecution(user, tools, gateway)
	handler := func(c *gin.Context) {
		actor, _ := c.Get("actor")
		respond(c, 200, "OK", "integration context", gin.H{"actor": actor, "module": "foundation", "financial_execution_enabled": gateway != nil && gateway.Configured() && actor.(identity.Actor).Kind == "user"})
	}
	user.GET("/context", handler)
	tools.GET("/integration/context", handler)
	registerCore(user, core)
	registerRecipients(user, core, phoneAuth.service)
	registerGoals(user, goals)
	registerBilling(user, tools, bills)
	registerSubscriptions(user, tools, subs)
	registerPlanning(user, tools, plans)
	registerOrchestration(user, tools, jobs)
	registerLifestyle(userV2, tools, lives)
	r.NoRoute(func(c *gin.Context) {
		if gateway != nil && (strings.HasPrefix(c.Request.URL.Path, "/api/v1/") || strings.HasPrefix(c.Request.URL.Path, "/tools/v1/")) {
			var a identity.Actor
			var e error
			if strings.HasPrefix(c.Request.URL.Path, "/api/v1/") {
				a, e = auth.UserContext(c.Request.Context(), strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
			} else {
				a, e = auth.Tool(c.GetHeader("X-Banking-Tool-Key"))
			}
			if e != nil && sensitiveExecutionPath(c.Request.Method, c.Request.URL.Path) {
				executionDenial(c, gateway, nil, "EXECUTE", "UNAUTHENTICATED")
				return
			}
			if e == nil && (sensitiveExecutionPath(c.Request.Method, c.Request.URL.Path) || a.Kind == "agent" && c.Request.Method != "GET") {
				executionDenial(c, gateway, &a, "EXECUTE", "FORBIDDEN")
				return
			}
		}
		respond(c, 404, "NOT_FOUND", "route not found", nil)
	})
	r.NoMethod(func(c *gin.Context) {
		if gateway != nil && (strings.HasPrefix(c.Request.URL.Path, "/api/v1/") || strings.HasPrefix(c.Request.URL.Path, "/tools/v1/")) {
			var a identity.Actor
			var e error
			if strings.HasPrefix(c.Request.URL.Path, "/api/v1/") {
				a, e = auth.UserContext(c.Request.Context(), strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
			} else {
				a, e = auth.Tool(c.GetHeader("X-Banking-Tool-Key"))
			}
			if e != nil && sensitiveExecutionPath(c.Request.Method, c.Request.URL.Path) {
				executionDenial(c, gateway, nil, "EXECUTE", "UNAUTHENTICATED")
				return
			}
			if e == nil && (sensitiveExecutionPath(c.Request.Method, c.Request.URL.Path) || a.Kind == "agent" && c.Request.Method != "GET") {
				executionDenial(c, gateway, &a, "EXECUTE", "FORBIDDEN")
				return
			}
		}
		respond(c, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "method not allowed", nil)
	})
	return r
}
