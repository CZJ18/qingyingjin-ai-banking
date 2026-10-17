package aa

import (
	"database/sql"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	"sort"
)

func LockUsers(tx *gorm.DB, ids ...int64) error {
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	var prior int64
	for _, id := range ids {
		if id <= 0 {
			return entity.Fail("INVALID_ARGUMENT", "positive owner ID required")
		}
		if id == prior {
			continue
		}
		prior = id
		var u entity.User
		if e := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id=? AND status=?", id, "active").First(&u).Error; e != nil {
			if e == gorm.ErrRecordNotFound {
				return entity.Fail("NOT_FOUND", "active AA user not found")
			}
			return e
		}
	}
	return nil
}
func Load(db *gorm.DB, user int64) (Portfolio, error) {
	var raw sql.NullString
	e := db.Model(&entity.User{}).Where("id=?", user).Select("aa_portfolio").Row().Scan(&raw)
	if e != nil {
		if e == sql.ErrNoRows {
			return Portfolio{}, entity.Fail("NOT_FOUND", "AA owner not found")
		}
		return Portfolio{}, e
	}
	if !raw.Valid {
		return Portfolio{SchemaVersion: 1, Bills: []Bill{}, Joined: []Reference{}}, nil
	}
	var p Portfolio
	if len(raw.String) > 4<<20 || json.Unmarshal([]byte(raw.String), &p) != nil || p.SchemaVersion != 1 || p.Bills == nil || p.Joined == nil || len(p.Bills) > MaxBills || len(p.Joined) > MaxJoined {
		return Portfolio{}, entity.Fail("DEPENDENCY_UNAVAILABLE", "invalid persisted AA portfolio")
	}
	seen := map[string]bool{}
	for _, b := range p.Bills {
		if uuid.Validate(b.ID) != nil || seen[b.ID] || b.OwnerID != user || (method(b.PaymentMethod) == "simulation" && b.AccountID <= 0) || validateDetails(Input{ManagementMode: b.ManagementMode, PaymentMethod: b.PaymentMethod, AccountID: b.AccountID, RecipientHint: b.RecipientHint, Memo: b.Memo, Items: b.Items, TotalMinor: b.TotalMinor}) != nil || b.Version < 1 || len(b.Shares) < 2 || len(b.Shares) > 20 || b.TotalMinor <= 0 || b.TotalMinor > entity.MaxBalance || len(b.Invite) != 64 || (b.Status != "open" && b.Status != "settled" && b.Status != "cancelled") {
			return Portfolio{}, entity.Fail("DEPENDENCY_UNAVAILABLE", "invalid persisted AA bill")
		}
		seen[b.ID] = true
		var sum int64
		members := map[int64]bool{}
		shareIDs := map[string]bool{}
		for i, s := range b.Shares {
			if uuid.Validate(s.ID) != nil || shareIDs[s.ID] || s.AmountMinor < 0 || s.AmountMinor > b.TotalMinor-sum || (i == 0 && (s.UserID != user || s.Status != "self")) || (i > 0 && (s.AmountMinor == 0 || !validShareState(b, s))) || (s.Status == "unclaimed" && s.UserID != 0) || ((i == 0 || management(b.ManagementMode) == "collaborative") && s.Status != "unclaimed" && (s.UserID <= 0 || members[s.UserID])) || (s.Status == "paid" && (uuid.Validate(s.OperationID) != nil || s.PaidAt <= 0)) {
				return Portfolio{}, entity.Fail("DEPENDENCY_UNAVAILABLE", "invalid persisted AA share")
			}
			sum += s.AmountMinor
			shareIDs[s.ID] = true
			if s.UserID > 0 {
				members[s.UserID] = true
			}
		}
		if sum != b.TotalMinor {
			return Portfolio{}, entity.Fail("DEPENDENCY_UNAVAILABLE", "AA shares do not balance")
		}
	}
	for _, r := range p.Joined {
		if r.OwnerID <= 0 || r.OwnerID == user || uuid.Validate(r.BillID) != nil {
			return Portfolio{}, entity.Fail("DEPENDENCY_UNAVAILABLE", "invalid AA membership reference")
		}
	}
	return p, nil
}
func Save(tx *gorm.DB, user int64, p Portfolio) error {
	raw, e := json.Marshal(p)
	if e != nil {
		return e
	}
	if len(raw) > 4<<20 {
		return entity.Fail("STATE_CONFLICT", "AA portfolio capacity reached")
	}
	return tx.Model(&entity.User{}).Where("id=?", user).UpdateColumn("aa_portfolio", string(raw)).Error
}
func Find(p *Portfolio, id string) (*Bill, error) {
	if uuid.Validate(id) != nil {
		return nil, entity.Fail("INVALID_ARGUMENT", "bill UUID required")
	}
	for i := range p.Bills {
		if p.Bills[i].ID == id {
			return &p.Bills[i], nil
		}
	}
	return nil, entity.Fail("NOT_FOUND", "AA bill not found")
}
