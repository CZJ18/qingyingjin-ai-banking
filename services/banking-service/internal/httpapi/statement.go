package httpapi

import (
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/service"
	"github.com/qingyingjin/banking-service/internal/statement"
	"strconv"
)

func registerStatementImport(user *gin.RouterGroup, core *service.Core) {
	user.POST("/transactions/import/preview", func(c *gin.Context) {
		if c.MustGet("actor").(identity.Actor).Kind != "user" {
			respond(c, 403, "FORBIDDEN", "CSV upload requires the user", nil)
			return
		}
		if !core.Configured() {
			respond(c, 503, "DEPENDENCY_UNAVAILABLE", "Banking repository unavailable", nil)
			return
		}
		input(c, func(in struct {
			statement.Input
			AccountID string `json:"account_id"`
		}) (any, error) {
			id, e := strconv.ParseInt(in.AccountID, 10, 64)
			if e != nil || id <= 0 || strconv.FormatInt(id, 10) != in.AccountID {
				return nil, entity.Fail("INVALID_ARGUMENT", "canonical account ID required")
			}
			return core.PreviewStatement(c.Request.Context(), meta(c).UserID, id, in.Input)
		}, 200)
	})
	user.GET("/transactions/:id/source", func(c *gin.Context) {
		if !core.Configured() {
			respond(c, 503, "DEPENDENCY_UNAVAILABLE", "Banking repository unavailable", nil)
			return
		}
		id, e := pathID(c)
		if e != nil {
			coreError(c, e)
			return
		}
		transaction, e := core.Transaction(c.Request.Context(), meta(c).UserID, id)
		if e != nil {
			coreError(c, e)
			return
		}
		if len(transaction.SourceMetadata) == 0 {
			respond(c, 404, "NOT_FOUND", "no retained upload source", nil)
			return
		}
		var source statement.Source
		if json.Unmarshal(transaction.SourceMetadata, &source) != nil || statement.ValidateSource(&source) != nil {
			coreError(c, entity.Fail("NOT_FOUND", "no retained CSV source"))
			return
		}
		respond(c, 200, "OK", "unverified user-uploaded source evidence", source)
	})
}
