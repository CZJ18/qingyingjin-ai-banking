package aa

import (
	"github.com/qingyingjin/banking-service/internal/entity"
	"strings"
)

const MaxBills = 50
const MaxJoined = 100

type ShareInput struct {
	Name        string `json:"name"`
	AmountMinor int64  `json:"amount_minor"`
}
type Input struct {
	ManagementMode  string       `json:"management_mode,omitempty"`
	PaymentMethod   string       `json:"payment_method,omitempty"`
	RecipientHint   string       `json:"recipient_hint,omitempty"`
	Memo            string       `json:"memo,omitempty"`
	Items           []LineItem   `json:"items,omitempty"`
	Title           string       `json:"title"`
	TotalMinor      int64        `json:"total_minor"`
	Mode            string       `json:"mode"`
	AccountID       int64        `json:"account_id,string"`
	AlreadyPaid     bool         `json:"already_paid"`
	OwnerShareMinor int64        `json:"owner_share_minor"`
	Participants    []ShareInput `json:"participants"`
}
type Share struct {
	External    *ExternalRecord `json:"external,omitempty"`
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	AmountMinor int64           `json:"amount_minor"`
	UserID      int64           `json:"user_id,string"`
	Status      string          `json:"status"`
	OperationID string          `json:"operation_id"`
	PaidAt      int64           `json:"paid_at"`
}
type Bill struct {
	ManagementMode string     `json:"management_mode,omitempty"`
	PaymentMethod  string     `json:"payment_method,omitempty"`
	RecipientHint  string     `json:"recipient_hint,omitempty"`
	Memo           string     `json:"memo,omitempty"`
	Items          []LineItem `json:"items,omitempty"`
	ID             string     `json:"id"`
	OwnerID        int64      `json:"owner_id,string"`
	AccountID      int64      `json:"account_id,string"`
	Title          string     `json:"title"`
	TotalMinor     int64      `json:"total_minor"`
	Mode           string     `json:"mode"`
	Status         string     `json:"status"`
	Version        int64      `json:"version"`
	Invite         string     `json:"invite"`
	Key            string     `json:"key"`
	RequestHash    string     `json:"request_hash"`
	Shares         []Share    `json:"shares"`
	CreatedAt      int64      `json:"created_at"`
	UpdatedAt      int64      `json:"updated_at"`
}
type Reference struct {
	OwnerID int64  `json:"owner_id,string"`
	BillID  string `json:"bill_id"`
}
type Portfolio struct {
	SchemaVersion int         `json:"schema_version"`
	Bills         []Bill      `json:"bills"`
	Joined        []Reference `json:"joined"`
}
type Participant struct {
	ReportedAt  int64  `json:"reported_at"`
	ReceivedAt  int64  `json:"received_at"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	AmountMinor int64  `json:"amount_minor"`
	Status      string `json:"status"`
	IsMe        bool   `json:"is_me"`
	PaidAt      int64  `json:"paid_at"`
	OperationID string `json:"operation_id,omitempty"`
}
type View struct {
	ManagementMode   string        `json:"management_mode,omitempty"`
	PaymentMethod    string        `json:"payment_method"`
	RecipientHint    string        `json:"recipient_hint"`
	Memo             string        `json:"memo"`
	Items            []LineItem    `json:"items"`
	PlatformVerified bool          `json:"platform_verified"`
	ID               string        `json:"id"`
	OwnerID          int64         `json:"owner_id,string"`
	Title            string        `json:"title"`
	TotalMinor       int64         `json:"total_minor"`
	Currency         string        `json:"currency"`
	Mode             string        `json:"mode"`
	Status           string        `json:"status"`
	Version          int64         `json:"version"`
	IsOwner          bool          `json:"is_owner"`
	Invitation       string        `json:"invitation,omitempty"`
	CollectedMinor   int64         `json:"collected_minor"`
	OutstandingMinor int64         `json:"outstanding_minor"`
	Participants     []Participant `json:"participants"`
	CreatedAt        int64         `json:"created_at"`
}
type PaymentInput struct {
	OwnerID         int64  `json:"owner_id,string"`
	BillID          string `json:"bill_id"`
	ParticipantID   string `json:"participant_id"`
	SourceAccountID int64  `json:"source_account_id,string"`
}

func Allocate(in Input) ([]int64, error) {
	bad := func() ([]int64, error) {
		return nil, entity.Fail("INVALID_ARGUMENT", "valid paid AA bill, unique participant names and exact positive cent allocation required")
	}
	count := len(in.Participants) + 1
	if strings.TrimSpace(in.Title) == "" || len(in.Title) > 128 || (method(in.PaymentMethod) == "simulation" && in.AccountID <= 0) || !in.AlreadyPaid || count < 2 || count > 20 || in.TotalMinor < int64(count) || in.TotalMinor > entity.MaxBalance || validateDetails(in) != nil {
		return bad()
	}
	names := map[string]bool{}
	for _, p := range in.Participants {
		n := strings.TrimSpace(p.Name)
		if n == "" || len(n) > 64 || names[n] {
			return bad()
		}
		names[n] = true
	}
	out := make([]int64, count)
	switch in.Mode {
	case "equal":
		if in.OwnerShareMinor != 0 {
			return bad()
		}
		for _, p := range in.Participants {
			if p.AmountMinor != 0 {
				return bad()
			}
		}
		for i := range out {
			out[i] = in.TotalMinor / int64(count)
			if int64(i) < in.TotalMinor%int64(count) {
				out[i]++
			}
		}
	case "custom":
		if in.OwnerShareMinor < 0 || in.OwnerShareMinor > in.TotalMinor {
			return bad()
		}
		out[0] = in.OwnerShareMinor
		sum := out[0]
		for i, p := range in.Participants {
			if p.AmountMinor <= 0 || p.AmountMinor > in.TotalMinor-sum {
				return bad()
			}
			out[i+1] = p.AmountMinor
			sum += p.AmountMinor
		}
		if sum != in.TotalMinor {
			return bad()
		}
	default:
		return bad()
	}
	return out, nil
}
