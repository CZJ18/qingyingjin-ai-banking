package aa

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"strconv"
	"strings"
	"time"
)

type Service struct{ db *gorm.DB }

func New(db *gorm.DB) *Service { return &Service{db} }
func asView(b Bill, user int64) View {
	v := View{ID: b.ID, OwnerID: b.OwnerID, Title: b.Title, TotalMinor: b.TotalMinor, Currency: "CNY", Mode: b.Mode, Status: b.Status, Version: b.Version, IsOwner: user == b.OwnerID, Participants: []Participant{}, CreatedAt: b.CreatedAt}
	v.PaymentMethod, v.RecipientHint, v.Memo = method(b.PaymentMethod), b.RecipientHint, b.Memo
	v.Items = append([]LineItem{}, b.Items...)
	v.ManagementMode = management(b.ManagementMode)
	if v.IsOwner && v.ManagementMode == "collaborative" {
		v.Invitation = strconv.FormatInt(b.OwnerID, 10) + "." + b.ID + "." + b.Invite
	}
	for i, p := range b.Shares {
		item := Participant{ID: p.ID, Name: p.Name, AmountMinor: p.AmountMinor, Status: p.Status, IsMe: p.UserID == user, PaidAt: p.PaidAt}
		if p.External != nil {
			item.ReportedAt, item.ReceivedAt = p.External.ReportedAt, p.External.ReceivedAt
		}
		if item.IsMe {
			item.OperationID = p.OperationID
		}
		v.Participants = append(v.Participants, item)
		if i > 0 {
			if p.Status == "paid" || p.Status == "receipt_confirmed" {
				v.CollectedMinor += p.AmountMinor
			} else {
				v.OutstandingMinor += p.AmountMinor
			}
		}
	}
	return v
}
func member(b Bill, user int64) bool {
	for _, p := range b.Shares {
		if p.UserID == user {
			return true
		}
	}
	return false
}
func receiving(tx *gorm.DB, user, id int64) (entity.BankAccount, error) {
	var a entity.BankAccount
	e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND user_id=?", id, user).First(&a).Error
	if e == gorm.ErrRecordNotFound {
		return a, entity.Fail("NOT_FOUND", "owned AA account not found")
	}
	if e != nil {
		return a, e
	}
	if !a.IsSimulated || a.Currency != "CNY" || a.SavingsGoalID != nil {
		return a, entity.Fail("INVALID_ARGUMENT", "AA requires a simulated CNY account without a savings goal")
	}
	if a.Status != "active" {
		return a, entity.Fail("ACCOUNT_FROZEN", "AA account must be active")
	}
	return a, nil
}
func keyValid(key string) bool {
	if len(key) < 1 || len(key) > 128 {
		return false
	}
	for _, c := range key {
		if c < 33 || c > 126 {
			return false
		}
	}
	return true
}
func (s *Service) Create(ctx context.Context, user int64, key string, in Input) (out View, err error) {
	amounts, e := Allocate(in)
	if e != nil {
		return out, e
	}
	if !keyValid(key) {
		return out, entity.Fail("INVALID_ARGUMENT", "Idempotency-Key required")
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := LockUsers(tx, user); e != nil {
			return e
		}
		p, e := Load(tx, user)
		if e != nil {
			return e
		}
		rawHash := entity.CanonicalJSONHash(marshal(in))
		for _, b := range p.Bills {
			if b.Key == key {
				if b.RequestHash != rawHash {
					return entity.Fail("IDEMPOTENCY_CONFLICT", "AA key reused for different bill")
				}
				out = asView(b, user)
				return nil
			}
		}
		if len(p.Bills) >= MaxBills {
			return entity.Fail("STATE_CONFLICT", "AA bill capacity reached (50)")
		}
		if method(in.PaymentMethod) == "simulation" {
			if _, e := receiving(tx, user, in.AccountID); e != nil {
				return e
			}
		}
		secret := make([]byte, 32)
		if _, e = rand.Read(secret); e != nil {
			return e
		}
		now := time.Now().UnixMilli()
		b := Bill{ID: uuid.NewString(), OwnerID: user, AccountID: in.AccountID, Title: strings.TrimSpace(in.Title), TotalMinor: in.TotalMinor, Mode: in.Mode, Status: "open", Version: 1, Invite: hex.EncodeToString(secret), Key: key, RequestHash: rawHash, CreatedAt: now, UpdatedAt: now, Shares: []Share{{ID: uuid.NewString(), Name: "发起人（已垫付）", UserID: user, AmountMinor: amounts[0], Status: "self"}}}
		b.ManagementMode = newManagement(in)
		b.PaymentMethod, b.RecipientHint, b.Memo, b.Items = method(in.PaymentMethod), strings.TrimSpace(in.RecipientHint), strings.TrimSpace(in.Memo), in.Items
		shareState := "unclaimed"
		if b.ManagementMode == "single_manager" {
			shareState = "awaiting_receipt"
		}
		for i, item := range in.Participants {
			b.Shares = append(b.Shares, Share{ID: uuid.NewString(), Name: strings.TrimSpace(item.Name), AmountMinor: amounts[i+1], Status: shareState})
		}
		p.Bills = append(p.Bills, b)
		if e = Save(tx, user, p); e != nil {
			return e
		}
		out = asView(b, user)
		return nil
	})
	return
}
func (s *Service) Get(ctx context.Context, user, owner int64, id string) (View, error) {
	p, e := Load(s.db.WithContext(ctx), owner)
	if e != nil {
		return View{}, e
	}
	b, e := Find(&p, id)
	if e != nil {
		return View{}, e
	}
	if !member(*b, user) {
		return View{}, entity.Fail("NOT_FOUND", "AA bill not found")
	}
	return asView(*b, user), nil
}
func (s *Service) List(ctx context.Context, user int64, limit, offset int) ([]View, error) {
	p, e := Load(s.db.WithContext(ctx), user)
	if e != nil {
		return nil, e
	}
	rows := []View{}
	for i := len(p.Bills) - 1; i >= 0; i-- {
		rows = append(rows, asView(p.Bills[i], user))
	}
	for i := len(p.Joined) - 1; i >= 0; i-- {
		r := p.Joined[i]
		v, e := s.Get(ctx, user, r.OwnerID, r.BillID)
		if e != nil {
			return nil, e
		}
		rows = append(rows, v)
	}
	if offset >= len(rows) {
		return []View{}, nil
	}
	end := offset + limit
	if end > len(rows) {
		end = len(rows)
	}
	return rows[offset:end], nil
}
func parseInvitation(inv string) (int64, string, string, error) {
	parts := strings.Split(inv, ".")
	if len(parts) != 3 {
		return 0, "", "", entity.Fail("INVALID_ARGUMENT", "invalid AA invitation")
	}
	owner, e := strconv.ParseInt(parts[0], 10, 64)
	code, e2 := hex.DecodeString(parts[2])
	if e != nil || owner <= 0 || strconv.FormatInt(owner, 10) != parts[0] || uuid.Validate(parts[1]) != nil || e2 != nil || len(code) != 32 {
		return 0, "", "", entity.Fail("INVALID_ARGUMENT", "invalid AA invitation")
	}
	return owner, parts[1], parts[2], nil
}
func invitationBill(tx *gorm.DB, owner int64, id, code string) (Portfolio, *Bill, error) {
	p, e := Load(tx, owner)
	if e != nil {
		return p, nil, e
	}
	b, e := Find(&p, id)
	if e != nil {
		return p, nil, e
	}
	if subtle.ConstantTimeCompare([]byte(b.Invite), []byte(code)) != 1 {
		return p, nil, entity.Fail("NOT_FOUND", "invitation not found")
	}
	if management(b.ManagementMode) != "collaborative" {
		return p, nil, entity.Fail("STATE_CONFLICT", "single-manager bills do not accept invitations")
	}
	if b.Status != "open" {
		return p, nil, entity.Fail("STATE_CONFLICT", "AA bill no longer accepts participants")
	}
	return p, b, nil
}
func (s *Service) Preview(ctx context.Context, user int64, inv string) (View, error) {
	owner, id, code, e := parseInvitation(inv)
	if e != nil {
		return View{}, e
	}
	_, b, e := invitationBill(s.db.WithContext(ctx), owner, id, code)
	if e != nil {
		return View{}, e
	}
	return asView(*b, user), nil
}
func (s *Service) Join(ctx context.Context, user int64, inv, participant string) (out View, err error) {
	owner, id, code, e := parseInvitation(inv)
	if e != nil {
		return out, e
	}
	if uuid.Validate(participant) != nil || owner == user {
		return out, entity.Fail("INVALID_ARGUMENT", "select another participant's unclaimed slot")
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := LockUsers(tx, owner, user); e != nil {
			return e
		}
		p, b, e := invitationBill(tx, owner, id, code)
		if e != nil {
			return e
		}
		var chosen *Share
		for i := range b.Shares {
			item := &b.Shares[i]
			if item.UserID == user && item.ID != participant {
				return entity.Fail("STATE_CONFLICT", "one participant per user per bill")
			}
			if item.ID == participant {
				chosen = item
			}
		}
		if chosen == nil {
			return entity.Fail("NOT_FOUND", "participant not found")
		}
		if chosen.UserID == user {
			out = asView(*b, user)
			return nil
		}
		if chosen.UserID != 0 || chosen.Status != "unclaimed" {
			return entity.Fail("STATE_CONFLICT", "participant already claimed")
		}
		mine, e := Load(tx, user)
		if e != nil {
			return e
		}
		if len(mine.Joined) >= MaxJoined {
			return entity.Fail("STATE_CONFLICT", "joined AA capacity reached (100)")
		}
		chosen.UserID = user
		chosen.Status = "joined"
		b.Version++
		b.UpdatedAt = time.Now().UnixMilli()
		mine.Joined = append(mine.Joined, Reference{OwnerID: owner, BillID: id})
		if e = Save(tx, owner, p); e != nil {
			return e
		}
		if e = Save(tx, user, mine); e != nil {
			return e
		}
		out = asView(*b, user)
		return nil
	})
	return
}
func (s *Service) Cancel(ctx context.Context, user int64, id string, version int64) (out View, err error) {
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if e := LockUsers(tx, user); e != nil {
			return e
		}
		p, e := Load(tx, user)
		if e != nil {
			return e
		}
		b, e := Find(&p, id)
		if e != nil {
			return e
		}
		if b.Status == "cancelled" {
			out = asView(*b, user)
			return nil
		}
		if b.Version != version {
			return entity.Fail("VERSION_CONFLICT", "AA bill changed")
		}
		if b.Status != "open" {
			return entity.Fail("STATE_CONFLICT", "only open AA bills may be cancelled")
		}
		b.Status = "cancelled"
		b.Version++
		b.UpdatedAt = time.Now().UnixMilli()
		if e = Save(tx, user, p); e != nil {
			return e
		}
		out = asView(*b, user)
		return nil
	})
	return
}
