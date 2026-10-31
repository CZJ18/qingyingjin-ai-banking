package execution

import (
	"context"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/service"
	"gorm.io/gorm"
	"log/slog"
	"strconv"
	"time"
)

// RunScheduled consumes only exact pre-existing human authorizations for local
// SQL simulations. No model/merchant request, grant creation, unknown replay or
// expired consent renewal occurs. Each effect and consent consumption is atomic.
func (s *Service) RunScheduled(ctx context.Context, log *slog.Logger, interval time.Duration) {
	if !s.Configured() {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.RunDue(ctx); err != nil {
				log.Warn("scenario_schedule_check_failed", "code", entity.Code(err))
			}
		}
	}
}
func (s *Service) RunDue(ctx context.Context) error {
	var ops []entity.OperationIntent
	if err := s.db.WithContext(ctx).Where("status='authorized' AND kind IN ?", []string{"scheduled_transfer", "occasion_order"}).Order("created_at,id").Limit(1000).Find(&ops).Error; err != nil {
		return err
	}
	for _, op := range ops {
		due, err := scheduledDue(op)
		if err != nil {
			return err
		}
		if due > time.Now().UnixMilli() {
			continue
		}
		var consent entity.UserAuthorization
		if err := s.db.WithContext(ctx).Where("id=? AND user_id=? AND operation_id=? AND decision='approve' AND consumed_at=0", op.AuthorizationID, op.UserID, op.ID).First(&consent).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				continue
			}
			return err
		}
		if consent.TokenID == "" || consent.IntentDigest != op.IntentDigest {
			continue
		}
		actor := identity.Actor{Kind: "user", UserID: strconv.FormatInt(op.UserID, 10), Subject: "persisted-schedule-consent", TokenID: consent.TokenID, Scopes: []string{"execution:execute"}}
		_, err = s.Execute(ctx, actor, service.Meta{UserID: op.UserID, TraceID: op.TraceID}, op.ID, "scheduled:"+op.ID, ExecuteInput{AuthorizationID: consent.ID, IntentDigest: op.IntentDigest, ExpectedVersion: op.Version})
		if err != nil {
			return err
		}
	}
	return nil
}
func (s *Service) CancelScheduled(ctx context.Context, a identity.Actor, m service.Meta, id string, in AuthorizeInput) (out View, err error) {
	if err = permission(a, m, "authorize"); err != nil {
		return out, err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := lockUser(tx, m.UserID); e != nil {
			return e
		}
		op, e := owned(tx, m.UserID, id)
		if e != nil {
			return e
		}
		due, e := scheduledDue(op)
		if e != nil {
			return e
		}
		if in.Decision != "reject" || due == 0 || op.Status != "authorized" || !equal(op.IntentDigest, in.IntentDigest) || !equal(op.Nonce, in.ConfirmationNonce) {
			return entity.Fail("STATE_CONFLICT", "exact authorized scheduled operation required")
		}
		var consent entity.UserAuthorization
		if e := tx.Where("id=? AND user_id=? AND operation_id=? AND consumed_at=0", op.AuthorizationID, m.UserID, id).First(&consent).Error; e != nil {
			return e
		}
		consent.ConsumedAt = time.Now().UnixMilli()
		op.Status = "rejected"
		op.ErrorCode = "USER_CANCELLED"
		op.Version++
		if e := tx.Save(&consent).Error; e != nil {
			return e
		}
		if e := audit(tx, m, actorName(a), &op, "scheduled_cancelled", "AUTHORIZE", ""); e != nil {
			return e
		}
		if e := tx.Save(&op).Error; e != nil {
			return e
		}
		out, e = view(tx, op)
		return e
	})
	return out, s.failOperation(ctx, a, m, "CANCEL", id, err)
}

type ScenarioState struct {
	Products    []InvestmentProduct      `json:"products"`
	Positions   []entity.BankAccount     `json:"positions"`
	Operations  []entity.OperationIntent `json:"operations"`
	IsSimulated bool                     `json:"is_simulated"`
}

func (s *Service) Scenarios(ctx context.Context, a identity.Actor, m service.Meta) (out ScenarioState, err error) {
	out = ScenarioState{Products: investmentProducts(), Positions: []entity.BankAccount{}, Operations: []entity.OperationIntent{}, IsSimulated: true}
	if a.Kind != "user" {
		return out, entity.Fail("FORBIDDEN", "human scenario access required")
	}
	if err = permission(a, m, "read"); err != nil {
		return out, err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := lockUser(tx, m.UserID); e != nil {
			return e
		}
		if e := tx.Where("user_id=? AND account_type IN ?", m.UserID, []string{"investment", "occasion"}).Order("id").Find(&out.Positions).Error; e != nil {
			return e
		}
		return tx.Where("user_id=? AND kind IN ?", m.UserID, []string{"scheduled_transfer", "investment_assessment", "investment_order", "occasion_plan", "occasion_order", "occasion_release"}).Order("created_at DESC,id").Limit(1000).Find(&out.Operations).Error
	})
	return out, err
}
