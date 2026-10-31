package coze

import (
	"strings"
	"testing"
)

func TestBillingQualitySeparatesActualMissingAdviceFromSafeFacts(t *testing.T) {
	// Exact text from this round's real result: truthful facts and limitations,
	// but no action or way to review it. No model is called by this regression.
	text := "账目仅涵盖已记录交易，完整性尚未核实，且未提供可用账户余额。支出主要集中在餐饮与购物两类，收入与支出情况以页面展示的汇总结算为准。"
	got := AssessBillingExplanation(text)
	if got.HasActionAndReview || got.Action != "" || got.Review != "" || got.FourSections {
		t.Fatal("observed generic answer incorrectly credited with concrete advice", got)
	}
}

func TestBillingQualityObservesCategoryActionAndReviewIndependently(t *testing.T) {
	text := "结论：已记录支出涉及餐饮与购物。\n依据：依据所列账单分类及明细。\n局限：账单完整性尚未核实，不能承诺下月节省金额。\n建议：行动：先核对购物明细，标记必要采购与可延后项目；复核：记录试行期间的同类支出，再比较是否下降。"
	got := AssessBillingExplanation(text)
	if !got.FourSections || !got.HasActionAndReview || len(got.MissingSections) != 0 || !strings.Contains(got.Action, "购物") || !strings.Contains(got.Review, "记录") {
		t.Fatal(got)
	}
	// Missing titles are a presentation gap, not proof of unsafe or useless
	// prose. The rubric must not invent a new task-admission failure.
	unlabelled := AssessBillingExplanation("行动：先核对餐饮明细并标记可调整项目；复核：记录试行期间的餐饮支出，再比较同类消费变化。")
	if unlabelled.FourSections || !unlabelled.HasActionAndReview {
		t.Fatal("format and observable action were conflated", unlabelled)
	}
}

func TestBillingQualityDoesNotCreditEmptyOrGenericSlogans(t *testing.T) {
	for _, suggestion := range []string{"合理消费。", "行动：合理消费；复核：注意节约。", "行动：核对购物消费明细；复核：", "行动：记录同类消费明细；复核：记录同类消费明细。"} {
		text := "结论：支出已记录。依据：所列账单。局限：完整性未核实。建议：" + suggestion
		if got := AssessBillingExplanation(text); got.HasActionAndReview {
			t.Fatal("generic or missing action/review credited", suggestion, got)
		}
	}
}

func TestSharedBillingGuidanceDoesNotChangePublishedWorkflowContract(t *testing.T) {
	for _, prompt := range []string{DirectBillingExplanationPrompt, BillingWorkflowExplanationPrompt} {
		if !strings.HasSuffix(prompt, BillingExplanationGuidance) {
			t.Fatal("transport guidance drifted")
		}
		for _, required := range []string{"statement completeness is unverified", "Preserve essential spending and reserves", "Do not promise any savings amount", "No Markdown, tools, SQL, financial actions or additional calls", "替代：", "weekly", "unspecified reversible adjustment", "postpone a user-confirmed optional purchase"} {
			if !strings.Contains(prompt, required) {
				t.Fatal("financial boundary lost", required)
			}
		}
	}
	for _, field := range []string{"task_id", "agent_type", "explanation", "evidence_ids"} {
		if !strings.Contains(BillingWorkflowExplanationPrompt, field) {
			t.Fatal("existing Coze field lost", field)
		}
	}
}
