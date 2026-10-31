//go:build integration

package integration

import (
	"context"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/service"
	"testing"
	"time"
)

func TestCanonicalCategoryFilterMatchesBillingAndPagination(t *testing.T) {
	h := harness(t)
	a := h.account(t, 0)
	for _, category := range []string{" FOOD ", "餐饮", "dining", "\tfood\t", "\u3000FOOD\u00a0", "salary", "unrecognized"} {
		_, err := h.svc.ImportTransaction(context.Background(), service.Meta{UserID: h.uid}, uuid.NewString(), service.TransactionInput{BankAccountID: a.ID, ExternalTransactionID: uuid.NewString(), Direction: "expense", AmountMinor: 100, Currency: "CNY", Category: category, OccurredAt: time.Now().UnixMilli(), EffectMode: "record_only"})
		if err != nil {
			t.Fatal(err)
		}
	}
	type page struct {
		Items []entity.Transaction `json:"items"`
		Total int64                `json:"total"`
	}
	p := parse[page](t, h.call(t, "GET", "/api/v1/transactions?canonical_category=dining&limit=2&offset=1", "", nil, 200))
	if p.Total != 5 || len(p.Items) != 2 {
		t.Fatalf("canonical dining filter: %+v", p)
	}
	p = parse[page](t, h.call(t, "GET", "/api/v1/transactions?canonical_category=uncategorized", "", nil, 200))
	if p.Total != 2 {
		t.Fatalf("unknown categories: %+v", p)
	}
	p = parse[page](t, h.call(t, "GET", "/api/v1/transactions?category=dining", "", nil, 200))
	if p.Total != 1 {
		t.Fatal("raw category compatibility changed", p)
	}
	h.call(t, "GET", "/api/v1/transactions?canonical_category=bad", "", nil, 400)
}
