package subscription

import (
	"testing"
	"time"
)

func TestDetectionWindowCoversAnnualEvidenceAndValidatesTimezone(t *testing.T) {
	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	for _, zone := range []string{"", "UTC", "Asia/Shanghai"} {
		from, to, _, err := NormalizeDetection(DetectInput{Timezone: zone}, now)
		if err != nil || !to.Equal(now) || now.Sub(from) != 1096*24*time.Hour {
			t.Fatalf("window %v %v %v", from, to, err)
		}
	}
	for _, in := range []DetectInput{{Timezone: "Mars/Olympus"}, {From: "bad"}, {From: "2020-01-01T00:00:00Z"}, {From: "2026-11-01T00:00:00Z"}, {AccountID: "01"}} {
		if _, _, _, err := NormalizeDetection(in, now); err == nil {
			t.Fatalf("invalid input accepted: %+v", in)
		}
	}
	from, to, _, err := NormalizeDetection(DetectInput{To: "2025-10-20T00:00:00Z"}, now)
	if err != nil || to.Sub(from) != 1096*24*time.Hour {
		t.Fatal("default must be relative to supplied end", from, to, err)
	}
}
