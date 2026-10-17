package execution

import (
	"context"
	"crypto/subtle"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/service"
	"gorm.io/gorm"
	"time"
)

func equal(a, b string) bool {
	return a != "" && len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
func (s *Service) Authorize(ctx context.Context, a identity.Actor, m service.Meta, id string, in AuthorizeInput) (out View, err error) {
	if err = permission(a, m, "authorize"); err != nil {
		return out, s.failOperation(ctx, a, m, "AUTHORIZE", id, err)
	}
	if in.Decision != "approve" && in.Decision != "reject" {
		return out, s.failOperation(ctx, a, m, "AUTHORIZE", id, entity.Fail("INVALID_ARGUMENT", "approve or reject decision required"))
	}
	owner, e := s.operationCounterparty(ctx, m.UserID, id)
	if e != nil {
		return out, s.failOperation(ctx, a, m, "AUTHORIZE", id, e)
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := lockOperationUsers(tx, m.UserID, owner); e != nil {
			return e
		}
		op, e := owned(tx, m.UserID, id)
		if e != nil {
			return e
		}
		if !equal(op.IntentDigest, in.IntentDigest) || !equal(op.Nonce, in.ConfirmationNonce) || !equal(op.IntentDigest, entity.OperationDigest(&op)) {
			return entity.Fail("AUTHORIZATION_MISMATCH", "confirmation must match the complete stored intent")
		}
		now := time.Now().UnixMilli()
		if op.ExpiresAt <= now {
			if op.Status == "pending_authorization" || op.Status == "authorized" {
				op.Status = "expired"
				op.ErrorCode = "APPROVAL_EXPIRED"
				op.Version++
				if e := projectTerminal(tx, &op); e != nil {
					return e
				}
				if e := audit(tx, m, actorName(a), &op, op.Status, "AUTHORIZE", op.ErrorCode); e != nil {
					return e
				}
				if e := tx.Save(&op).Error; e != nil {
					return e
				}
			} else if e := audit(tx, m, actorName(a), &op, "authorization_expired", "AUTHORIZE", "APPROVAL_EXPIRED"); e != nil {
				return e
			}
			out, e = view(tx, op)
			return e
		}
		if op.AuthorizationID != "" {
			var saved entity.UserAuthorization
			if e := tx.Where("id = ? AND user_id = ?", op.AuthorizationID, m.UserID).First(&saved).Error; e != nil {
				return e
			}
			if saved.Decision != in.Decision || saved.IntentDigest != op.IntentDigest {
				return entity.Fail("STATE_CONFLICT", "operation already has a different authorization")
			}
			if e := audit(tx, m, actorName(a), &op, "authorization_replay", "AUTHORIZE", ""); e != nil {
				return e
			}
			out, e = view(tx, op)
			return e
		}
		if op.Status != "pending_authorization" {
			return entity.Fail("STATE_CONFLICT", "operation cannot be authorized in current state")
		}
		consent := entity.UserAuthorization{ID: uuid.NewString(), UserID: m.UserID, OperationID: op.ID, Decision: in.Decision, IntentDigest: op.IntentDigest, TokenID: a.TokenID, ExpiresAt: op.ExpiresAt}
		op.AuthorizationID = consent.ID
		op.Status = "authorized"
		if in.Decision == "reject" {
			op.Status = "rejected"
			consent.ConsumedAt = now
		}
		op.Version++
		if e := projectTerminal(tx, &op); e != nil {
			return e
		}
		if e := tx.Create(&consent).Error; e != nil {
			return e
		}
		if e := audit(tx, m, actorName(a), &op, op.Status, "AUTHORIZE", ""); e != nil {
			return e
		}
		if e := tx.Save(&op).Error; e != nil {
			return e
		}
		out, e = view(tx, op)
		return e
	})
	return out, s.failOperation(ctx, a, m, "AUTHORIZE", id, err)
}
