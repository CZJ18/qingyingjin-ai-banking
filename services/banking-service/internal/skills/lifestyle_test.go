package skills

import "testing"

func TestLifestyleAdvisoryCompiler(t *testing.T) {
	b, e := Resolve(Request{Agent: "lifestyle", Tool: "compile_lifestyle_plan", Mode: "advice"})
	if e != nil || b.SkillID != "lifestyle_planning" {
		t.Fatal(b, e)
	}
	if _, e = Resolve(Request{Agent: "billing", Tool: "compile_lifestyle_plan"}); e == nil {
		t.Fatal("cross-role compilation")
	}
	if _, e = Resolve(Request{Agent: "lifestyle", Tool: "compile_lifestyle_plan", Mode: "execute"}); e == nil {
		t.Fatal("execution escalation")
	}
}
