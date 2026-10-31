package repository

import (
	"context"
	"strings"

	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/statement"
)

// StatementTransactions matches in the database's actual unique-key collation,
// rather than guessing equivalent external IDs in Go. Every lookup is scoped
// to the same owner/account as the persistent unique constraint. It is SELECT
// only: a preview never reserves an ID or authorizes a subsequent import.
func (r *Core) StatementTransactions(ctx context.Context, user, account int64, ids []string) (map[string]entity.Transaction, error) {
	out := map[string]entity.Transaction{}
	if user <= 0 || account <= 0 || len(ids) > statement.MaxRecords {
		return nil, entity.Fail("INVALID_ARGUMENT", "bounded owned statement lookup required")
	}
	for start := 0; start < len(ids); start += 100 {
		end := min(start+100, len(ids))
		parts := make([]string, 0, end-start)
		args := make([]any, 0, end-start+2)
		for _, id := range ids[start:end] {
			parts = append(parts, "SELECT ? AS external_id")
			args = append(args, id)
		}
		args = append(args, user, account)
		var rows []struct {
			RequestedID        string `gorm:"column:requested_id"`
			entity.Transaction `gorm:"embedded"`
		}
		query := "SELECT requested.external_id AS requested_id, stored.* FROM (" + strings.Join(parts, " UNION ALL ") + ") requested JOIN banking_transactions stored ON stored.external_transaction_id=requested.external_id WHERE stored.user_id=? AND stored.bank_account_id=?"
		if err := r.db.WithContext(ctx).Raw(query, args...).Scan(&rows).Error; err != nil {
			return nil, DBError(err)
		}
		for _, row := range rows {
			out[row.RequestedID] = row.Transaction
		}
	}
	return out, nil
}
