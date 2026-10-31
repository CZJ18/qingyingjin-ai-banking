package recipients

import (
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/phoneid"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Settings struct {
	Enabled   bool              `json:"enabled"`
	AccountID int64             `json:"account_id,string"`
	Version   int64             `json:"version"`
	Remarks   map[string]string `json:"remarks,omitempty"`
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

// Lookup proves the selected recipient was found by exact name or phone.
// A private remark never grants access to an arbitrary recipient ID.
type RemarkInput struct {
	Remark           string      `json:"remark"`
	RecipientID      int64       `json:"recipient_id,string"`
	RecipientVersion int64       `json:"recipient_version"`
	ExpectedVersion  int64       `json:"expected_version"`
	Lookup           SearchInput `json:"lookup"`
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
	if in.Kind == "remark" {
		return normalizeRemark(v)
	}
	if in.Kind != "name" || !utf8.ValidString(v) || utf8.RuneCountInString(v) < 2 || len(v) > 128 {
		return "", entity.Fail("INVALID_ARGUMENT", "enter an exact display name (at least two characters) or full phone number")
	}
	return v, nil
}

func normalizeRemark(v string) (string, error) {
	v = strings.TrimSpace(v)
	if !utf8.ValidString(v) || utf8.RuneCountInString(v) < 1 || utf8.RuneCountInString(v) > 32 || len(v) > 128 || strings.IndexFunc(v, unicode.IsControl) >= 0 {
		return "", entity.Fail("INVALID_ARGUMENT", "private exact remark must contain 1..32 visible characters")
	}
	return v, nil
}
func validRemarks(remarks map[string]string) bool {
	if len(remarks) > 20 {
		return false
	}
	for key, value := range remarks {
		label, err := normalizeRemark(key)
		id, idErr := strconv.ParseInt(value, 10, 64)
		if err != nil || label != key || idErr != nil || id <= 0 || strconv.FormatInt(id, 10) != value {
			return false
		}
	}
	return true
}
func ValidatePayment(user int64, in PaymentInput) error {
	if in.RecipientID <= 0 || in.RecipientID == user || in.RecipientVersion < 1 || in.SourceAccountID <= 0 || in.AmountMinor <= 0 || in.AmountMinor > entity.MaxBalance || strings.TrimSpace(in.Description) == "" || len(in.Description) > 512 {
		return entity.Fail("INVALID_ARGUMENT", "positive amount, another recipient, directory version, owned source and transfer description required")
	}
	return nil
}
func Fingerprint(r Recipient) string { raw, _ := json.Marshal(r); return entity.CanonicalJSONHash(raw) }
