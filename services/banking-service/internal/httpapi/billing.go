package httpapi

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/billing"
	"github.com/qingyingjin/banking-service/internal/entity"
)

func registerBilling(user, tools *gin.RouterGroup, s *billing.Service) {
	for _, parent := range []*gin.RouterGroup{user, tools} {
		group := parent.Group("/billing", func(c *gin.Context) {
			if !s.Configured() {
				respond(c, 503, "DEPENDENCY_UNAVAILABLE", "billing repository unavailable", nil)
				c.Abort()
				return
			}
			c.Next()
		})
		for _, name := range billing.Tools {
			tool := name
			group.POST("/"+tool, func(c *gin.Context) {
				input(c, func(in billing.Input) (any, error) { return s.Run(c.Request.Context(), meta(c), tool, in) }, 200)
			})
		}
		group.GET("/reports/:id", func(c *gin.Context) {
			id := c.Param("id")
			parsed, e := uuid.Parse(id)
			if e != nil || parsed.String() != id {
				coreError(c, entity.Fail("INVALID_ARGUMENT", "canonical report UUID required"))
				return
			}
			r, e := s.Get(c.Request.Context(), meta(c).UserID, id)
			finish(c, r, e, 200)
		})
	}
	group := user.Group("/billing", func(c *gin.Context) {
		if !s.Configured() {
			respond(c, 503, "DEPENDENCY_UNAVAILABLE", "billing repository unavailable", nil)
			c.Abort()
			return
		}
		c.Next()
	})
	group.POST("/query", func(c *gin.Context) {
		input(c, func(in billing.QueryInput) (any, error) { return s.Query(c.Request.Context(), meta(c), in) }, 200)
	})
}
