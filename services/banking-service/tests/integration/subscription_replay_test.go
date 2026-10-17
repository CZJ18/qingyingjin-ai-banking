//go:build integration

package integration

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/repository"
)

// Reanalysis may add duplicate evidence, but must never erase it or insert a
// second payment for the same user's transaction on either supported database.
func TestSubscriptionPaymentReplayPreservesDuplicateEvidence(t *testing.T) {
	h := harness(t)
	account := h.account(t, 0)
	at := time.Now().UTC().Add(-time.Hour).UnixMilli()
	transaction := parse[entity.Transaction](t, h.call(t, "POST", "/api/v1/transactions/import", uuid.NewString(), map[string]any{
		"bank_account_id": fmt.Sprint(account.ID), "external_transaction_id": uuid.NewString(),
		"direction": "expense", "amount_minor": int64(2000), "category": "sports",
		"merchant_name": "Replay fixture", "occurred_at": at, "effect_mode": "record_only",
	}, 201))
	repo := repository.New(h.conn.DB)
	err := repo.Transaction(context.Background(), h.uid, func(tx *repository.Tx) error {
		sub, err := tx.StoreSubscriptionCandidate(&entity.Subscription{
			Base: entity.Base{UserID: h.uid}, BankAccountID: account.ID, Fingerprint: uuid.NewString(),
			MerchantName: "Replay fixture", Category: "sports", BillingInterval: "monthly", AmountMinor: 2000,
			Currency: "CNY", NextBillingAt: at, ConfidenceBPS: 9000, Status: "candidate", Version: 1, IsSimulated: true,
		})
		if err != nil {
			return err
		}
		for _, duplicate := range []bool{false, true, false} {
			if err = tx.StoreSubscriptionPayment(&entity.SubscriptionPayment{
				Base: entity.Base{UserID: h.uid}, SubscriptionID: sub.ID, TransactionID: transaction.ID,
				AmountMinor: transaction.AmountMinor, OccurredAt: transaction.OccurredAt, IsPossibleDuplicate: duplicate,
			}); err != nil {
				return fmt.Errorf("store evidence duplicate=%t: %w", duplicate, err)
			}
		}
		payments, err := tx.SubscriptionPayments(sub.ID)
		if err != nil {
			return err
		}
		if len(payments) != 1 || !payments[0].IsPossibleDuplicate || payments[0].TransactionID != transaction.ID {
			return fmt.Errorf("payment replay lost duplicate evidence or inserted extra rows")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
