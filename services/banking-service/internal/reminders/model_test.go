package reminders

import (
	"github.com/qingyingjin/banking-service/internal/entity"
	"testing"
	"time"
)

func now() time.Time {
	return time.Date(2026, 10, 16, 9, 0, 0, 0, time.FixedZone("Asia/Shanghai", 8*3600))
}
func sub() entity.Subscription {
	return entity.Subscription{Base: entity.Base{ID: 7, UserID: 42}, Status: "active", IsSimulated: true, Currency: "CNY", AmountMinor: 2000, MerchantName: "模拟会员", NextBillingAt: now().Add(48 * time.Hour).UnixMilli()}
}
func identifier() string { return "94b27a56-5355-400e-8c17-fce6643da4b4" }
func TestFutureCycleAndDedup(t *testing.T) {
	s := Default()
	v, e := Reconcile(s, []entity.Subscription{sub()}, now(), identifier)
	if e != nil || len(v.Items) != 1 || v.Items[0].Status != "unread" {
		t.Fatal(e, v)
	}
	again, e := Reconcile(v, []entity.Subscription{sub()}, now().Add(time.Minute), identifier)
	if e != nil || len(again.Items) != 1 || again.Items[0].ID != v.Items[0].ID {
		t.Fatal(e)
	}
}
func TestOnlyConfirmedFutureSimulation(t *testing.T) {
	for _, change := range []func(*entity.Subscription){func(s *entity.Subscription) { s.Status = "candidate" }, func(s *entity.Subscription) { s.Status = "cancelled" }, func(s *entity.Subscription) { s.IsSimulated = false }, func(s *entity.Subscription) { s.Currency = "USD" }, func(s *entity.Subscription) { s.NextBillingAt = now().Add(-time.Hour).UnixMilli() }, func(s *entity.Subscription) { s.NextBillingAt = now().AddDate(0, 0, 8).UnixMilli() }} {
		s := sub()
		change(&s)
		v, e := Reconcile(Default(), []entity.Subscription{s}, now(), identifier)
		if e != nil || len(v.Items) != 0 {
			t.Fatal(e)
		}
	}
}
func TestShanghaiCalendarWindowIncludesToday(t *testing.T) {
	v := Default()
	v.Settings.LeadDays = 0
	s := sub()
	s.NextBillingAt = now().Add(10 * time.Hour).UnixMilli()
	v, e := Reconcile(v, []entity.Subscription{s}, now(), identifier)
	if e != nil || len(v.Items) != 1 {
		t.Fatal(e)
	}
	s.NextBillingAt = now().Add(16 * time.Hour).UnixMilli()
	v, e = Reconcile(DefaultWithLead(0), []entity.Subscription{s}, now(), identifier)
	if e != nil || len(v.Items) != 0 {
		t.Fatal(e)
	}
}
func TestReadAndPriceChange(t *testing.T) {
	s := sub()
	v, _ := Reconcile(Default(), []entity.Subscription{s}, now(), identifier)
	v, e := MarkRead(v, v.Items[0].ID, v.Items[0].Version, now())
	if e != nil || v.Items[0].Status != "read" {
		t.Fatal(e)
	}
	oldVersion := v.Items[0].Version
	s.AmountMinor = 3000
	v, e = Reconcile(v, []entity.Subscription{s}, now(), identifier)
	if e != nil || v.Items[0].Status != "unread" || v.Items[0].AmountMinor != 3000 {
		t.Fatal(e)
	}
	if _, e = MarkRead(v, v.Items[0].ID, oldVersion, now()); entity.Code(e) != "VERSION_CONFLICT" {
		t.Fatal(e)
	}
}
func TestCancellationRescheduleAndDisable(t *testing.T) {
	s := sub()
	v, _ := Reconcile(Default(), []entity.Subscription{s}, now(), identifier)
	s.Status = "cancelled"
	v, _ = Reconcile(v, []entity.Subscription{s}, now(), identifier)
	if v.Items[0].Status != "withdrawn" {
		t.Fatal(v)
	}
	s.Status = "active"
	s.NextBillingAt += 3600000
	v, _ = Reconcile(v, []entity.Subscription{s}, now(), func() string { return "another-cycle" })
	if len(v.Items) != 2 {
		t.Fatal(v)
	}
	v.Settings.Enabled = false
	v, _ = Reconcile(v, []entity.Subscription{s}, now(), identifier)
	for _, n := range v.Items {
		if n.Status == "unread" {
			t.Fatal("disabled notice still unread")
		}
	}
}
func TestReadRetryAndMissingOwnerNotice(t *testing.T) {
	v, _ := Reconcile(Default(), []entity.Subscription{sub()}, now(), identifier)
	version := v.Items[0].Version
	v, _ = MarkRead(v, v.Items[0].ID, version, now())
	if _, e := MarkRead(v, v.Items[0].ID, version, now()); e != nil {
		t.Fatal(e)
	}
	if _, e := MarkRead(v, "another-owner", version, now()); entity.Code(e) != "NOT_FOUND" {
		t.Fatal(e)
	}
}
func TestNoticeCapacityIsExplicit(t *testing.T) {
	v := Default()
	v.Items = make([]Notice, MaxNotices)
	for i := range v.Items {
		v.Items[i] = Notice{ID: "existing", SubscriptionID: int64(i + 100), ChargeAt: sub().NextBillingAt, Status: "read"}
	}
	v, e := Reconcile(v, []entity.Subscription{sub()}, now(), identifier)
	if e != nil || !v.CapacityLimited || len(v.Items) > MaxNotices {
		t.Fatal(e)
	}
}
