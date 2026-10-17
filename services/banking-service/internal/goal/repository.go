package goal

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	coreRepo "github.com/qingyingjin/banking-service/internal/repository"
	"github.com/qingyingjin/banking-service/internal/service"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"time"
)

// Repository exposes goal persistence only, never account/ledger writes.
type Repository struct {
	db       *gorm.DB
	requests *coreRepo.Core
}

func NewRepository(db *gorm.DB) *Repository { return &Repository{db: db, requests: coreRepo.New(db)} }
func goalAudit(m service.Meta, action string, id int64, outcome, code string) *entity.Audit {
	return &entity.Audit{ID: uuid.NewString(), UserID: m.UserID, Action: action, ResourceID: id, Outcome: outcome, ErrorCode: code, TraceID: m.TraceID, TaskID: m.TaskID}
}
func (r *Repository) Create(ctx context.Context, m service.Meta, key string, in Input) (View, error) {
	return coreRepo.Atomic(r.requests, ctx, m.UserID, "life_goal.create", key, in, func(tx *coreRepo.Tx) (View, error) {
		if err := tx.RequireUser(); err != nil {
			return View{}, err
		}
		g := Goal{Base: entity.Base{UserID: m.UserID}, CurrentVersion: 1, Status: "unparsed"}
		if err := tx.Insert(&g); err != nil {
			return View{}, err
		}
		raw, _ := json.Marshal(in.Context)
		a := goalAudit(m, "life_goal.create", g.ID, "succeeded", "")
		v := GoalVersion{Base: entity.Base{UserID: m.UserID}, GoalID: g.ID, Version: 1, Text: in.Text, ContextJSON: raw, ParseStatus: "unparsed", TraceID: m.TraceID, AuditID: a.ID}
		if err := tx.Insert(&v); err != nil {
			return View{}, err
		}
		if err := tx.Audit(a); err != nil {
			return View{}, err
		}
		return View{Goal: &g, Version: &v}, nil
	})
}
func (r *Repository) Update(ctx context.Context, m service.Meta, id int64, in Input) (View, error) {
	var out View
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var g Goal
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=?", id, m.UserID).First(&g).Error; err != nil {
			return err
		}
		if g.CurrentVersion != in.ExpectedVersion {
			return entity.Fail("VERSION_CONFLICT", "goal has a newer version")
		}
		var u entity.User
		if err := tx.Where("id=? AND status='active'", m.UserID).First(&u).Error; err != nil {
			return err
		}
		g.CurrentVersion++
		g.Status = "unparsed"
		raw, _ := json.Marshal(in.Context)
		a := goalAudit(m, "life_goal.update", g.ID, "succeeded", "")
		v := GoalVersion{Base: entity.Base{UserID: m.UserID}, GoalID: g.ID, Version: g.CurrentVersion, Text: in.Text, ContextJSON: raw, ParseStatus: "unparsed", TraceID: m.TraceID, AuditID: a.ID}
		if err := tx.Create(&v).Error; err != nil {
			return err
		}
		if err := tx.Save(&g).Error; err != nil {
			return err
		}
		if err := tx.Create(a).Error; err != nil {
			return err
		}
		out = View{Goal: &g, Version: &v}
		return nil
	})
	return out, coreRepo.DBError(err)
}
func (r *Repository) View(ctx context.Context, user, id, version int64) (View, error) {
	var out View
	g := new(Goal)
	if err := r.db.WithContext(ctx).Where("id=? AND user_id=?", id, user).First(g).Error; err != nil {
		return out, coreRepo.DBError(err)
	}
	if version == 0 {
		version = g.CurrentVersion
	}
	v := new(GoalVersion)
	if err := r.db.WithContext(ctx).Where("goal_id=? AND user_id=? AND version=?", id, user, version).First(v).Error; err != nil {
		return out, coreRepo.DBError(err)
	}
	out = View{Goal: g, Version: v}
	plan := new(TaskPlan)
	err := r.db.WithContext(ctx).Where("goal_version_id=? AND user_id=?", v.ID, user).First(plan).Error
	if err == nil {
		out.Plan = plan
	} else if err != gorm.ErrRecordNotFound {
		return View{}, coreRepo.DBError(err)
	}
	return out, nil
}
func (r *Repository) Versions(ctx context.Context, user, id int64, limit, offset int) ([]GoalVersion, error) {
	if _, err := r.View(ctx, user, id, 0); err != nil {
		return nil, err
	}
	rows := []GoalVersion{}
	err := r.db.WithContext(ctx).Where("goal_id=? AND user_id=?", id, user).Order("version DESC").Limit(limit).Offset(offset).Find(&rows).Error
	return rows, coreRepo.DBError(err)
}
func (r *Repository) Claim(ctx context.Context, m service.Meta, id, version int64, workflow string) (*GoalVersion, error) {
	var out GoalVersion
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var g Goal
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=?", id, m.UserID).First(&g).Error; err != nil {
			return err
		}
		if g.CurrentVersion != version {
			return entity.Fail("VERSION_CONFLICT", "parse must target the current goal version")
		}
		var u entity.User
		if err := tx.Where("id=? AND status='active'", m.UserID).First(&u).Error; err != nil {
			return err
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("goal_id=? AND user_id=? AND version=?", id, m.UserID, version).First(&out).Error; err != nil {
			return err
		}
		if out.ParseStatus == "parsed" {
			return nil
		}
		if out.ParseStatus == "parsing" {
			if out.LeaseUntil < time.Now().UnixMilli() {
				return entity.Fail("PARSE_OUTCOME_UNKNOWN", "previous parser outcome unknown; create a new goal version to retry")
			}
			return entity.Fail("PARSE_IN_PROGRESS", "goal version is already parsing")
		}
		bindCompilation(&out)
		out.ParseStatus = "parsing"
		out.ParseID = uuid.NewString()
		out.WorkflowID = workflow
		out.ErrorCode = ""
		out.LeaseUntil = time.Now().Add(2 * time.Minute).UnixMilli()
		if err := tx.Save(&out).Error; err != nil {
			return err
		}
		return tx.Create(goalAudit(m, "life_goal.parse.start", out.ID, "started", "")).Error
	})
	return &out, coreRepo.DBError(err)
}
func (r *Repository) Finalize(ctx context.Context, m service.Meta, v *GoalVersion, parsed Parsed, plan Plan, execute string, parseErr error) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var g Goal
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=?", v.GoalID, m.UserID).First(&g).Error; err != nil {
			return err
		}
		var current GoalVersion
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=?", v.ID, m.UserID).First(&current).Error; err != nil {
			return err
		}
		if current.ParseID != v.ParseID || current.ParseStatus != "parsing" {
			return entity.Fail("VERSION_CONFLICT", "parser attempt has been superseded")
		}
		outcome := "succeeded"
		code := ""
		status := plan.Status
		if parseErr != nil {
			outcome = "failed"
			code = entity.Code(parseErr)
			status = "parse_failed"
			current.ParseStatus = "failed"
		} else {
			current.ParseStatus = "parsed"
			current.ParsedJSON, _ = json.Marshal(parsed)
			raw, _ := json.Marshal(plan)
			a := goalAudit(m, "life_goal.compile", current.ID, "succeeded", "")
			p := TaskPlan{Base: entity.Base{UserID: m.UserID}, GoalID: v.GoalID, GoalVersionID: v.ID, Version: v.Version, Status: plan.Status, PlanJSON: raw, TraceID: m.TraceID, AuditID: a.ID}
			if err := tx.Create(&p).Error; err != nil {
				return err
			}
			if err := tx.Create(a).Error; err != nil {
				return err
			}
			for _, c := range plan.Constraints {
				row := GoalConstraint{Base: entity.Base{UserID: m.UserID}, GoalVersionID: v.ID, Kind: c.Kind, Description: c.Description, Evidence: c.Evidence}
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
			}
			for _, task := range plan.Tasks {
				for _, dep := range task.Dependencies {
					row := TaskDependency{Base: entity.Base{UserID: m.UserID}, PlanID: p.ID, TaskID: task.TaskID, DependsOn: dep}
					if err := tx.Create(&row).Error; err != nil {
						return err
					}
				}
			}
		}
		current.ExecuteID = execute
		current.ErrorCode = code
		current.LeaseUntil = 0
		a := goalAudit(m, "life_goal.parse.complete", v.ID, outcome, code)
		current.AuditID = a.ID
		if err := tx.Save(&current).Error; err != nil {
			return err
		}
		// A late response updates its own historical version, never a newer goal head.
		if err := tx.Model(&Goal{}).Where("id=? AND user_id=? AND current_version=?", v.GoalID, m.UserID, v.Version).Updates(map[string]any{"status": status, "updated_at": time.Now().UnixMilli()}).Error; err != nil {
			return err
		}
		return tx.Create(a).Error
	})
	return coreRepo.DBError(err)
}

type Difference struct {
	Field  string          `json:"field"`
	Before json.RawMessage `json:"before"`
	After  json.RawMessage `json:"after"`
}
type Comparison struct {
	From        int64        `json:"from"`
	To          int64        `json:"to"`
	Differences []Difference `json:"differences"`
}

func (r *Repository) Compare(ctx context.Context, user, id, from, to int64) (Comparison, error) {
	result := Comparison{From: from, To: to, Differences: []Difference{}}
	a, err := r.View(ctx, user, id, from)
	if err != nil {
		return result, err
	}
	b, err := r.View(ctx, user, id, to)
	if err != nil {
		return result, err
	}
	before := map[string]any{"text": a.Version.Text, "financial_context": a.Version.ContextJSON, "parsed": a.Version.ParsedJSON}
	after := map[string]any{"text": b.Version.Text, "financial_context": b.Version.ContextJSON, "parsed": b.Version.ParsedJSON}
	for _, field := range []string{"text", "financial_context", "parsed"} {
		x, _ := json.Marshal(before[field])
		y, _ := json.Marshal(after[field])
		if string(x) != string(y) {
			result.Differences = append(result.Differences, Difference{field, x, y})
		}
	}
	return result, nil
}
