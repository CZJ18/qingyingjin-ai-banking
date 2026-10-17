package goal

import "github.com/qingyingjin/banking-service/internal/skills"

// bindCompilation attaches the immutable internal entry contract to the same
// durable parse attempt carrying ParseID, workflow ExecuteID and GoalVersionID.
// The configured parser and compiler remain the only business implementation.
func bindCompilation(v *GoalVersion) {
	d := skills.Internal("goal.parse")
	v.SkillID, v.SkillVersion = d.ID, d.Version
}
