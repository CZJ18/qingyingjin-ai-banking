package execution

import "github.com/qingyingjin/banking-service/internal/skills"

// A preparation binding never supplies authorization or an execute capability.
func preparationSkill() skills.Definition {
	return skills.Internal("execution.prepare")
}
