package execution

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/service"
	"github.com/qingyingjin/banking-service/internal/subscription"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strconv"
	"time"
)

type Service struct {
	db        *gorm.DB
	simulator subscription.Simulator
}

func New(db *gorm.DB, simulator subscription.Simulator) *Service {
	if simulator == nil {
		simulator = subscription.LocalSimulator{}
	}
	return &Service{db: db, simulator: simulator}
}
func (s *Service) Configured() bool { return s != nil && s.db != nil }
func permission(a identity.Actor, m service.Meta, p string) error {
	n, e := strconv.ParseInt(a.UserID, 10, 64)
	if e != nil || n <= 0 || n != m.UserID || strconv.FormatInt(n, 10) != a.UserID {
		return entity.Fail("FORBIDDEN", "owned authenticated identity required")
	}
	if p == "authorize" || p == "execute" {
		if a.Kind != "user" || a.TokenID == "" {
			return entity.Fail("FORBIDDEN", "explicit authenticated human authorization required")
		}
	}
	for _, scope := range a.Scopes {
		if scope == "execution:"+p {
			return nil
		}
	}
	return entity.Fail("FORBIDDEN", "execution permission required")
}
func lockUser(tx *gorm.DB, user int64) error {
	var u entity.User
	e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND status = ?", user, "active").First(&u).Error
	if e == gorm.ErrRecordNotFound {
		return entity.Fail("FORBIDDEN", "active owner required")
	}
	return e
}
func owned(tx *gorm.DB, user int64, id string) (entity.OperationIntent, error) {
	var op entity.OperationIntent
	e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", id, user).First(&op).Error
	if e == gorm.ErrRecordNotFound {
		e = entity.Fail("NOT_FOUND", "operation not found")
	}
	return op, e
}
func view(tx *gorm.DB, op entity.OperationIntent) (View, error) {
	v := View{Operation: op, Authorizations: []entity.UserAuthorization{}, RiskEvents: []entity.RiskEvent{}}
	if e := tx.Where("operation_id = ? AND user_id = ?", op.ID, op.UserID).Order("created_at, id").Find(&v.Authorizations).Error; e != nil {
		return v, e
	}
	e := tx.Where("operation_id = ? AND user_id = ?", op.ID, op.UserID).Order("created_at, id").Find(&v.RiskEvents).Error
	return v, e
}
func audit(tx *gorm.DB, m service.Meta, agent string, op *entity.OperationIntent, state, permission, code string) error {
	ev := entity.AuditLog{ID: uuid.NewString(), UserID: &m.UserID, Agent: agent, Permission: permission, State: state, ErrorCode: code, TraceID: m.TraceID, TaskID: m.TaskID, CreatedAt: time.Now().UnixMilli()}
	if op != nil {
		ev.OperationID = op.ID
		ev.OperationType = op.Kind
		ev.AuthorizationID = op.AuthorizationID
		ev.AuthorizationDigest = op.IntentDigest
		ev.RiskLevel = op.RiskLevel
		op.AuditID = ev.ID
	}
	return tx.Create(&ev).Error
}
func (s *Service) Denied(ctx context.Context, a *identity.Actor, m service.Meta, p, code string) error {
	if !s.Configured() {
		return entity.Fail("DEPENDENCY_UNAVAILABLE", "execution audit unavailable")
	}
	ev := entity.AuditLog{ID: uuid.NewString(), Permission: p, State: "denied", ErrorCode: code, TraceID: m.TraceID, TaskID: m.TaskID, CreatedAt: time.Now().UnixMilli()}
	if a != nil {
		ev.Agent = actorName(*a)
		var u entity.User
		if e := s.db.WithContext(ctx).Where("id = ?", m.UserID).First(&u).Error; e == nil {
			ev.UserID = &m.UserID
		} else if !errors.Is(e, gorm.ErrRecordNotFound) {
			return entity.Fail("DEPENDENCY_UNAVAILABLE", "execution audit unavailable")
		}
	}
	if e := s.db.WithContext(ctx).Create(&ev).Error; e != nil {
		return entity.Fail("DEPENDENCY_UNAVAILABLE", "execution audit unavailable")
	}
	return nil
}

// DeniedOperation links malformed or forbidden requests to an owned intent only.
// Foreign identifiers never become visible in the caller's audit history.
func (s *Service) DeniedOperation(ctx context.Context, a identity.Actor, m service.Meta, p, id, code string) error {
	err := s.failOperation(ctx, a, m, p, id, entity.Fail(code, "execution request denied"))
	if entity.Code(err) == code {
		return nil
	}
	return err
}
func actorName(a identity.Actor) string {
	if a.AgentType != "" {
		return a.AgentType
	}
	return a.Kind + ":" + a.Subject
}
func (s *Service) fail(ctx context.Context, a identity.Actor, m service.Meta, p string, e error) error {
	if e == nil {
		return nil
	}
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
	}
	if ae := s.Denied(ctx, &a, m, p, entity.Code(e)); ae != nil {
		return ae
	}
	return e
}

func (s *Service) failOperation(ctx context.Context, a identity.Actor, m service.Meta, p, id string, e error) error {
	if e == nil {
		return nil
	}
	if !s.Configured() || a.UserID != strconv.FormatInt(m.UserID, 10) {
		return s.fail(ctx, a, m, p, e)
	}
	if ctx.Err() != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
	}
	var op entity.OperationIntent
	queryErr := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", id, m.UserID).First(&op).Error
	if queryErr != nil {
		return s.fail(ctx, a, m, p, e)
	}
	if ae := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := riskEvent(tx, &op, "authorization_boundary", "deny", entity.Code(e), map[string]any{"permission": p}); e != nil {
			return e
		}
		return audit(tx, m, actorName(a), &op, "denied", p, entity.Code(e))
	}); ae != nil {
		return entity.Fail("DEPENDENCY_UNAVAILABLE", "execution audit unavailable")
	}
	return e
}
func (s *Service) Get(ctx context.Context, a identity.Actor, m service.Meta, id string) (v View, e error) {
	if e = permission(a, m, "read"); e != nil {
		return v, s.fail(ctx, a, m, "READ", e)
	}
	e = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := lockUser(tx, m.UserID); e != nil {
			return e
		}
		op, e := owned(tx, m.UserID, id)
		if e != nil {
			return e
		}
		v, e = view(tx, op)
		return e
	})
	return v, s.fail(ctx, a, m, "READ", e)
}
func (s *Service) List(ctx context.Context, a identity.Actor, m service.Meta, kind string, resourceID int64) (out []entity.OperationIntent, e error) {
	out = []entity.OperationIntent{}
	if e = permission(a, m, "read"); e != nil {
		return out, s.fail(ctx, a, m, "READ", e)
	}
	if kind != "" && !kindValid(kind) {
		return out, s.fail(ctx, a, m, "READ", entity.Fail("INVALID_ARGUMENT", "invalid operation kind"))
	}
	e = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := lockUser(tx, m.UserID); e != nil {
			return e
		}
		q := tx.Where("user_id = ?", m.UserID)
		if kind != "" {
			q = q.Where("kind = ?", kind)
		}
		if resourceID > 0 {
			q = q.Where("resource_id = ?", resourceID)
		}
		return q.Order("created_at DESC, id").Limit(100).Find(&out).Error
	})
	return out, s.fail(ctx, a, m, "READ", e)
}
func (s *Service) Rules(ctx context.Context, a identity.Actor, m service.Meta) (out []entity.RiskRule, e error) {
	out = []entity.RiskRule{}
	if e = permission(a, m, "read"); e != nil {
		return out, s.fail(ctx, a, m, "READ", e)
	}
	e = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := lockUser(tx, m.UserID); e != nil {
			return e
		}
		return tx.Order("name").Find(&out).Error
	})
	return out, s.fail(ctx, a, m, "READ", e)
}
func (s *Service) Audits(ctx context.Context, a identity.Actor, m service.Meta, id string) (out []entity.AuditLog, e error) {
	out = []entity.AuditLog{}
	if _, e = s.Get(ctx, a, m, id); e != nil {
		return out, e
	}
	e = s.db.WithContext(ctx).Where("user_id = ? AND operation_id = ?", m.UserID, id).Order("created_at, id").Find(&out).Error
	return out, s.fail(ctx, a, m, "READ", e)
}
