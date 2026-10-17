package httpapi

import (
	"encoding/json"
	"errors"
	legacy "github.com/coze-dev/coze-studio/backend/domain/banking/entity"
	"github.com/gin-gonic/gin"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/repository"

	"github.com/qingyingjin/banking-service/internal/service"
	"io"
	"net/http"
	"strconv"
)

func coreError(c *gin.Context, err error) {
	err = repository.DBError(err)
	code := entity.Code(err)
	message := "database temporarily unavailable"
	var domain *entity.Error
	if errors.As(err, &domain) {
		message = domain.Message
	}
	status := 422
	switch code {
	case "INVALID_ARGUMENT":
		status = 400
	case "NOT_FOUND":
		status = 404
	case "FORBIDDEN":
		status = 403
	case "RECIPIENT_RATE_LIMITED", "MODEL_QUOTA_EXCEEDED":
		status = 429
	case "IDEMPOTENCY_CONFLICT", "VERSION_CONFLICT", "STATE_CONFLICT", "PARSE_IN_PROGRESS", "PARSE_OUTCOME_UNKNOWN", "MODEL_OUTCOME_UNKNOWN", "MODEL_REQUEST_CONFLICT":
		status = 409
	case "DEPENDENCY_UNAVAILABLE", "COZE_UNAVAILABLE", "STORAGE_FROZEN", "MODEL_BUDGET_UNAVAILABLE":
		status = 503
	}
	respond(c, status, code, message, nil)
}
func decode(c *gin.Context, out any) error {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	d := json.NewDecoder(c.Request.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return entity.Fail("INVALID_ARGUMENT", "invalid JSON, unknown fields or non-integer amounts")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return entity.Fail("INVALID_ARGUMENT", "one JSON object required")
	}
	return nil
}
func meta(c *gin.Context) service.Meta {
	actor := c.MustGet("actor").(identity.Actor)
	uid, _ := strconv.ParseInt(actor.UserID, 10, 64)
	return service.Meta{UserID: uid, TraceID: c.GetString("trace_id"), TaskID: c.GetString("task_id")}
}
func pathID(c *gin.Context) (int64, error) {
	raw := c.Param("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 || strconv.FormatInt(id, 10) != raw {
		return 0, entity.Fail("INVALID_ARGUMENT", "positive canonical ID required")
	}
	return id, nil
}
func queryInt(c *gin.Context, key string) (int64, error) {
	raw := c.Query(key)
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || n < 0 {
		return 0, entity.Fail("INVALID_ARGUMENT", "invalid query "+key)
	}
	return n, nil
}
func paging(c *gin.Context) (*legacy.ListOptions, error) {
	l, err := queryInt(c, "limit")
	if err != nil {
		return nil, err
	}
	o, err := queryInt(c, "offset")
	if err != nil {
		return nil, err
	}
	if o > 1_000_000 || l > 1_000_000 {
		return nil, entity.Fail("INVALID_ARGUMENT", "pagination out of range")
	}
	return &legacy.ListOptions{Limit: int(l), Offset: int(o)}, nil
}
func finish(c *gin.Context, data any, err error, status int) {
	if err != nil {
		coreError(c, err)
		return
	}
	respond(c, status, "OK", "success", data)
}
func input[T any](c *gin.Context, fn func(T) (any, error), status int) {
	var in T
	if err := decode(c, &in); err != nil {
		coreError(c, err)
		return
	}
	data, err := fn(in)
	finish(c, data, err, status)
}
func registerCore(user *gin.RouterGroup, s *service.Core) {
	registerAA(user, s)
	registerRenewals(user, s)
	user.GET("/cards", func(c *gin.Context) {
		if c.MustGet("actor").(identity.Actor).Kind != "user" {
			respond(c, 403, "FORBIDDEN", "card management requires the user", nil)
			return
		}
		if !s.Configured() {
			respond(c, 503, "DEPENDENCY_UNAVAILABLE", "Banking database unavailable", nil)
			return
		}
		v, e := s.Cards(c.Request.Context(), meta(c).UserID)
		finish(c, v, e, 200)
	})
	user.GET("/cards/:id", func(c *gin.Context) {
		if c.MustGet("actor").(identity.Actor).Kind != "user" {
			respond(c, 403, "FORBIDDEN", "card management requires the user", nil)
			return
		}
		if !s.Configured() {
			respond(c, 503, "DEPENDENCY_UNAVAILABLE", "Banking database unavailable", nil)
			return
		}
		v, e := s.Card(c.Request.Context(), meta(c).UserID, c.Param("id"))
		finish(c, v, e, 200)
	})
	group := user.Group("", func(c *gin.Context) {
		if !s.Configured() {
			respond(c, 503, "DEPENDENCY_UNAVAILABLE", "Banking Core database is not configured", nil)
			c.Abort()
			return
		}
		c.Next()
	})
	group.POST("/users/me", func(c *gin.Context) {
		input(c, func(in struct {
			DisplayName string `json:"display_name"`
		}) (any, error) {
			return s.Register(c.Request.Context(), meta(c), in.DisplayName)
		}, 201)
	})
	group.GET("/users/me", func(c *gin.Context) {
		v, err := s.User(c.Request.Context(), meta(c).UserID)
		finish(c, v, err, 200)
	})
	group.POST("/simulation/accounts", func(c *gin.Context) {
		input(c, func(in service.AccountInput) (any, error) {
			return s.CreateAccount(c.Request.Context(), meta(c), c.GetHeader("Idempotency-Key"), in)
		}, 201)
	})
	group.GET("/accounts", func(c *gin.Context) {
		o, err := paging(c)
		if err != nil {
			coreError(c, err)
			return
		}
		v, err := s.Accounts(c.Request.Context(), meta(c).UserID, o)
		finish(c, v, err, 200)
	})
	group.GET("/accounts/:id", func(c *gin.Context) {
		id, err := pathID(c)
		if err != nil {
			coreError(c, err)
			return
		}
		v, err := s.Account(c.Request.Context(), meta(c).UserID, id)
		finish(c, v, err, 200)
	})
	group.GET("/accounts/:id/balance", func(c *gin.Context) {
		id, err := pathID(c)
		if err != nil {
			coreError(c, err)
			return
		}
		v, err := s.Balance(c.Request.Context(), meta(c).UserID, id)
		finish(c, v, err, 200)
	})
	group.PATCH("/accounts/:id/status", func(c *gin.Context) {
		id, err := pathID(c)
		if err != nil {
			coreError(c, err)
			return
		}
		input(c, func(in struct {
			Status string `json:"status"`
		}) (any, error) {
			err := s.SetAccountStatus(c.Request.Context(), meta(c), id, in.Status)
			return gin.H{"account_id": strconv.FormatInt(id, 10), "status": in.Status}, err
		}, 200)
	})
	group.GET("/accounts/:id/ledger", func(c *gin.Context) {
		id, err := pathID(c)
		if err != nil {
			coreError(c, err)
			return
		}
		if _, err = s.Account(c.Request.Context(), meta(c).UserID, id); err != nil {
			coreError(c, err)
			return
		}
		o, err := paging(c)
		if err != nil {
			coreError(c, err)
			return
		}
		v, err := s.AccountLedger(c.Request.Context(), meta(c).UserID, id, o)
		finish(c, v, err, 200)
	})
	group.POST("/transactions/import", func(c *gin.Context) {
		input(c, func(in service.TransactionInput) (any, error) {
			return s.ImportTransaction(c.Request.Context(), meta(c), c.GetHeader("Idempotency-Key"), in)
		}, 201)
	})
	group.GET("/transactions", func(c *gin.Context) {
		o, err := paging(c)
		if err != nil {
			coreError(c, err)
			return
		}
		account, err := queryInt(c, "bank_account_id")
		if err != nil {
			coreError(c, err)
			return
		}
		from, err := queryInt(c, "from")
		if err != nil {
			coreError(c, err)
			return
		}
		to, err := queryInt(c, "to")
		if err != nil {
			coreError(c, err)
			return
		}
		if (to > 0 && to <= from) || (c.Query("direction") != "" && c.Query("direction") != "income" && c.Query("direction") != "expense") {
			coreError(c, entity.Fail("INVALID_ARGUMENT", "invalid period/direction"))
			return
		}
		if account > 0 {
			if _, err = s.Account(c.Request.Context(), meta(c).UserID, account); err != nil {
				coreError(c, err)
				return
			}
		}
		v, total, err := s.Transactions(c.Request.Context(), meta(c).UserID, service.TransactionFilter{AccountID: account, Direction: c.Query("direction"), Category: c.Query("category"), Merchant: c.Query("merchant"), From: from, To: to, Limit: o.Limit, Offset: o.Offset})
		normalized := legacy.NormalizeListOptions(o)
		finish(c, gin.H{"items": v, "total": total, "limit": normalized.Limit, "offset": normalized.Offset}, err, 200)
	})
	group.GET("/transactions/:id", func(c *gin.Context) {
		id, err := pathID(c)
		if err != nil {
			coreError(c, err)
			return
		}
		v, err := s.Transaction(c.Request.Context(), meta(c).UserID, id)
		finish(c, v, err, 200)
	})
	group.POST("/transfers", func(c *gin.Context) {
		input(c, func(in service.TransferInput) (any, error) {
			return s.CreateTransfer(c.Request.Context(), meta(c), c.GetHeader("Idempotency-Key"), in)
		}, 201)
	})
	group.GET("/transfers/:id", func(c *gin.Context) {
		id, err := pathID(c)
		if err != nil {
			coreError(c, err)
			return
		}
		v, err := s.Transfer(c.Request.Context(), meta(c).UserID, id)
		finish(c, v, err, 200)
	})
	group.POST("/transfers/:id/approvals", func(c *gin.Context) {
		id, err := pathID(c)
		if err != nil {
			coreError(c, err)
			return
		}
		var in service.ApprovalInput
		if err = decode(c, &in); err != nil {
			coreError(c, err)
			return
		}
		v, err := s.ApproveTransfer(c.Request.Context(), meta(c), id, in)
		if err != nil {
			coreError(c, err)
			return
		}
		if v.Status == "failed" {
			respond(c, 422, v.FailureCode, "simulated transfer failed", v)
			return
		}
		finish(c, v, nil, 200)
	})
	group.POST("/budgets", func(c *gin.Context) {
		input(c, func(in service.BudgetInput) (any, error) {
			return s.CreateBudget(c.Request.Context(), meta(c), c.GetHeader("Idempotency-Key"), in)
		}, 201)
	})
	group.GET("/budgets", func(c *gin.Context) {
		o, err := paging(c)
		if err != nil {
			coreError(c, err)
			return
		}
		v, err := s.Budgets(c.Request.Context(), meta(c).UserID, c.Query("month"), o)
		finish(c, v, err, 200)
	})
	group.GET("/budgets/:id", func(c *gin.Context) {
		id, err := pathID(c)
		if err != nil {
			coreError(c, err)
			return
		}
		v, err := s.Budget(c.Request.Context(), meta(c).UserID, id)
		finish(c, v, err, 200)
	})
	group.GET("/budgets/:id/usage", func(c *gin.Context) {
		id, err := pathID(c)
		if err != nil {
			coreError(c, err)
			return
		}
		v, err := s.BudgetUsage(c.Request.Context(), meta(c).UserID, id)
		finish(c, v, err, 200)
	})
	group.POST("/savings/goals", func(c *gin.Context) {
		input(c, func(in service.GoalInput) (any, error) {
			return s.CreateGoal(c.Request.Context(), meta(c), c.GetHeader("Idempotency-Key"), in)
		}, 201)
	})
	group.GET("/savings/goals", func(c *gin.Context) {
		o, err := paging(c)
		if err != nil {
			coreError(c, err)
			return
		}
		goals, err := s.Goals(c.Request.Context(), meta(c).UserID, o)
		if err != nil {
			coreError(c, err)
			return
		}
		finish(c, goals, nil, 200)
	})
	group.GET("/savings/goals/:id", func(c *gin.Context) {
		id, err := pathID(c)
		if err != nil {
			coreError(c, err)
			return
		}
		v, err := s.Goal(c.Request.Context(), meta(c).UserID, id)
		finish(c, v, err, 200)
	})
	group.GET("/savings/goals/:id/ledger", func(c *gin.Context) {
		id, err := pathID(c)
		if err != nil {
			coreError(c, err)
			return
		}
		if _, err = s.Goal(c.Request.Context(), meta(c).UserID, id); err != nil {
			coreError(c, err)
			return
		}
		o, err := paging(c)
		if err != nil {
			coreError(c, err)
			return
		}
		v, err := s.GoalLedger(c.Request.Context(), meta(c).UserID, id, o)
		finish(c, v, err, 200)
	})
	for _, withdraw := range []bool{false, true} {
		suffix := "/deposits"
		if withdraw {
			suffix = "/withdrawals"
		}
		withdraw := withdraw
		group.POST("/savings/goals/:id"+suffix, func(c *gin.Context) {
			id, err := pathID(c)
			if err != nil {
				coreError(c, err)
				return
			}
			input(c, func(in service.GoalTransferInput) (any, error) {
				return s.GoalTransfer(c.Request.Context(), meta(c), id, c.GetHeader("Idempotency-Key"), in, withdraw)
			}, 201)
		})
	}
	group.GET("/audits", func(c *gin.Context) {
		o, err := paging(c)
		if err != nil {
			coreError(c, err)
			return
		}
		v, err := s.Audits(c.Request.Context(), meta(c).UserID, o)
		finish(c, v, err, 200)
	})
}
