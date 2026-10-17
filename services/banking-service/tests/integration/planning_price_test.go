//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/planning"
	"github.com/qingyingjin/banking-service/internal/service"
	"testing"
	"time"
)

func TestAdaptiveObservedSubscriptionPriceMySQL(t *testing.T) {
	h := harness(t)
	ctx := context.Background()
	m := service.Meta{UserID: h.uid, TraceID: "price-test", TaskID: "module6-price"}
	a := h.account(t, 100000)
	g, e := h.svc.CreateGoal(ctx, m, uuid.NewString(), service.GoalInput{Name: "price fixture", TargetMinor: 100000, MonthlyMinor: 70000, StartsAt: time.Now().Add(-time.Hour).UnixMilli(), EndsAt: time.Now().AddDate(0, 3, 0).UnixMilli()})
	if e != nil {
		t.Fatal(e)
	}
	sub := entity.Subscription{Base: entity.Base{UserID: h.uid}, BankAccountID: a.ID, Fingerprint: fmt.Sprintf("%064d", a.ID), MerchantName: "Simulation price evidence", Category: "sports", BillingInterval: "monthly", AmountMinor: 2000, Currency: "CNY", NextBillingAt: time.Now().UnixMilli(), ConfidenceBPS: 9000, Status: "active", Version: 1, IsSimulated: true}
	if e = h.conn.DB.Create(&sub).Error; e != nil {
		t.Fatal(e)
	}
	p := planning.New(h.conn.DB)
	v, e := p.Create(ctx, m, planning.CreateInput{Name: "price fixture", Settings: planning.Settings{AccountIDs: []int64{a.ID}, SavingsGoalIDs: []int64{g.ID}, LivingRemainingMinor: cents(30000), EssentialRemainingMinor: cents(20000), SafetyReserveMinor: 10000, CashBufferMinor: 5000}})
	if e != nil || v.Result.SavingsCapMinor != 53000 {
		t.Fatal(v, e)
	}
	tx, e := h.svc.ImportTransaction(ctx, m, uuid.NewString(), service.TransactionInput{BankAccountID: a.ID, ExternalTransactionID: uuid.NewString(), Direction: "expense", AmountMinor: 2500, Currency: "CNY", Category: "subscription", MerchantName: sub.MerchantName, OccurredAt: time.Now().UnixMilli(), EffectMode: "record_only"})
	if e != nil {
		t.Fatal(e)
	}
	pay := entity.SubscriptionPayment{Base: entity.Base{UserID: h.uid}, SubscriptionID: sub.ID, TransactionID: tx.ID, AmountMinor: tx.AmountMinor, OccurredAt: tx.OccurredAt}
	if e = h.conn.DB.Create(&pay).Error; e != nil {
		t.Fatal(e)
	}
	v, e = p.Replan(ctx, m, v.Plan.ID)
	if e != nil || v.Result.SavingsCapMinor != 52500 || v.Result.AdditionalObligationsMinor != 2500 {
		t.Fatal(v.Result, e)
	}
	h.balance(t, a.ID, 100000)
	h.balance(t, g.AccountID, 0)
	var snapshot planning.Snapshot
	if e = json.Unmarshal(v.Version.SnapshotJSON, &snapshot); e != nil {
		t.Fatal(e)
	}
	found := false
	for _, s := range snapshot.Sources {
		if s.Type == "subscription_payment" && s.ID == fmt.Sprint(pay.ID) {
			found = true
		}
	}
	if !found {
		t.Fatal("price evidence lost")
	}
}
