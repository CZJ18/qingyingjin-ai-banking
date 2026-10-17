package cards

import (
	"database/sql"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"gorm.io/gorm"
	"time"
)

func Read(db *gorm.DB, user int64) ([]Card, error) {
	var raw string
	e := db.Model(&entity.User{}).Select("COALESCE(card_portfolio, '[]')").Where("id = ?", user).Row().Scan(&raw)
	if e == sql.ErrNoRows {
		return nil, entity.Fail("NOT_FOUND", "user not found")
	}
	if e != nil {
		return nil, e
	}
	out := []Card{}
	if json.Unmarshal([]byte(raw), &out) != nil || out == nil || len(out) > 20 {
		return nil, entity.Fail("DEPENDENCY_UNAVAILABLE", "invalid persisted card portfolio")
	}
	defaults := 0
	seen := map[string]bool{}
	for i, c := range out {
		if uuid.Validate(c.ID) != nil || seen[c.ID] || Validate(Input{Action: "bind", AccountID: c.AccountID, Issuer: c.Issuer, Name: c.Name, LastFour: c.LastFour, CardType: c.CardType, CreditLimitMinor: c.CreditLimitMinor}) != nil || c.CreditUsedMinor < 0 || c.CreditUsedMinor > c.CreditLimitMinor || !c.IsSimulated || c.Currency != "CNY" || (c.Status != "active" && c.Status != "unbound") || c.Status == "unbound" && c.Default {
			return nil, entity.Fail("DEPENDENCY_UNAVAILABLE", "invalid persisted card portfolio")
		}
		seen[c.ID] = true
		out[i].CreditAvailableMinor = c.CreditLimitMinor - c.CreditUsedMinor
		if c.Default {
			defaults++
		}
	}
	if defaults > 1 {
		return nil, entity.Fail("DEPENDENCY_UNAVAILABLE", "invalid persisted card defaults")
	}
	return out, nil
}
func Save(tx *gorm.DB, user int64, portfolio []Card) error {
	raw, e := json.Marshal(portfolio)
	if e != nil {
		return e
	}
	result := tx.Model(&entity.User{}).Where("id = ?", user).Updates(map[string]any{"card_portfolio": string(raw), "updated_at": time.Now().UnixMilli()})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected != 1 {
		return entity.Fail("STATE_CONFLICT", "card owner changed")
	}
	return nil
}
