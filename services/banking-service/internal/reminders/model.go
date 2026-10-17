// Package reminders generates advisory notices only. It never renews or debits funds.
package reminders

import (
	"github.com/qingyingjin/banking-service/internal/entity"
	"sort"
	"time"
)

const MaxNotices = 200

var shanghai = time.FixedZone("Asia/Shanghai", 8*3600)

type Settings struct {
	Enabled  bool  `json:"enabled"`
	LeadDays int   `json:"lead_days"`
	Version  int64 `json:"version"`
}
type Notice struct {
	ID             string `json:"id"`
	SubscriptionID int64  `json:"subscription_id,string"`
	MerchantName   string `json:"merchant_name"`
	AmountMinor    int64  `json:"amount_minor"`
	Currency       string `json:"currency"`
	ChargeAt       int64  `json:"charge_at"`
	Status         string `json:"status"`
	Reason         string `json:"reason"`
	Version        int64  `json:"version"`
	ReadAt         int64  `json:"read_at"`
	CreatedAt      int64  `json:"created_at"`
	UpdatedAt      int64  `json:"updated_at"`
}
type State struct {
	SchemaVersion   int      `json:"schema_version"`
	Settings        Settings `json:"settings"`
	Items           []Notice `json:"items"`
	LastCheckedAt   int64    `json:"last_checked_at"`
	CapacityLimited bool     `json:"capacity_limited"`
}
type View struct {
	Settings        Settings `json:"settings"`
	Items           []Notice `json:"items"`
	LastCheckedAt   int64    `json:"last_checked_at"`
	CapacityLimited bool     `json:"capacity_limited"`
	UnreadCount     int      `json:"unread_count"`
}

func Default() State {
	return State{SchemaVersion: 1, Settings: Settings{Enabled: true, LeadDays: 3, Version: 1}, Items: []Notice{}}
}
func DefaultWithLead(days int) State { s := Default(); s.Settings.LeadDays = days; return s }
func AsView(s State) View {
	v := View{Settings: s.Settings, Items: s.Items, LastCheckedAt: s.LastCheckedAt, CapacityLimited: s.CapacityLimited}
	for _, n := range s.Items {
		if n.Status == "unread" {
			v.UnreadCount++
		}
	}
	return v
}
func cutoff(now time.Time, days int) int64 {
	local := now.In(shanghai)
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, shanghai).AddDate(0, 0, days+1).UnixMilli()
}
func sourceValid(sub entity.Subscription) bool {
	return sub.Status == "active" && sub.IsSimulated && sub.Currency == "CNY" && sub.AmountMinor > 0 && sub.AmountMinor <= entity.MaxBalance
}
func Reconcile(s State, subscriptions []entity.Subscription, now time.Time, newID func() string) (State, error) {
	if s.Settings.LeadDays < 0 || s.Settings.LeadDays > 30 {
		return s, entity.Fail("INVALID_ARGUMENT", "lead_days must be 0..30")
	}
	out := s
	out.Items = append([]Notice{}, s.Items...)
	out.CapacityLimited = false
	out.LastCheckedAt = now.UnixMilli()
	sources := map[int64]entity.Subscription{}
	for _, sub := range subscriptions {
		sources[sub.ID] = sub
	}
	retained := []Notice{}
	for _, n := range out.Items {
		if n.ChargeAt >= now.AddDate(0, 0, -30).UnixMilli() {
			retained = append(retained, n)
		}
	}
	out.Items = retained
	for i, n := range out.Items {
		old := n
		sub, exists := sources[n.SubscriptionID]
		reason := ""
		switch {
		case !exists || !sourceValid(sub) || sub.NextBillingAt != n.ChargeAt:
			reason = "subscription_changed"
		case n.ChargeAt < now.UnixMilli():
			reason = "billing_time_passed"
		case !s.Settings.Enabled:
			reason = "disabled"
		case n.ChargeAt >= cutoff(now, s.Settings.LeadDays):
			reason = "outside_window"
		}
		if reason != "" {
			n.Status = "withdrawn"
			if reason == "billing_time_passed" {
				n.Status = "expired"
			}
			n.Reason = reason
		} else {
			if n.AmountMinor != sub.AmountMinor || n.MerchantName != sub.MerchantName {
				n.ReadAt = 0
			}
			n.AmountMinor = sub.AmountMinor
			n.MerchantName = sub.MerchantName
			n.Reason = ""
			n.Status = "unread"
			if n.ReadAt > 0 {
				n.Status = "read"
			}
		}
		if n != old {
			n.Version++
			n.UpdatedAt = now.UnixMilli()
		}
		out.Items[i] = n
	}
	if s.Settings.Enabled {
		ordered := append([]entity.Subscription{}, subscriptions...)
		sort.Slice(ordered, func(i, j int) bool {
			if ordered[i].NextBillingAt == ordered[j].NextBillingAt {
				return ordered[i].ID < ordered[j].ID
			}
			return ordered[i].NextBillingAt < ordered[j].NextBillingAt
		})
		for _, sub := range ordered {
			if !sourceValid(sub) || sub.NextBillingAt < now.UnixMilli() || sub.NextBillingAt >= cutoff(now, s.Settings.LeadDays) {
				continue
			}
			found := false
			for _, n := range out.Items {
				if n.SubscriptionID == sub.ID && n.ChargeAt == sub.NextBillingAt {
					found = true
					break
				}
			}
			if found {
				continue
			}
			if len(out.Items) >= MaxNotices {
				out.CapacityLimited = true
				continue
			}
			out.Items = append(out.Items, Notice{ID: newID(), SubscriptionID: sub.ID, MerchantName: sub.MerchantName, AmountMinor: sub.AmountMinor, Currency: "CNY", ChargeAt: sub.NextBillingAt, Status: "unread", Version: 1, CreatedAt: now.UnixMilli(), UpdatedAt: now.UnixMilli()})
		}
	}
	sort.SliceStable(out.Items, func(i, j int) bool {
		if out.Items[i].CreatedAt == out.Items[j].CreatedAt {
			return out.Items[i].ID < out.Items[j].ID
		}
		return out.Items[i].CreatedAt > out.Items[j].CreatedAt
	})
	return out, nil
}
func MarkRead(s State, id string, expected int64, now time.Time) (State, error) {
	out := s
	out.Items = append([]Notice{}, s.Items...)
	for i, n := range out.Items {
		if n.ID != id {
			continue
		}
		if n.Status == "read" {
			return out, nil
		}
		if n.Version != expected {
			return s, entity.Fail("VERSION_CONFLICT", "notification changed; refresh before marking read")
		}
		if n.Status != "unread" {
			return s, entity.Fail("STATE_CONFLICT", "notice is no longer actionable")
		}
		n.Status = "read"
		n.ReadAt = now.UnixMilli()
		n.UpdatedAt = now.UnixMilli()
		n.Version++
		out.Items[i] = n
		return out, nil
	}
	return s, entity.Fail("NOT_FOUND", "notification not found")
}
