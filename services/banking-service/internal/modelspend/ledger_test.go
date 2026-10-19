package modelspend

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestLedgerRejectsMissingDatabaseAndInvalidPolicy(t *testing.T) {
	if _, err := New(nil, syntheticPolicy()); !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing database accepted", err)
	}
	if _, err := New(&gorm.DB{}, Policy{}); !errors.Is(err, ErrPolicy) {
		t.Fatal("missing prices accepted", err)
	}
}

func TestExpiredPriceCannotReserveButHistoricalServiceCanOpen(t *testing.T) {
	p := syntheticPolicy()
	p.ValidFrom = time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	p.ValidUntil = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)
	s, err := New(&gorm.DB{}, p)
	if err != nil {
		t.Fatal("historical reconciliation blocked by expired price", err)
	}
	if _, err := s.Reserve(context.Background(), "expired", strings.Repeat("a", 64)); !errors.Is(err, ErrPolicy) {
		t.Fatal("expired price reached SQL/paid dispatch", err)
	}
}

func TestLedgerTimestampsStayInMillisecondsWhenGORMUpdatesRows(t *testing.T) {
	for _, model := range []any{&Pool{}, &Attempt{}, &Event{}} {
		parsed, err := schema.Parse(model, &sync.Map{}, schema.NamingStrategy{})
		if err != nil {
			t.Fatal(err)
		}
		if created := parsed.LookUpField("CreatedAt"); created == nil || created.AutoCreateTime != schema.UnixMillisecond {
			t.Fatalf("%T CreatedAt must use Unix milliseconds", model)
		}
		if updated := parsed.LookUpField("UpdatedAt"); updated != nil && updated.AutoUpdateTime != schema.UnixMillisecond {
			t.Fatalf("%T UpdatedAt must use Unix milliseconds", model)
		}
	}
}

func TestOperatorCredentialAndEvidenceAreRequiredBeforeDatabase(t *testing.T) {
	if _, err := NewReconciler(nil, strings.Repeat("z", 32), "operator"); err == nil {
		t.Fatal("missing ledger accepted")
	}
	s := &Service{}
	for _, tc := range []struct{ secret, operator string }{{"short", "operator"}, {strings.Repeat("z", 32), "../operator"}} {
		if _, err := NewReconciler(s, tc.secret, tc.operator); err == nil {
			t.Fatal("invalid operator configuration accepted")
		}
	}
	r := &Reconciler{}
	if _, err := r.Reconcile(context.Background(), "wrong", "key", Settlement{}); !errors.Is(err, ErrUnavailable) {
		t.Fatal("unauthorized reconciliation", err)
	}
	if _, err := r.Pending(context.Background(), "wrong"); !errors.Is(err, ErrUnavailable) {
		t.Fatal("unauthorized pending access", err)
	}
}

func TestReportCacheRejectsUnsafeOrUnboundedResponses(t *testing.T) {
	for _, tc := range []Report{
		{ProviderID: "provider", ResponseBody: []byte(`{}`), ContentType: "text/html"},
		{ProviderID: "provider", ResponseBody: []byte(`{}`), ContentType: "application/json; charset=utf-16"},
		{ProviderID: "provider", ResponseBody: []byte(`not-json`), ContentType: "application/json"},
		{ProviderID: "provider", ResponseBody: []byte(strings.Repeat("x", (2<<20)+1)), ContentType: "text/event-stream"},
		{ProviderID: "bad\nheader", ResponseBody: []byte(`{}`), ContentType: "application/json"},
	} {
		if _, _, err := validateReport(tc); err == nil {
			t.Fatal("unsafe report accepted", tc.ContentType, len(tc.ResponseBody))
		}
	}
	for _, tc := range []Report{
		{ProviderID: "provider", ResponseBody: []byte(`{"choices":[]}`), ContentType: "application/json; charset=utf-8"},
		{ProviderID: "provider", ResponseBody: []byte("data: {}\n\ndata: [DONE]\n\n"), ContentType: "text/event-stream"},
	} {
		if digest, _, err := validateReport(tc); err != nil || len(digest) != 64 {
			t.Fatal("valid provider response rejected", err)
		}
	}
}

func TestSettlementEvidenceAndBillIdentityAreStrict(t *testing.T) {
	valid := Settlement{AttemptID: "attempt", ProjectID: "synthetic-project", Currency: "USD", BillID: "bill-2026-10", LineID: "line:1", ProviderID: "provider-1", ActualCost: "0.00000001", EvidenceDigest: strings.Repeat("a", 64)}
	if _, err := validateSettlement(valid); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*Settlement){
		func(s *Settlement) { s.EvidenceDigest = "not-independent-evidence" },
		func(s *Settlement) { s.EvidenceDigest = strings.Repeat("g", 64) },
		func(s *Settlement) { s.BillID = "../bill" },
		func(s *Settlement) { s.LineID = strings.Repeat("a", 129) },
		func(s *Settlement) { s.ProviderID = "" },
		func(s *Settlement) { s.Currency = "usd" },
		func(s *Settlement) { s.ActualCost = "NaN" },
	} {
		bad := valid
		mutate(&bad)
		if _, err := validateSettlement(bad); err == nil {
			t.Fatalf("invalid settlement accepted %+v", bad)
		}
	}
}

func TestSupplierModelMismatchInvalidatesTheReservedBound(t *testing.T) {
	p := syntheticPolicy()
	for _, model := range []string{"other-supplier-model", ""} {
		_, exceeded, err := evaluateReport(p, Report{Model: model, Usage: Usage{PromptTokens: 1, TotalTokens: 1}})
		if err != nil || !exceeded {
			t.Fatal("supplier model mismatch did not invalidate bound", model, exceeded, err)
		}
	}
	_, exceeded, err := evaluateReport(p, Report{Model: p.Model, Usage: Usage{PromptTokens: 1, TotalTokens: 1}})
	if err != nil || exceeded {
		t.Fatal("matching supplier model rejected", exceeded, err)
	}
}

func TestSupplierModelEvidenceCannotOverflowTheFreezeAudit(t *testing.T) {
	for _, value := range []string{"synthetic-model", strings.Repeat("x", 2<<20), string([]byte{0xff})} {
		model, digest := reportModelEvidence(value)
		if len(model) > 128 || len(digest) != 64 || digest != hashText(value) {
			t.Fatal("unbounded or untraceable model audit")
		}
		if value == "synthetic-model" && model != value {
			t.Fatal("bounded supplier model was lost")
		}
	}
}

func TestCoherentHugeSupplierUsageFreezesBeforeEstimateSafetyCaps(t *testing.T) {
	p := syntheticPolicy()
	for _, usage := range []Usage{
		{PromptTokens: 1_000_000_000_001, TotalTokens: 1_000_000_000_001},
		{CompletionTokens: 1_000_000_000_001, TotalTokens: 1_000_000_000_001},
		{PromptTokens: math.MaxInt64, TotalTokens: math.MaxInt64},
	} {
		if _, exceeded, err := evaluateReport(p, Report{Model: p.Model, Usage: usage}); err != nil || !exceeded {
			t.Fatal("coherent usage beyond verified bound did not freeze", exceeded, err)
		}
	}
	if _, _, err := evaluateReport(p, Report{Model: p.Model, Usage: Usage{PromptTokens: math.MaxInt64, CompletionTokens: 1, TotalTokens: 0}}); !errors.Is(err, ErrPolicy) {
		t.Fatal("overflowed usage arithmetic accepted", err)
	}
}

func TestMissingUsageCannotBeRecordedAsZeroConsumption(t *testing.T) {
	p := syntheticPolicy()
	if _, exceeded, err := evaluateReport(p, Report{Model: p.Model, UsageUnknown: true}); exceeded || !errors.Is(err, ErrUnavailable) {
		t.Fatal("missing usage became a zero-valued report", exceeded, err)
	}
	if _, exceeded, err := evaluateReport(p, Report{Model: "wrong-model", UsageUnknown: true}); !exceeded || err != nil {
		t.Fatal("missing usage concealed supplier model mismatch", exceeded, err)
	}
}
