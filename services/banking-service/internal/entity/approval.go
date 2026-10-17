package entity

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
)

// Digest binds every execution-relevant immutable parameter, including the
// purpose/goal and expiry. Mutable state and trace headers are excluded.
func TransferDigest(v *TransferIntent) string {
	data, _ := json.Marshal(struct {
		ID, User, Source, Target, Amount, Expiry int64
		Currency, Purpose                        string
		Goal                                     *int64
	}{v.ID, v.UserID, v.SourceAccountID, v.TargetAccountID, v.AmountMinor, v.ExpiresAt, v.Currency, v.Purpose, v.SavingsGoalID})
	return fmt.Sprintf("%x", sha256.Sum256(data))
}
