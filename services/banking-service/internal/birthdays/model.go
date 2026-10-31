// Package birthdays keeps only explicitly supplied calendar dates. Reads never
// create financial intents, authorizations, reserves or merchant orders.
package birthdays

import (
	"math"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
)

type Entry struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	MonthDay string `json:"month_day"`
}
type State struct {
	SchemaVersion int     `json:"schema_version"`
	Version       int64   `json:"version"`
	Enabled       bool    `json:"enabled"`
	LeadDays      int     `json:"lead_days"`
	Entries       []Entry `json:"entries"`
}
type Input struct {
	ExpectedVersion int64   `json:"expected_version"`
	Enabled         *bool   `json:"enabled"`
	LeadDays        *int    `json:"lead_days"`
	Entries         []Entry `json:"entries"`
}
type Upcoming struct {
	Entry
	NextDate  string `json:"next_date"`
	DaysUntil int    `json:"days_until"`
}
type View struct {
	Version       int64      `json:"version"`
	Enabled       bool       `json:"enabled"`
	LeadDays      int        `json:"lead_days"`
	Entries       []Entry    `json:"entries"`
	Upcoming      []Upcoming `json:"upcoming"`
	Timezone      string     `json:"timezone"`
	LeapDayPolicy string     `json:"leap_day_policy"`
}

func Default() State {
	return State{SchemaVersion: 1, Version: 1, Enabled: true, LeadDays: 30, Entries: []Entry{}}
}
func Validate(s State) error {
	if s.SchemaVersion != 1 || s.Version < 1 || s.Version == math.MaxInt64 || s.LeadDays < 0 || s.LeadDays > 30 || s.Entries == nil || len(s.Entries) > 50 {
		return entity.Fail("INVALID_ARGUMENT", "explicit calendar list (max50), lead_days0..30 and version required")
	}
	seen := map[string]bool{}
	for _, e := range s.Entries {
		id, err := uuid.Parse(e.ID)
		date, dateErr := time.Parse("2006-01-02", "2000-"+e.MonthDay)
		if err != nil || id.String() != e.ID || seen[e.ID] || !utf8.ValidString(e.Name) || strings.TrimSpace(e.Name) != e.Name || e.Name == "" || utf8.RuneCountInString(e.Name) > 32 || strings.IndexFunc(e.Name, unicode.IsControl) >= 0 || dateErr != nil || date.Format("01-02") != e.MonthDay {
			return entity.Fail("INVALID_ARGUMENT", "unique UUID, explicit name and exact MM-DD birthday required")
		}
		seen[e.ID] = true
	}
	return nil
}
func Replace(old State, in Input) (State, error) {
	if in.Enabled == nil || in.LeadDays == nil || in.ExpectedVersion < 1 {
		return State{}, entity.Fail("INVALID_ARGUMENT", "enabled,lead_days,entries and expected_version required")
	}
	if old.Version != in.ExpectedVersion {
		return State{}, entity.Fail("VERSION_CONFLICT", "birthday calendar changed; reload before saving")
	}
	next := State{SchemaVersion: 1, Version: old.Version + 1, Enabled: *in.Enabled, LeadDays: *in.LeadDays, Entries: in.Entries}
	return next, Validate(next)
}
func AsView(s State, now time.Time) (View, error) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return View{}, entity.Fail("DEPENDENCY_UNAVAILABLE", "Shanghai timezone unavailable")
	}
	v := View{Version: s.Version, Enabled: s.Enabled, LeadDays: s.LeadDays, Entries: s.Entries, Upcoming: []Upcoming{}, Timezone: "Asia/Shanghai", LeapDayPolicy: "next_leap_year"}
	if !s.Enabled {
		return v, nil
	}
	now = now.In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	for _, e := range s.Entries {
		base, parseErr := time.Parse("2006-01-02", "2000-"+e.MonthDay)
		if parseErr != nil {
			return View{}, entity.Fail("DEPENDENCY_UNAVAILABLE", "invalid saved birthday")
		}
		for year := today.Year(); year <= today.Year()+8; year++ {
			due := time.Date(year, base.Month(), base.Day(), 0, 0, 0, 0, time.UTC)
			if due.Month() != base.Month() || due.Before(today) {
				continue
			}
			days := int(due.Sub(today) / (24 * time.Hour))
			if days <= s.LeadDays {
				v.Upcoming = append(v.Upcoming, Upcoming{Entry: e, NextDate: due.Format("2006-01-02"), DaysUntil: days})
			}
			break
		}
	}
	sort.Slice(v.Upcoming, func(i, j int) bool {
		if v.Upcoming[i].NextDate == v.Upcoming[j].NextDate {
			return v.Upcoming[i].ID < v.Upcoming[j].ID
		}
		return v.Upcoming[i].NextDate < v.Upcoming[j].NextDate
	})
	return v, nil
}
