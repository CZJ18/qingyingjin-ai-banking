package httpapi

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/orchestrator"
	"io"
	"net/http"
	"strconv"
	"time"
)

func orchestrationID(raw string) (string, error) {
	id, e := uuid.Parse(raw)
	if e != nil || id.String() != raw {
		return "", entity.Fail("INVALID_ARGUMENT", "canonical job UUID required")
	}
	return raw, nil
}
func orchestrationCursor(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	n, e := strconv.ParseInt(raw, 10, 64)
	if e != nil || n < 0 || strconv.FormatInt(n, 10) != raw {
		return 0, entity.Fail("INVALID_ARGUMENT", "canonical nonnegative event ID required")
	}
	return n, nil
}
func registerOrchestration(user, tools *gin.RouterGroup, s *orchestrator.Service) {
	ready := func(c *gin.Context) {
		if !s.Configured() {
			respond(c, 503, "DEPENDENCY_UNAVAILABLE", "orchestration repository unavailable", nil)
			c.Abort()
			return
		}
		c.Next()
	}
	g := user.Group("/orchestrations", ready)
	g.POST("", func(c *gin.Context) {
		input(c, func(in orchestrator.SubmitInput) (any, error) {
			return s.Submit(c.Request.Context(), meta(c), c.GetHeader("Idempotency-Key"), in)
		}, 201)
	})
	valid := func(c *gin.Context) (string, bool) {
		id, e := orchestrationID(c.Param("id"))
		if e != nil {
			coreError(c, e)
			return "", false
		}
		return id, true
	}
	g.GET("/:id", func(c *gin.Context) {
		id, ok := valid(c)
		if !ok {
			return
		}
		v, e := s.Get(c.Request.Context(), meta(c).UserID, id)
		finish(c, v, e, 200)
	})
	g.GET("/:id/result", func(c *gin.Context) {
		id, ok := valid(c)
		if !ok {
			return
		}
		v, e := s.Result(c.Request.Context(), meta(c).UserID, id)
		finish(c, v, e, 200)
	})
	g.GET("/:id/trace", func(c *gin.Context) {
		id, ok := valid(c)
		if !ok {
			return
		}
		after, e := orchestrationCursor(c.Query("last_event_id"))
		if e != nil {
			coreError(c, e)
			return
		}
		limit, e := orchestrationCursor(c.DefaultQuery("limit", "100"))
		if e != nil || limit < 1 || limit > 100 {
			coreError(c, entity.Fail("INVALID_ARGUMENT", "limit must be 1..100"))
			return
		}
		v, e := s.Trace(c.Request.Context(), meta(c).UserID, id, after, int(limit))
		finish(c, v, e, 200)
	})
	for _, action := range []string{"cancel", "pause", "resume"} {
		g.POST("/:id/"+action, func(c *gin.Context) {
			id, ok := valid(c)
			if !ok {
				return
			}
			body, e := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
			if e != nil {
				coreError(c, entity.Fail("INVALID_ARGUMENT", "empty body or {} required"))
				return
			}
			trimmed := bytes.TrimSpace(body)
			if len(trimmed) > 0 && trimmed[0] != '{' {
				coreError(c, entity.Fail("INVALID_ARGUMENT", "empty body or {} required"))
				return
			}
			if len(trimmed) > 0 {
				c.Request.Body = io.NopCloser(bytes.NewReader(body))
				var empty struct{}
				if e = decode(c, &empty); e != nil {
					coreError(c, e)
					return
				}
			}
			v, e := s.Control(c.Request.Context(), meta(c), id, action)
			finish(c, v, e, 200)
		})
	}
	g.POST("/:id/confirm", func(c *gin.Context) {
		id, ok := valid(c)
		if !ok {
			return
		}
		input(c, func(in orchestrator.ConfirmInput) (any, error) {
			return s.Confirm(c.Request.Context(), meta(c), id, in)
		}, 200)
	})
	g.POST("/:id/inputs", func(c *gin.Context) {
		id, ok := valid(c)
		if !ok {
			return
		}
		input(c, func(in orchestrator.InputsInput) (any, error) { return s.Inputs(c.Request.Context(), meta(c), id, in) }, 200)
	})
	g.GET("/:id/events", func(c *gin.Context) {
		id, ok := valid(c)
		if !ok {
			return
		}
		after, e := orchestrationCursor(c.GetHeader("Last-Event-ID"))
		if e != nil {
			coreError(c, e)
			return
		}
		if _, e = s.Get(c.Request.Context(), meta(c).UserID, id); e != nil {
			coreError(c, e)
			return
		}
		streamOrchestration(c, s, id, after)
	})
	for _, name := range []string{"get_orchestration", "get_orchestration_result"} {
		tools.POST("/orchestrations/"+name, ready, func(c *gin.Context) {
			input(c, func(in struct {
				JobID string `json:"job_id"`
			}) (any, error) {
				id, e := orchestrationID(in.JobID)
				if e != nil {
					return nil, e
				}
				if name == "get_orchestration_result" {
					return s.Result(c.Request.Context(), meta(c).UserID, id)
				}
				return s.Get(c.Request.Context(), meta(c).UserID, id)
			}, 200)
		})
	}
}
func streamOrchestration(c *gin.Context, s *orchestrator.Service, id string, after int64) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("X-Accel-Buffering", "no")
	c.Header("Cache-Control", "no-store")
	c.Status(200)
	_ = http.NewResponseController(c.Writer).SetWriteDeadline(time.Now().Add(35 * time.Second))
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	write := func(event string, n int64, v any) bool {
		b, e := json.Marshal(v)
		if e != nil {
			return false
		}
		prefix := ""
		if n > 0 {
			prefix = fmt.Sprintf("id: %d\n", n)
		}
		if _, e = fmt.Fprintf(c.Writer, "%sevent: %s\ndata: %s\n\n", prefix, event, b); e != nil {
			return false
		}
		c.Writer.Flush()
		return true
	}
	for {
		events, e := s.Events(c.Request.Context(), meta(c).UserID, id, after, 100)
		if e != nil {
			write("error", 0, map[string]string{"code": entity.Code(e)})
			return
		}
		for _, ev := range events {
			if !write("task", ev.ID, ev) {
				return
			}
			after = ev.ID
		}
		if len(events) == 100 {
			continue
		}
		v, e := s.Get(c.Request.Context(), meta(c).UserID, id)
		if e != nil {
			return
		}
		switch v.Job.Status {
		case "completed", "succeeded", "failed", "cancelled":
			// Completion may commit between the event read and the status read.
			// Drain that final transaction before ending the replay stream.
			tail, err := s.Events(c.Request.Context(), meta(c).UserID, id, after, 100)
			if err != nil {
				write("error", 0, map[string]string{"code": entity.Code(err)})
				return
			}
			if len(tail) > 0 {
				for _, ev := range tail {
					if !write("task", ev.ID, ev) {
						return
					}
					after = ev.ID
				}
				continue
			}
			write("end", 0, map[string]string{"status": v.Job.Status})
			return
		}
		select {
		case <-c.Request.Context().Done():
			return
		case <-timer.C:
			write("end", 0, map[string]string{"status": "reconnect", "last_event_id": strconv.FormatInt(after, 10)})
			return
		case <-ticker.C:
			if _, e = fmt.Fprint(c.Writer, ": heartbeat\n\n"); e != nil {
				return
			}
			c.Writer.Flush()
		}
	}
}
