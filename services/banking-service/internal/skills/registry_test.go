package skills

import "testing"

func TestBindingCannotEscalate(t *testing.T) {
	for _, in := range []Request{
		{SkillID: "billing_analysis", SkillVersion: 1, Agent: "billing", Tool: "create_cancel_intent", Mode: "intent"},
		{SkillID: "savings_planning", SkillVersion: 1, Agent: "savings", Tool: "replan_financial_plan", Mode: "execute"},
		{SkillID: "billing_analysis", SkillVersion: 99, Agent: "billing", Tool: "get_transactions"},
		{SkillID: "unknown", SkillVersion: 1, Agent: "billing", Tool: "get_transactions"},
		{SkillID: "billing_analysis", Agent: "billing", Tool: "get_transactions"},
		{SkillVersion: 1, Agent: "billing", Tool: "get_transactions"},
		{Agent: "supervisor", Tool: "goal.parse"},
	} {
		if _, err := Resolve(in); err == nil {
			t.Fatalf("accepted unauthorized binding: %+v", in)
		}
	}
}
func TestLegacyBinding(t *testing.T) {
	b, err := Resolve(Request{Agent: "billing", Tool: "get_transactions"})
	if err != nil || b.SkillID != "billing_analysis" || b.SkillVersion != 1 || b.Mode != "read_only" {
		t.Fatalf("%+v %v", b, err)
	}
}
func TestCatalogIsolation(t *testing.T) {
	original := Catalog()
	mutated := Catalog()
	mutated[0].ID = "injected"
	mutated[1].AllowedPairs[0].Tool = "execute"
	mutated[0].RequiredEvidence[0] = "injected"
	fresh := Catalog()
	if fresh[0].ID != original[0].ID || fresh[1].AllowedPairs[0].Tool != original[1].AllowedPairs[0].Tool || fresh[0].RequiredEvidence[0] != original[0].RequiredEvidence[0] {
		t.Fatal("mutable security policy")
	}
}
