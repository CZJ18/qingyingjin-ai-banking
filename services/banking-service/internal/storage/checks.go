package storage

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/qingyingjin/banking-service/internal/entity"
)

// Each query returns violating groups, never sensitive row values. FK checks
// below also defend against historical schemas whose constraints were disabled.
var invariantSQL = []struct{ name, sql string }{
	{"account_ledger_balance", `SELECT COUNT(*) FROM banking_bank_accounts a LEFT JOIN (SELECT user_id,account_id,SUM(delta_minor) amount FROM banking_ledger_entries WHERE account_id IS NOT NULL GROUP BY user_id,account_id) l ON l.user_id=a.user_id AND l.account_id=a.id WHERE a.balance_minor<>COALESCE(l.amount,0) OR a.balance_minor<0 OR a.balance_minor>1000000000000 OR a.is_simulated<>TRUE`},
	{"balanced_two_leg_journals", `SELECT COUNT(*) FROM (SELECT journal_id FROM banking_ledger_entries GROUP BY journal_id HAVING COUNT(*)<>2 OR SUM(delta_minor)<>0 OR COUNT(DISTINCT currency)<>1 OR COUNT(DISTINCT user_id)<>1 OR COUNT(DISTINCT sequence)<>2 OR MIN(sequence)<>1 OR MAX(sequence)<>2) v`},
	{"ledger_owner_and_transaction", `SELECT COUNT(*) FROM banking_ledger_entries l LEFT JOIN banking_bank_accounts a ON a.id=l.account_id AND a.user_id=l.user_id LEFT JOIN banking_transactions t ON t.id=l.transaction_id AND t.user_id=l.user_id WHERE (l.account_id IS NOT NULL AND (a.id IS NULL OR a.currency<>l.currency)) OR (l.transaction_id IS NOT NULL AND (t.id IS NULL OR t.journal_id<>l.journal_id OR t.bank_account_id<>l.account_id OR t.currency<>l.currency OR t.amount_minor<>ABS(l.delta_minor) OR (t.direction='income' AND l.delta_minor<0) OR (t.direction='expense' AND l.delta_minor>0))) OR l.delta_minor=0 OR (l.side='debit' AND l.delta_minor<0) OR (l.side='credit' AND l.delta_minor>0)`},
	{"applied_transactions_have_ledger", `SELECT COUNT(*) FROM banking_transactions t LEFT JOIN banking_ledger_entries l ON l.transaction_id=t.id AND l.user_id=t.user_id WHERE (t.effect_mode='apply_simulated_effect' AND (t.journal_id IS NULL OR l.id IS NULL)) OR (t.effect_mode='record_only' AND (t.journal_id IS NOT NULL OR l.id IS NOT NULL))`},
	{"transaction_core_audit", `SELECT COUNT(*) FROM banking_transactions t LEFT JOIN banking_core_audits a ON a.id=t.audit_id AND a.user_id=t.user_id WHERE a.id IS NULL`},
	{"transfer_posting_once", `SELECT COUNT(*) FROM banking_transfer_intents t LEFT JOIN (SELECT transfer_id,COUNT(*) n,COUNT(DISTINCT journal_id) journals FROM banking_transactions WHERE transfer_id IS NOT NULL GROUP BY transfer_id) x ON x.transfer_id=t.id LEFT JOIN banking_transfer_approvals a ON a.transfer_id=t.id AND a.user_id=t.user_id WHERE (t.status='succeeded' AND (COALESCE(x.n,0)<>2 OR x.journals<>1 OR t.journal_id='' OR a.transfer_id IS NULL OR a.decision<>'approve' OR a.consumed_at=0 OR a.approval_hash<>t.approval_hash)) OR (t.status<>'succeeded' AND COALESCE(x.n,0)<>0)`},
	{"savings_ledger_transfer", `SELECT COUNT(*) FROM banking_savings_ledgers s LEFT JOIN banking_transfer_intents t ON t.id=s.transfer_id AND t.user_id=s.user_id LEFT JOIN banking_savings_goals g ON g.id=s.goal_id AND g.user_id=s.user_id WHERE t.id IS NULL OR g.id IS NULL OR t.status<>'succeeded' OR t.journal_id<>s.journal_id OR t.savings_goal_id<>s.goal_id OR s.currency<>t.currency OR (t.purpose='savings_withdrawal' AND s.delta_minor<>-t.amount_minor) OR (t.purpose<>'savings_withdrawal' AND s.delta_minor<>t.amount_minor)`},
	{"successful_gateway_authorization", `SELECT COUNT(*) FROM banking_operation_intents o LEFT JOIN banking_user_authorizations a ON a.id=o.authorization_id AND a.user_id=o.user_id AND a.operation_id=o.id WHERE o.status='succeeded' AND (a.id IS NULL OR a.decision<>'approve' OR a.token_id='' OR a.intent_digest<>o.intent_digest OR a.consumed_at=0)`},
	{"authorization_binding", `SELECT COUNT(*) FROM banking_user_authorizations a LEFT JOIN banking_operation_intents o ON o.id=a.operation_id AND o.user_id=a.user_id WHERE o.id IS NULL OR o.authorization_id<>a.id OR a.intent_digest<>o.intent_digest OR (a.decision='reject' AND a.consumed_at=0)`},
	{"gateway_audit_reference", `SELECT COUNT(*) FROM banking_operation_intents o LEFT JOIN banking_execution_audits a ON a.id=o.audit_id AND a.user_id=o.user_id AND a.operation_id=o.id WHERE o.audit_id='' OR a.id IS NULL`},
	// gateway.execute deliberately replays its stored operation, not a response
	// blob. Other Atomic actions commit their serialized response in the same Tx.
	{"idempotency_hash_and_response", `SELECT COUNT(*) FROM banking_core_idempotency WHERE LENGTH(request_hash)<>64 OR (action<>'gateway.execute' AND response IS NULL)`},
	{"plan_event_once", `SELECT COUNT(*) FROM (SELECT plan_id,event_id FROM banking_plan_versions WHERE event_id IS NOT NULL GROUP BY plan_id,event_id HAVING COUNT(*)<>1) v`},
	{"plan_event_owner", `SELECT COUNT(*) FROM banking_plan_versions p LEFT JOIN banking_replan_events e ON e.id=p.event_id AND e.user_id=p.user_id WHERE p.event_id IS NOT NULL AND e.id IS NULL`},
	{"financial_plan_current_version", `SELECT COUNT(*) FROM banking_financial_plans p LEFT JOIN banking_plan_versions v ON v.plan_id=p.id AND v.user_id=p.user_id AND v.version=p.current_version WHERE v.id IS NULL`},
	{"lifestyle_goal_current_version", `SELECT COUNT(*) FROM banking_lifestyle_goals g LEFT JOIN banking_lifestyle_goal_versions v ON v.goal_id=g.id AND v.user_id=g.user_id AND v.version=g.current_version WHERE v.id IS NULL`},
}

func invariants(ctx context.Context, q queryer) ([]Check, error) {
	out := make([]Check, 0, len(invariantSQL))
	for _, check := range invariantSQL {
		var count int64
		if e := q.QueryRowContext(ctx, check.sql).Scan(&count); e != nil {
			return out, fmt.Errorf("reconcile %s: %w", check.name, e)
		}
		out = append(out, Check{Name: check.name, Violations: count})
	}
	if binding, e := digestBindings(ctx, q); e != nil {
		return out, e
	} else {
		out = append(out, binding...)
	}
	return out, nil
}

func digestBindings(ctx context.Context, q queryer) ([]Check, error) {
	operation := Check{Name: "gateway_intent_digest"}
	rows, e := q.QueryContext(ctx, `SELECT id,user_id,kind,agent,payload_json,snapshot_json,resource_id,resource_digest,source_account_id,target_account_id,amount_minor,currency,nonce,expires_at,intent_digest FROM banking_operation_intents`)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var o entity.OperationIntent
		var payload, snapshot []byte
		if e = rows.Scan(&o.ID, &o.UserID, &o.Kind, &o.Agent, &payload, &snapshot, &o.ResourceID, &o.ResourceDigest, &o.SourceAccountID, &o.TargetAccountID, &o.AmountMinor, &o.Currency, &o.Nonce, &o.ExpiresAt, &o.IntentDigest); e != nil {
			rows.Close()
			return nil, e
		}
		o.Payload, o.Snapshot = payload, snapshot
		if entity.OperationDigest(&o) != o.IntentDigest {
			operation.Violations++
		}
	}
	if e = rows.Err(); e != nil {
		rows.Close()
		return nil, e
	}
	rows.Close()
	transfer := Check{Name: "transfer_approval_digest"}
	rows, e = q.QueryContext(ctx, `SELECT id,user_id,source_account_id,target_account_id,amount_minor,expires_at,currency,purpose,savings_goal_id,approval_hash FROM banking_transfer_intents`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	for rows.Next() {
		var v entity.TransferIntent
		var goal sql.NullInt64
		if e = rows.Scan(&v.ID, &v.UserID, &v.SourceAccountID, &v.TargetAccountID, &v.AmountMinor, &v.ExpiresAt, &v.Currency, &v.Purpose, &goal, &v.ApprovalHash); e != nil {
			return nil, e
		}
		if goal.Valid {
			v.SavingsGoalID = &goal.Int64
		}
		if entity.TransferDigest(&v) != v.ApprovalHash {
			transfer.Violations++
		}
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	return []Check{operation, transfer}, nil
}

func checksPass(checks []Check) bool {
	for _, c := range checks {
		if c.Violations != 0 {
			return false
		}
	}
	return true
}
