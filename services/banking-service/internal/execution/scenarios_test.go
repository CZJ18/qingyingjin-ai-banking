package execution

import (
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/entity"
	"testing"
	"time"
)

func TestOccasionExplicitLocalOrderTimeContract(t *testing.T) {
	var in OccasionPlanInput
	if err := strict(json.RawMessage(`{"name":"生日","birthday":"2026-11-01","order_time":"08:30","account_id":"1","reserve_minor":100000,"items":["flowers","cake"]}`), &in); err != nil {
		t.Fatalf("explicit two-days-before local order time rejected: %v", err)
	}
}

func TestOccasionUsesTwoCalendarDaysAndImmutableLocalClock(t *testing.T) {
	for _, tc := range []struct{ date, clock, want string }{
		{"2026-11-01", "08:30", "2026-10-30T08:30:00+08:00"},
		{"2026-11-01", "", "2026-10-30T09:00:00+08:00"},
		{"2028-03-01", "00:00", "2028-02-28T00:00:00+08:00"},
		{"2027-01-01", "23:59", "2026-12-30T23:59:00+08:00"},
	} {
		_, due, err := occasionDue(tc.date, tc.clock)
		if err != nil || due.Format(time.RFC3339) != tc.want {
			t.Fatal(tc, due, err)
		}
	}
	for _, clock := range []string{"9:00", "24:00", "09:60", "09:00:01", "09:00Z", " 09:00"} {
		if _, _, err := occasionDue("2026-11-01", clock); entity.Code(err) != "INVALID_ARGUMENT" {
			t.Fatal("invalid local order time", clock, err)
		}
	}
	if _, _, err := occasionDue("2026-02-30", "09:00"); entity.Code(err) != "INVALID_ARGUMENT" {
		t.Fatal("invalid birthday", err)
	}
}

func TestScenarioKindsUseExistingExecutionGateway(t *testing.T) {
	for _, kind := range []string{"scheduled_transfer", "investment_assessment", "investment_order", "occasion_plan", "occasion_order"} {
		if !kindValid(kind) {
			t.Errorf("missing explicit gateway kind: %s", kind)
		}
	}
}

func TestScheduledWindowAndImmutableDueTime(t *testing.T) {
	now := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	for _, due := range []int64{0, now.UnixMilli(), now.Add(31 * 24 * time.Hour).UnixMilli()} {
		if validateSchedule(due, now) == nil {
			t.Fatal("invalid future due time accepted", due)
		}
	}
	if err := validateSchedule(now.Add(time.Minute).UnixMilli(), now); err != nil {
		t.Fatal(err)
	}
}

func TestInvestmentAssessmentAndProductBoundaries(t *testing.T) {
	if _, err := assessRisk([]int{3, 3}); err == nil {
		t.Fatal("missing answer")
	}
	if _, err := assessRisk([]int{3, 0, 3}); err == nil {
		t.Fatal("invalid answer")
	}
	level, err := assessRisk([]int{3, 1, 3})
	if err != nil || level != 1 {
		t.Fatal("weakest capacity must constrain suitability")
	}
	if _, err := investmentProduct("made-up"); entity.Code(err) != "INVALID_ARGUMENT" {
		t.Fatal("unknown product")
	}
}

func TestOccasionCatalogCannotInventPriceOrMerchant(t *testing.T) {
	cost, err := occasionCost([]string{"flowers", "cake"})
	if err != nil || cost != 40000 {
		t.Fatal(cost, err)
	}
	for _, ids := range [][]string{nil, {"flowers", "flowers"}, {"unapproved_merchant"}} {
		if _, err := occasionCost(ids); err == nil {
			t.Fatal("invalid catalog selection", ids)
		}
	}
}
