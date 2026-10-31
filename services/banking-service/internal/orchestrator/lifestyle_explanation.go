package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/qingyingjin/banking-service/internal/billing"
	"github.com/qingyingjin/banking-service/internal/coze"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/lifestyle"
	"strings"
)

type groundedFinding struct {
	ID          string   `json:"finding_id"`
	Text        string   `json:"text"`
	EvidenceIDs []string `json:"evidence_ids"`
}

// These conclusions are computed from the already independently verified Go
// result. The model selects relevance/order; it cannot rewrite a fact or amount.
func lifestyleFindings(c Call, out *AgentOutput) ([]groundedFinding, error) {
	findings := []groundedFinding{}
	add := func(id, text string, ids []string) { findings = append(findings, groundedFinding{id, text, ids}) }
	if c.Tool == "get_lifestyle_goal" {
		var v lifestyle.GoalView
		if strictJSON(out.Result, &v) != nil {
			return nil, invalidOutput()
		}
		ids := []string{stringID(v.Goal.ID)}
		add("goal.separate_targets", "减少支出与储蓄分别记录，生活目标不会自动授权金融操作。", ids)
		add("goal.health_background", "健康目标仅作为生活背景，不能用于体重变化或限制饮食的资金奖惩。", ids)
		return findings, nil
	}
	var v lifestyle.PlanView
	var result lifestyle.Result
	var snap lifestyle.Snapshot
	if strictJSON(out.Result, &v) != nil || json.Unmarshal(v.Plan.ResultJSON, &result) != nil || json.Unmarshal(v.Plan.SnapshotJSON, &snap) != nil {
		return nil, invalidOutput()
	}
	ids := []string{stringID(v.Plan.ID)}
	yuan := func(n int64) string { return fmt.Sprintf("%d.%02d元", n/100, n%100) }
	if snap.Goal.ExpenseReductionMonthlyMinor > 0 {
		add("goal.reduction_target", "你的目标是每月少花"+yuan(snap.Goal.ExpenseReductionMonthlyMinor)+"；这是减少支出目标，不等同于已经存下这笔钱。", ids)
	}
	if snap.Goal.SavingsMonthlyMinor > 0 {
		add("goal.savings_target", "你的每月储蓄目标为"+yuan(snap.Goal.SavingsMonthlyMinor)+"，实际可安排金额仍由必要生活预留和安全资金约束决定。", ids)
	}
	categoryNames := map[string]string{"shopping": "购物", "subscriptions": "订阅", "sports": "运动", "other": "其他非必要开支"}
	optionNames := map[string]string{"conservative": "保守方案", "balanced": "平衡方案", "active": "积极方案"}
	for _, option := range result.Options {
		name, known := optionNames[option.Name]
		if !known {
			continue
		}
		for i, allocation := range option.Categories {
			category, ok := billing.CanonicalCategory(allocation.Category)
			label := categoryNames[category]
			if !ok || label == "" || allocation.Protected || allocation.ProposedMinor < 0 || allocation.HistoricalMinor <= allocation.ProposedMinor {
				continue
			}
			add(fmt.Sprintf("plan.action.%s.%d", option.Name, i), name+"："+label+"的历史月支出为"+yuan(allocation.HistoricalMinor)+"，参考上限为"+yuan(allocation.ProposedMinor)+"，预计少花"+yuan(allocation.HistoricalMinor-allocation.ProposedMinor)+"。每周核对实际消费；必要支出增加时重新规划，不自动取消订阅或执行资金操作。", ids)
		}
	}
	if len(result.Options) == 3 {
		add("plan.options", "已生成保守、平衡和积极三种生活消费方案，供用户比较确认。", ids)
	}
	hasProtected := false
	for _, o := range result.Options {
		for _, a := range o.Categories {
			if a.Protected {
				hasProtected = true
				if a.ProposedMinor != a.OriginalMinor {
					return nil, invalidOutput()
				}
			}
		}
	}
	if hasProtected {
		add("plan.necessary_expenses", "方案保留正常饮食及必要生活支出。", ids)
	}
	if result.Status == "conflict" || result.ReductionGapMinor > 0 || result.SavingsGapMinor > 0 {
		add("plan.conflict", "现有数据下目标与可行预算存在缺口，需要用户重新确认调整条件，原目标保持不变。", ids)
	}
	if result.Status == "needs_information" {
		add("plan.missing_information", "当前缺少完整规划依据，需先补充信息，不能形成可执行资金安排。", ids)
	}
	if len(snap.EvidenceTransactionIDs) > 0 {
		add("plan.historical_evidence", "生活方案参考了实际历史交易，历史消费不能证明用户健康状况或订阅使用情况。", snap.EvidenceTransactionIDs)
	}
	add("plan.forecast_not_cash", "预计减少支出属于预算建议，不能当作当前可用余额或直接计入目标金。", ids)
	add("plan.separate_authorization", "方案比较和偏好确认不代表金融执行授权，资金操作仍需独立确认。", ids)
	return findings, nil
}

func renderLifestyleFindings(selected []string, findings []groundedFinding, out *AgentOutput) error {
	if len(selected) < 1 || len(selected) > 3 {
		return entity.Fail("MODEL_OUTPUT_INVALID", "select one to three verified conclusions")
	}
	allowed := map[string]groundedFinding{}
	for _, f := range findings {
		allowed[f.ID] = f
	}
	available := map[string]bool{}
	for _, ev := range out.Evidence {
		available[ev.ID] = true
	}
	seen := map[string]bool{}
	used := map[string]bool{}
	text := []string{}
	ids := []string{}
	for _, id := range selected {
		f, ok := allowed[id]
		if !ok || seen[id] {
			return entity.Fail("MODEL_OUTPUT_INVALID", "model selected a nonexistent or repeated conclusion")
		}
		seen[id] = true
		text = append(text, f.Text)
		for _, ev := range f.EvidenceIDs {
			if !available[ev] {
				return invalidOutput()
			}
			if !used[ev] {
				used[ev] = true
				ids = append(ids, ev)
			}
		}
	}
	// The Coze path must retain the same mandatory caveats as the direct
	// adapter. Selecting only an appealing target/action may hide a known gap.
	for _, required := range []string{"plan.missing_information", "plan.conflict"} {
		if _, exists := allowed[required]; exists && !seen[required] {
			return entity.Fail("MODEL_OUTPUT_INVALID", "model omitted a mandatory planning limitation")
		}
	}
	out.Explanation = strings.Join(text, "")
	out.ExplanationEvidenceIDs = ids
	out.ExplanationFindingIDs = selected
	return nil
}

func (x *HTTPExecutor) explainLifestyle(ctx context.Context, c Call, out *AgentOutput) error {
	findings, e := lifestyleFindings(c, out)
	if e != nil {
		return e
	}
	payload, _ := json.Marshal(struct {
		TaskID    string            `json:"task_id"`
		AgentType string            `json:"agent_type"`
		Findings  []groundedFinding `json:"findings"`
	}{c.TaskID, c.AgentType, findings})
	r, e := x.coze.RunSyncWorkflow(ctx, coze.RunRequest{WorkflowID: x.workflows[c.AgentType], TraceID: c.Meta.TraceID, Parameters: map[string]any{"input_string": string(payload)}})
	if e != nil {
		if code := coze.BudgetCode(e); code != "" {
			return entity.Fail(code, "model budget refused or previous outcome unknown")
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return entity.Fail("COZE_UNAVAILABLE", "Lifestyle explanation workflow unavailable")
	}
	var result struct {
		TaskID     string   `json:"task_id"`
		AgentType  string   `json:"agent_type"`
		FindingIDs []string `json:"finding_ids"`
	}
	if e = workflowOutput(r, &result); e != nil {
		return e
	}
	if result.TaskID != c.TaskID || result.AgentType != c.AgentType {
		return entity.Fail("MODEL_OUTPUT_INVALID", "Lifestyle explanation identity mismatch")
	}
	if e = renderLifestyleFindings(result.FindingIDs, findings, out); e != nil {
		return e
	}
	out.WorkflowExecuteID = r.ExecuteID
	return nil
}
