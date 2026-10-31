package coze

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// AdmissionSnapshot forwards a read-only owner-bound snapshot, never a grant.
func (c *Client) AdmissionSnapshot(ctx context.Context, bearer string) (map[string]any, error) {
	if c.bridge == nil || !strings.HasPrefix(bearer, "Bearer ") {
		return nil, ErrMonetaryAdmissionRequired
	}
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, c.bridge.base+"/v1/budget", nil)
	if e != nil {
		return nil, ErrMonetaryAdmissionRequired
	}
	req.Header.Set("Authorization", "Bearer "+c.bridge.token)
	req.Header.Set("X-Banking-Authorization", bearer)
	res, e := c.bridge.client.Do(req)
	if e != nil {
		return nil, ErrBudgetUnavailable
	}
	defer res.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(res.Body, 32769))
	if e != nil || len(raw) > 32768 {
		return nil, ErrMonetaryAdmissionRequired
	}
	if res.StatusCode == http.StatusServiceUnavailable && gatewayQuotaCode(raw) == "MODEL_EXECUTION_CONFIRMATION_REQUIRED" {
		return nil, ErrExecutionConfirmationRequired
	}
	if res.StatusCode == http.StatusServiceUnavailable {
		switch gatewayQuotaCode(raw) {
		case "MODEL_BUDGET_UNAVAILABLE", "BUDGET_UNAVAILABLE":
			return nil, ErrBudgetUnavailable
		case "POLICY_UNAVAILABLE":
			return nil, ErrApprovalRequired
		}
	}
	if res.StatusCode == http.StatusTooManyRequests {
		switch gatewayQuotaCode(raw) {
		case "MODEL_APPROVAL_LIMIT_EXCEEDED":
			return nil, ErrApprovalLimit
		case "USER_MONTHLY_BUDGET_EXCEEDED":
			return nil, ErrMonthlyQuotaExceeded
		case "MODEL_SPEND_QUOTA_EXCEEDED":
			return nil, ErrSpendQuotaExceeded
		}
	}
	if res.StatusCode == 403 {
		return nil, ErrApprovalRequired
	}
	if res.StatusCode != 200 {
		return nil, ErrMonetaryAdmissionRequired
	}
	var snapshot map[string]any
	if json.Unmarshal(raw, &snapshot) != nil {
		return nil, ErrMonetaryAdmissionRequired
	}
	allowed := map[string]bool{"approval_window": true, "approval_limits": true, "period": true, "timezone": true, "currency": true, "limit": true, "settled": true, "reserved": true, "frozen": true, "settlement_basis": true}
	for k := range snapshot {
		if !allowed[k] {
			return nil, ErrMonetaryAdmissionRequired
		}
	}
	return snapshot, nil
}
