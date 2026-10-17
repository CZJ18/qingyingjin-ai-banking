package goal

import "testing"

func TestCompilationBindingPreservesAttemptEvidence(t *testing.T) {
	v := GoalVersion{ParseID: "attempt", ExecuteID: "workflow-execution", Version: 3}
	bindCompilation(&v)
	if v.SkillID != "goal_compilation" || v.SkillVersion != 1 || v.ParseID != "attempt" || v.ExecuteID != "workflow-execution" || v.Version != 3 {
		t.Fatal(v)
	}
}
