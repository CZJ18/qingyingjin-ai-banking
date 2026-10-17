package recipients

import (
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/phoneid"
	"strings"
	"unicode/utf8"
)

type Settings struct {
	Enabled   bool  `json:"enabled"`
	AccountID int64 `json:"account_id,string"`
	Version   int64 `json:"version"`
}
type SettingsInput struct {
	Enabled         bool  `json:"enabled"`
	AccountID       int64 `json:"account_id,string"`
	ExpectedVersion int64 `json:"expected_version"`
}
type SearchInput struct {
	Kind  string `json:"kind"`
	Query string `json:"query"`
}
type Recipient struct {
	ID                      int64  `json:"id,string"`
	DisplayName             string `json:"display_name"`
	PhoneMasked             string `json:"phone_masked"`
	AccountName             string `json:"account_name"`
	AccountMaskedNumber     string `json:"account_masked_number"`
	Version                 int64  `json:"version"`
	PhoneVerificationMethod string `json:"phone_verification_method"`
}
type SearchResult struct {
	Items   []Recipient `json:"items"`
	Limited bool        `json:"limited"`
}
type PaymentInput struct {
	RecipientID      int64  `json:"recipient_id,string"`
	RecipientVersion int64  `json:"recipient_version"`
	SourceAccountID  int64  `json:"source_account_id,string"`
	AmountMinor      int64  `json:"amount_minor"`
	Description      string `json:"description"`
}

func NormalizeQuery(in SearchInput) (string, error) {
	if in.Kind == "phone" {
		return phoneid.Normalize(in.Query)
	}
	v := strings.TrimSpace(in.Query)
	if in.Kind != "name" || !utf8.ValidString(v) || utf8.RuneCountInString(v) < 2 || len(v) > 128 {
		return "", entity.Fail("INVALID_ARGUMENT", "enter an exact display name (at least two characters) or full phone number")
	}
	return v, nil
}
func ValidatePayment(user int64, in PaymentInput) error {
	if in.RecipientID <= 0 || in.RecipientID == user || in.RecipientVersion < 1 || in.SourceAccountID <= 0 || in.AmountMinor <= 0 || in.AmountMinor > entity.MaxBalance || strings.TrimSpace(in.Description) == "" || len(in.Description) > 512 {
		return entity.Fail("INVALID_ARGUMENT", "positive amount, another recipient, directory version, owned source and transfer description required")
	}
	return nil
}
func Fingerprint(r Recipient) string { raw, _ := json.Marshal(r); return entity.CanonicalJSONHash(raw) }
