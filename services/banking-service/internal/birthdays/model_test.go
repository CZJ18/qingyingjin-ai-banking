package birthdays

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/qingyingjin/banking-service/internal/entity"
)

func fixture() State {
	s := Default()
	s.Entries = []Entry{{ID: "94b27a56-5355-400e-8c17-fce6643da4b4", Name: "明确填写的对象", MonthDay: "11-01"}}
	return s
}

func TestShanghaiCalendarWindowAndAnnualRecurrence(t *testing.T) {
	s := fixture()
	for _, tc := range []struct {
		now, date   string
		days, count int
	}{
		{"2026-10-01T15:59:59Z", "", 0, 0},
		{"2026-10-01T16:00:00Z", "2026-11-01", 30, 1},
		{"2026-10-31T16:00:00Z", "2026-11-01", 0, 1},
		{"2026-11-01T16:00:00Z", "", 0, 0},
		{"2027-10-31T16:00:00Z", "2027-11-01", 0, 1},
	} {
		now, _ := time.Parse(time.RFC3339, tc.now)
		v, err := AsView(s, now)
		if err != nil || len(v.Upcoming) != tc.count {
			t.Fatal(tc, v, err)
		}
		if tc.count > 0 && (v.Upcoming[0].NextDate != tc.date || v.Upcoming[0].DaysUntil != tc.days) {
			t.Fatal(tc, v)
		}
	}
	s.Enabled = false
	v, err := AsView(s, time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC))
	if err != nil || len(v.Upcoming) != 0 || len(v.Entries) != 1 {
		t.Fatal(v, err)
	}
}

func TestLeapDayOnlyUsesNextActualLeapYearAndReadDoesNotMutate(t *testing.T) {
	s := fixture()
	s.Entries[0].MonthDay = "02-29"
	before, _ := json.Marshal(s)
	for _, tc := range []struct {
		year  int
		want  string
		count int
	}{{2027, "", 0}, {2028, "2028-02-29", 1}, {2100, "", 0}, {2104, "2104-02-29", 1}} {
		v, err := AsView(s, time.Date(tc.year, 2, 1, 0, 0, 0, 0, time.UTC))
		if err != nil || v.LeapDayPolicy != "next_leap_year" || len(v.Upcoming) != tc.count {
			t.Fatal(tc, v, err)
		}
		if tc.count > 0 && v.Upcoming[0].NextDate != tc.want {
			t.Fatal(v)
		}
	}
	after, _ := json.Marshal(s)
	if string(before) != string(after) {
		t.Fatal("read changed persisted calendar")
	}
}

func TestExplicitBirthdaySettingsValidationAndVersion(t *testing.T) {
	s := fixture()
	for _, mutate := range []func(*State){
		func(v *State) { v.Entries[0].MonthDay = "02-30" },
		func(v *State) { v.Entries[0].MonthDay = "2-01" },
		func(v *State) { v.Entries[0].MonthDay = "2026-11-01" },
		func(v *State) { v.Entries[0].Name = " " },
		func(v *State) { v.Entries[0].Name = "对象\n" },
		func(v *State) { v.Entries = append(v.Entries, v.Entries[0]) },
		func(v *State) { v.Entries = nil },
		func(v *State) { v.LeadDays = 31 },
	} {
		bad := fixture()
		mutate(&bad)
		if Validate(bad) == nil {
			t.Fatal("invalid calendar accepted", bad)
		}
	}
	if Validate(s) != nil {
		t.Fatal("explicit valid birthday rejected")
	}
	enabled, days := true, 30
	input := Input{ExpectedVersion: 1, Enabled: &enabled, LeadDays: &days, Entries: []Entry{}}
	next, err := Replace(s, input)
	if err != nil || next.Version != 2 || len(next.Entries) != 0 {
		t.Fatal("delete did not persist", next, err)
	}
	if _, err = Replace(next, input); entity.Code(err) != "VERSION_CONFLICT" {
		t.Fatal("stale settings accepted", err)
	}
	input.Enabled = nil
	if _, err = Replace(s, input); entity.Code(err) != "INVALID_ARGUMENT" {
		t.Fatal("implicit opt-in accepted", err)
	}
}
