package execution

import (
	"context"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/orchestrator"
	"github.com/qingyingjin/banking-service/internal/skills"
	"github.com/qingyingjin/banking-service/internal/subscription"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strconv"
	"time"
)

// Propose validates persisted scheduler state. Call.Confirmed never grants
// financial authorization; this path only creates pending intents.
func (s *Service) Propose(ctx context.Context, c orchestrator.Call) (out json.RawMessage, err error) {
	actor := identity.Actor{Kind: "agent", Subject: "supervisor-worker", UserID: strconv.FormatInt(c.Meta.UserID, 10), AgentType: c.AgentType, TaskID: c.TaskID, Scopes: []string{"execution:prepare"}}
	if c.Tool != "create_cancel_intent" || c.AgentType != "subscription" || c.LeaseToken == "" || c.InvocationKey != "orchestrator:"+c.TaskID {
		return nil, s.fail(ctx, actor, c.Meta, "PREPARE", entity.Fail("FORBIDDEN", "persisted scheduler lease required"))
	}
	if _, e := skills.Resolve(skills.Request{SkillID: c.SkillID, SkillVersion: c.SkillVersion, Agent: c.AgentType, Tool: c.Tool, Mode: c.Mode}); e != nil {
		return nil, s.fail(ctx, actor, c.Meta, "PREPARE", entity.Fail("FORBIDDEN", "unauthorized skill binding"))
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := lockUser(tx, c.Meta.UserID); e != nil {
			return e
		}
		var job orchestrator.Job
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", c.JobID, c.Meta.UserID).First(&job).Error; e != nil {
			return entity.Fail("FORBIDDEN", "owned running job required")
		}
		if job.Status != "running" || job.LeaseToken != c.LeaseToken || job.LeaseUntil <= time.Now().UnixMilli() {
			return entity.Fail("FORBIDDEN", "active scheduler lease required")
		}
		var task orchestrator.AgentTask
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ? AND job_id = ?", c.TaskID, c.Meta.UserID, c.JobID).First(&task).Error; e != nil {
			return entity.Fail("FORBIDDEN", "owned running task required")
		}
		if _, e := skills.Resolve(skills.Request{SkillID: task.SkillID, SkillVersion: task.SkillVersion, Agent: task.AgentType, Tool: task.Tool, Mode: task.Mode}); e != nil {
			return entity.Fail("FORBIDDEN", "persisted skill binding invalid")
		}
		if c.SkillID != "" && (task.SkillID != c.SkillID || task.SkillVersion != c.SkillVersion) {
			return entity.Fail("FORBIDDEN", "persisted skill binding required")
		}
		if task.Status != "running" || task.Confirmation != "approve" || task.Tool != c.Tool || task.AgentType != c.AgentType || entity.CanonicalJSONHash(task.Parameters) != entity.CanonicalJSONHash(c.Parameters) {
			return entity.Fail("FORBIDDEN", "persisted task approval and exact parameters required")
		}
		var count int64
		if e := tx.Model(&orchestrator.AgentExecution{}).Where("user_id = ? AND job_id = ? AND task_id = ? AND lease_token = ? AND attempt = ? AND status = ?", c.Meta.UserID, c.JobID, c.TaskID, c.LeaseToken, task.Attempts, "running").Count(&count).Error; e != nil {
			return e
		}
		if count != 1 {
			return entity.Fail("FORBIDDEN", "active task execution required")
		}
		var in subscription.IntentInput
		if e := strict(task.Parameters, &in); e != nil {
			return e
		}
		v, e := s.prepareTx(ctx, tx, actor, c.Meta, c.InvocationKey, PrepareInput{Kind: "cancel_subscription", Parameters: marshal(in)})
		if e != nil {
			return e
		}
		var intent entity.SubscriptionActionIntent
		if e := tx.Where("id = ? AND user_id = ?", v.Operation.ResourceID, c.Meta.UserID).First(&intent).Error; e != nil {
			return e
		}
		out = marshal(intent)
		return nil
	})
	return out, s.fail(ctx, actor, c.Meta, "PREPARE", err)
}
