package httpapi

import (
	"github.com/gin-gonic/gin"
	"github.com/qingyingjin/banking-service/internal/subscription"
)

func registerSubscriptions(user, tools *gin.RouterGroup, s *subscription.Service) {
	ready := func(c *gin.Context) {
		if !s.Configured() {
			respond(c, 503, "DEPENDENCY_UNAVAILABLE", "subscription repository unavailable", nil)
			c.Abort()
			return
		}
		c.Next()
	}
	for _, parent := range []*gin.RouterGroup{user, tools} {
		g := parent.Group("/subscriptions", ready)
		g.POST("/detect_subscriptions", func(c *gin.Context) {
			input(c, func(in subscription.DetectInput) (any, error) {
				return s.Analyze(c.Request.Context(), meta(c), "detect_subscriptions", in)
			}, 200)
		})
		for _, tool := range []string{"analyze_subscription_cost", "suggest_subscription_actions"} {
			name := tool
			g.POST("/"+name, func(c *gin.Context) {
				// Native Coze emits no request body for an empty object schema.
				if c.Request.ContentLength == 0 {
					out, e := s.Analyze(c.Request.Context(), meta(c), name, subscription.DetectInput{})
					finish(c, out, e, 200)
					return
				}
				input(c, func(in struct{}) (any, error) {
					return s.Analyze(c.Request.Context(), meta(c), name, subscription.DetectInput{})
				}, 200)
			})
		}
		for _, tool := range []string{"get_subscription_details", "get_subscription_status"} {
			g.POST("/"+tool, func(c *gin.Context) {
				input(c, func(in subscription.IDInput) (any, error) {
					id, e := subscription.ID(in.SubscriptionID)
					if e != nil {
						return nil, e
					}
					return s.Get(c.Request.Context(), meta(c), id)
				}, 200)
			})
		}
		intentStatus := 200
		if parent == user {
			intentStatus = 201
		}
		g.POST("/create_cancel_intent", func(c *gin.Context) {
			input(c, func(in subscription.IntentInput) (any, error) {
				return s.CreateIntent(c.Request.Context(), meta(c), c.GetHeader("Idempotency-Key"), in)
			}, intentStatus)
		})
	}
	g := user.Group("/subscriptions", ready)
	g.GET("", func(c *gin.Context) {
		out, e := s.Analyze(c.Request.Context(), meta(c), "analyze_subscription_cost", subscription.DetectInput{})
		finish(c, out, e, 200)
	})
	for _, path := range []string{"/:id", "/:id/history"} {
		g.GET(path, func(c *gin.Context) {
			id, e := pathID(c)
			if e != nil {
				coreError(c, e)
				return
			}
			out, e := s.Get(c.Request.Context(), meta(c), id)
			finish(c, out, e, 200)
		})
	}
	g.POST("/:id/manage", func(c *gin.Context) {
		id, e := pathID(c)
		if e != nil {
			coreError(c, e)
			return
		}
		input(c, func(in subscription.ManageInput) (any, error) {
			return s.Manage(c.Request.Context(), meta(c), c.GetHeader("Idempotency-Key"), id, in)
		}, 200)
	})
	g.GET("/analyses/:id", func(c *gin.Context) {
		id, e := pathID(c)
		if e != nil {
			coreError(c, e)
			return
		}
		out, e := s.GetAnalysis(c.Request.Context(), meta(c), id)
		finish(c, out, e, 200)
	})
	intents := user.Group("/subscription-intents", ready)
	intents.GET("/:id", func(c *gin.Context) {
		id, e := pathID(c)
		if e != nil {
			coreError(c, e)
			return
		}
		out, e := s.GetIntent(c.Request.Context(), meta(c), id)
		finish(c, out, e, 200)
	})
	// Deliberately user JWT only. The Coze plugin never exposes management or confirmation.
	intents.POST("/:id/confirm", func(c *gin.Context) {
		id, e := pathID(c)
		if e != nil {
			coreError(c, e)
			return
		}
		input(c, func(in subscription.ConfirmInput) (any, error) {
			return s.Confirm(c.Request.Context(), meta(c), id, in)
		}, 200)
	})
}
