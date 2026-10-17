package httpapi

import "github.com/gin-gonic/gin"

type Envelope struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	RequestID string `json:"request_id"`
	TraceID   string `json:"trace_id"`
	TaskID    string `json:"task_id,omitempty"`
	Data      any    `json:"data,omitempty"`
}

func respond(c *gin.Context, status int, code, msg string, data any) {
	c.JSON(status, Envelope{Code: code, Message: msg, RequestID: c.GetString("request_id"), TraceID: c.GetString("trace_id"), TaskID: c.GetString("task_id"), Data: data})
}
