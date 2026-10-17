package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/orchestrator"
	"github.com/qingyingjin/banking-service/internal/service"
	"github.com/qingyingjin/banking-service/internal/skills"
)

// The bridge delegates only task-bound READ/PLAN/PREPARE identity. The JWT
// signing secret remains in Go; Coze receives neither bearer nor service key.
type LangGraphExecutor interface {
	Run(context.Context, orchestrator.Call) (orchestrator.AgentOutput, error)
	PlanWithEvidence(context.Context, service.Meta, string, orchestrator.Bindings) ([]orchestrator.TaskSpec, string, error)
}
type LangGraphBridge struct {
	keyHash    [32]byte
	configured bool
	runner     LangGraphExecutor
	// Used by the server to verify an active persisted simulation user.
	ActiveUser func(context.Context, int64) error
}

func NewLangGraphBridge(key string, runner LangGraphExecutor) *LangGraphBridge {
	return &LangGraphBridge{keyHash: sha256.Sum256([]byte(key)), configured: len(key) >= 32 && runner != nil, runner: runner}
}

var langGraphTask = regexp.MustCompile(`^lg-([0-9a-f-]{36})\.([A-Za-z0-9][A-Za-z0-9_-]{0,63})$`)

func validLangGraphTask(raw string) bool {
	m := langGraphTask.FindStringSubmatch(raw)
	if len(m) != 3 || len(raw) > 128 {
		return false
	}
	id, err := uuid.Parse(m[1])
	return err == nil && id.String() == m[1]
}
func registerLangGraph(user *gin.RouterGroup, auth *identity.Auth, bridge *LangGraphBridge, core *service.Core) {
	if bridge != nil && bridge.ActiveUser == nil && core != nil && core.Configured() {
		bridge.ActiveUser = func(ctx context.Context, id int64) error {
			v, err := core.User(ctx, id)
			if err != nil {
				return err
			}
			if v.Status != "active" {
				return entity.Fail("FORBIDDEN", "active simulation user required")
			}
			return nil
		}
	}
	g := user.Group("/langgraph", func(c *gin.Context) {
		if bridge == nil || !bridge.configured || bridge.ActiveUser == nil {
			respond(c, 503, "DEPENDENCY_UNAVAILABLE", "LangGraph bridge is not configured", nil)
			c.Abort()
			return
		}
		raw := c.GetHeader("X-Banking-Agent-Key")
		hash := sha256.Sum256([]byte(raw))
		if raw == "" || subtle.ConstantTimeCompare(hash[:], bridge.keyHash[:]) != 1 {
			respond(c, 401, "UNAUTHENTICATED", "valid agent-service credential required", nil)
			c.Abort()
			return
		}
		a := c.MustGet("actor").(identity.Actor)
		uid, err := strconv.ParseInt(a.UserID, 10, 64)
		if err != nil || uid <= 0 {
			respond(c, 403, "FORBIDDEN", "owned identity required", nil)
			c.Abort()
			return
		}
		if err = bridge.ActiveUser(c.Request.Context(), uid); err != nil {
			coreError(c, err)
			c.Abort()
			return
		}
		c.Next()
	})
	g.POST("/delegations", func(c *gin.Context) {
		a := c.MustGet("actor").(identity.Actor)
		if a.Kind != "user" {
			respond(c, 403, "FORBIDDEN", "user identity is required for delegation", nil)
			return
		}
		var in struct {
			TaskID    string `json:"task_id"`
			AgentType string `json:"agent_type"`
		}
		if err := decode(c, &in); err != nil {
			coreError(c, err)
			return
		}
		if !validLangGraphTask(in.TaskID) {
			coreError(c, entity.Fail("INVALID_ARGUMENT", "LangGraph task namespace required"))
			return
		}
		token, err := auth.IssueAgent(a.UserID, in.AgentType, in.TaskID, 2*time.Minute)
		if err != nil {
			coreError(c, entity.Fail("INVALID_ARGUMENT", "registered specialist required"))
			return
		}
		respond(c, 200, "OK", "task delegation", gin.H{"token": token, "expires_in_seconds": 120, "agent_type": in.AgentType, "task_id": in.TaskID})
	})
	agent := func(c *gin.Context) (identity.Actor, bool) {
		a := c.MustGet("actor").(identity.Actor)
		if a.Kind != "agent" || !validLangGraphTask(a.TaskID) {
			respond(c, 403, "FORBIDDEN", "signed LangGraph specialist required", nil)
			return a, false
		}
		return a, true
	}
	g.POST("/run", func(c *gin.Context) {
		a, ok := agent(c)
		if !ok {
			return
		}
		var in struct {
			SkillID       string          `json:"skill_id"`
			SkillVersion  int64           `json:"skill_version"`
			Tool          string          `json:"tool"`
			ExecutionMode string          `json:"execution_mode"`
			Parameters    json.RawMessage `json:"parameters"`
			UseCoze       bool            `json:"use_coze"`
		}
		if err := decode(c, &in); err != nil {
			coreError(c, err)
			return
		}
		b, err := skills.Resolve(skills.Request{SkillID: in.SkillID, SkillVersion: in.SkillVersion, Agent: a.AgentType, Tool: in.Tool, Mode: in.ExecutionMode})
		if err != nil || in.SkillID == "" || in.SkillVersion <= 0 || in.ExecutionMode != b.Mode || a.AgentType == "supervisor" || (b.Mode != "read_only" && b.Mode != "advice") {
			respond(c, 403, "FORBIDDEN", "registered specialist READ or PLAN tool required", nil)
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
		defer cancel()
		out, err := bridge.runner.Run(ctx, orchestrator.Call{Meta: meta(c), TaskID: a.TaskID, AgentType: a.AgentType, SkillID: b.SkillID, SkillVersion: b.SkillVersion, Mode: b.Mode, Tool: in.Tool, Parameters: in.Parameters, UseCoze: in.UseCoze, InvocationKey: "langgraph:" + a.TaskID})
		if ctx.Err() != nil {
			respond(c, 504, "TOOL_TIMEOUT", "specialist deadline exceeded", nil)
			return
		}
		if err != nil {
			coreError(c, err)
			return
		}
		if out.TaskID != a.TaskID || out.AgentType != a.AgentType || out.Status != "succeeded" || out.FinancialExecutionEnabled {
			coreError(c, entity.Fail("TOOL_OUTPUT_INVALID", "invalid specialist output"))
			return
		}
		out.SkillID, out.SkillVersion, out.RuleVersion = b.SkillID, b.SkillVersion, "1"
		respond(c, 200, "OK", "specialist result", out)
	})
	g.POST("/plan", func(c *gin.Context) {
		a, ok := agent(c)
		if !ok {
			return
		}
		if a.AgentType != "supervisor" {
			respond(c, 403, "FORBIDDEN", "signed Supervisor required", nil)
			return
		}
		var in struct {
			Instruction string                `json:"instruction"`
			Bindings    orchestrator.Bindings `json:"bindings"`
		}
		if err := decode(c, &in); err != nil {
			coreError(c, err)
			return
		}
		if strings.TrimSpace(in.Instruction) == "" || len(in.Instruction) > 8192 {
			coreError(c, entity.Fail("INVALID_ARGUMENT", "bounded instruction required"))
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
		defer cancel()
		tasks, executeID, err := bridge.runner.PlanWithEvidence(ctx, meta(c), in.Instruction, in.Bindings)
		if ctx.Err() != nil {
			respond(c, 504, "TOOL_TIMEOUT", "Supervisor deadline exceeded", nil)
			return
		}
		if err != nil {
			coreError(c, err)
			return
		}
		respond(c, 200, "OK", "validated task plan", gin.H{"tasks": tasks, "workflow_execute_id": executeID, "owner_engine": "langgraph", "financial_execution_enabled": false})
	})
}
