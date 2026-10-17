// Package skills defines the immutable, server-owned capability contract.
package skills

type Pair struct {
	Agent string `json:"agent_type"`
	Tool  string `json:"tool"`
	Mode  string `json:"execution_mode"`
}
type Definition struct {
	ID               string   `json:"skill_id"`
	Version          int64    `json:"skill_version"`
	Owner            string   `json:"owner"`
	Description      string   `json:"description"`
	AllowedPairs     []Pair   `json:"allowed_pairs"`
	InternalEntry    string   `json:"internal_entry"`
	RequiredEvidence []string `json:"required_evidence"`
	RuleVersion      string   `json:"rule_version"`
}
type Request struct {
	SkillID      string `json:"skill_id"`
	SkillVersion int64  `json:"skill_version"`
	Agent        string `json:"agent_type"`
	Tool         string `json:"tool"`
	Mode         string `json:"execution_mode"`
}
type Binding = Request
