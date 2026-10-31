package service

import (
	"context"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/statement"
)

// PreviewStatement retains raw upload provenance and compares normalized facts
// against the target account. Upload hashes establish integrity, not bank
// authenticity. The existing independent execution gate and unique constraint
// still protect imports racing with a preview.
func (s *Core) PreviewStatement(ctx context.Context, user, account int64, in statement.Input) (statement.Preview, error) {
	var empty statement.Preview
	if _, err := s.Account(ctx, user, account); err != nil {
		return empty, err
	}
	out, err := statement.Parse(in)
	if err != nil {
		return empty, err
	}
	ids := make([]string, 0, len(out.Records))
	for _, record := range out.Records {
		ids = append(ids, record.ExternalTransactionID)
	}
	stored, err := s.repo.StatementTransactions(ctx, user, account, ids)
	if err != nil {
		return empty, err
	}
	records := make([]statement.Record, 0, len(out.Records))
	skipped := 0
	for _, record := range out.Records {
		prior, exists := stored[record.ExternalTransactionID]
		if !exists {
			records = append(records, record)
			continue
		}
		if prior.AmountMinor != record.AmountMinor || prior.Direction != record.Direction || prior.Currency != record.Currency || prior.Category != record.Category || prior.MerchantName != record.MerchantName || prior.OccurredAt != record.OccurredAt || prior.Kind != "external" || prior.EffectMode != "record_only" {
			return empty, entity.Fail("IDEMPOTENCY_CONFLICT", "账单编号已存在且内容不同，请核对原交易或使用独立账本；不会覆盖历史记录。")
		}
		skipped++
	}
	out.Records = records
	out.Skipped += skipped
	if skipped > 0 {
		out.Warnings = append(out.Warnings, "已跳过当前账户中已补录且内容一致的交易；未覆盖原记录，预览不会创建授权或改变余额。")
	}
	return out, nil
}
