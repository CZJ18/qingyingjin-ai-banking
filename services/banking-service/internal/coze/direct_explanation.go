package coze

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/qingyingjin/banking-service/internal/modelspend"
)

// This is a local adapter identifier, not a published Coze workflow. It is
// opt-in, and its exact provider body still needs a separate approved digest.
const DirectBillingExplanationWorkflow = "direct_provider_billing_explanation"
const DirectBillingExplanationPrompt = `Return only JSON with explanation and evidence_ids. Cite only supplied evidence_ids and do not add JSON keys. ` + BillingExplanationGuidance

// These facts follow from the current report contract: it records transactions
// but has no verified supplier coverage or future-savings attestation. A caller
// cannot upgrade completeness by including model- or user-written metadata.
func BillingExplanationLimitations() []string {
	return []string{"recorded_transactions_only", "completeness_not_verified", "not_available_account_balance", "future_savings_not_verified"}
}

func DirectWorkflowAllowed(role, id string) bool {
	return role == "supervisor" && (id == "direct_provider" || id == DirectScenarioPlannerWorkflow) || role == "billing" && id == DirectBillingExplanationWorkflow || role == "lifestyle" && id == DirectLifestyleExplanationWorkflow
}

type directExplanationInput struct {
	TaskID    string          `json:"task_id"`
	AgentType string          `json:"agent_type"`
	Result    json.RawMessage `json:"authoritative_result"`
	Evidence  []struct {
		Type    string `json:"type"`
		ID      string `json:"id"`
		AuditID string `json:"audit_id,omitempty"`
	} `json:"evidence"`
}

type directBillingFacts struct {
	Limitations []string `json:"analysis_limitations"`
	Currency    string   `json:"currency"`
	Income      int64    `json:"total_income"`
	Expense     int64    `json:"total_expense"`
	Count       int64    `json:"transaction_count"`
	Categories  []struct {
		Category string `json:"category"`
		Expense  int64  `json:"expense_minor"`
	} `json:"category_breakdown"`
}

func directExplanationFacts(input string) (directExplanationInput, directBillingFacts, []string, error) {
	var in directExplanationInput
	var facts directBillingFacts
	if modelspend.StrictJSON([]byte(input), &in, 65536) != nil || in.TaskID == "" || in.AgentType != "billing" ||
		json.Unmarshal(in.Result, &facts) != nil || len(facts.Currency) != 3 || strings.Trim(facts.Currency, "ABCDEFGHIJKLMNOPQRSTUVWXYZ") != "" ||
		facts.Income < 0 || facts.Income > 1000000000000 || facts.Expense < 0 || facts.Expense > 1000000000000 || facts.Count < 0 || facts.Count > 1000000 || len(facts.Categories) > 128 {
		return in, facts, nil, ErrMonetaryAdmissionRequired
	}
	facts.Limitations = BillingExplanationLimitations()
	var total int64
	categories := map[string]bool{}
	for _, category := range facts.Categories {
		if category.Category == "" || len(category.Category) > 64 || categories[category.Category] || category.Expense < 0 || category.Expense > facts.Expense-total {
			return in, facts, nil, ErrMonetaryAdmissionRequired
		}
		categories[category.Category] = true
		total += category.Expense
	}
	if total != facts.Expense {
		return in, facts, nil, ErrMonetaryAdmissionRequired
	}
	sort.Slice(facts.Categories, func(i, j int) bool { return facts.Categories[i].Category < facts.Categories[j].Category })
	ids := []string{}
	seen := map[string]bool{}
	reportSeen := false
	for _, ev := range in.Evidence {
		if ev.Type == "billing_report" {
			if reportSeen || ev.ID == "" {
				return in, facts, nil, ErrMonetaryAdmissionRequired
			}
			reportSeen = true
			ids = append(ids, "billing_report")
			continue
		}
		if ev.Type != "transaction" {
			continue
		}
		if ev.ID == "" || ev.ID == "billing_report" || len(ev.ID) > 128 || seen[ev.ID] || len(ids) >= 128 {
			return in, facts, nil, ErrMonetaryAdmissionRequired
		}
		seen[ev.ID] = true
		ids = append(ids, ev.ID)
	}
	if facts.Count > 0 && len(ids) == 0 {
		return in, facts, nil, ErrMonetaryAdmissionRequired
	}
	sort.Strings(ids)
	return in, facts, ids, nil
}

// Stable, minimal facts make an exact independently reviewed payload possible
// without approving volatile task/report/audit IDs or arbitrary future input.
// The gateway remains responsible for the signed task lease and exact hash.
func DirectBillingExplanationRequest(model string, max int64, input string) ([]byte, error) {
	_, facts, ids, err := directExplanationFacts(input)
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(struct {
		Facts directBillingFacts `json:"authoritative_result"`
		IDs   []string           `json:"evidence_ids"`
	}{facts, ids})
	if err != nil {
		return nil, err
	}
	return directRequestWithPrompt(model, max, string(raw), DirectBillingExplanationPrompt)
}

func directExplanationResponse(input, content, providerID string) (RunResponse, error) {
	in, _, ids, err := directExplanationFacts(input)
	if err != nil {
		return RunResponse{}, ErrOutcomeUnknown
	}
	var result struct {
		Explanation string   `json:"explanation"`
		IDs         []string `json:"evidence_ids"`
	}
	if modelspend.StrictJSON([]byte(content), &result, 16384) != nil || strings.TrimSpace(result.Explanation) == "" || len(result.Explanation) > 8192 || result.IDs == nil || len(ids) > 0 && len(result.IDs) == 0 {
		return RunResponse{}, ErrOutcomeUnknown
	}
	allowed := map[string]bool{}
	used := map[string]bool{}
	for _, id := range ids {
		allowed[id] = true
	}
	for _, id := range result.IDs {
		if !allowed[id] || used[id] {
			return RunResponse{}, ErrOutcomeUnknown
		}
		used[id] = true
	}
	// The stable report citation is resolved locally to this tool's actual
	// persisted report. A model cannot select a report or task from another run.
	for i, id := range result.IDs {
		if id == "billing_report" {
			for _, ev := range in.Evidence {
				if ev.Type == "billing_report" {
					result.IDs[i] = ev.ID
				}
			}
		}
	}
	// Restore identity from trusted local input; the model never chooses it.
	raw, _ := json.Marshal(map[string]any{"task_id": in.TaskID, "agent_type": "billing", "explanation": result.Explanation, "evidence_ids": result.IDs})
	wrapped, _ := json.Marshal(map[string]json.RawMessage{"output": raw})
	zero := int64(0)
	return RunResponse{Code: &zero, ExecuteID: "direct_provider:" + providerID, Data: string(wrapped)}, nil
}
