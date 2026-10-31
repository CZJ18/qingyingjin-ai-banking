package modelspend

import "context"

// WorkflowBinding is validated server-side; never includes a credential.
type WorkflowBinding struct {
	TaskRequestDigest string `json:"task_request_digest,omitempty"`
	TaskVersion       int64  `json:"task_version,omitempty"`
	LeaseFingerprint  string `json:"lease_fingerprint,omitempty"`
	TaskID            string `json:"task_id"`
	WorkflowID        string `json:"workflow_id"`
	WorkflowSHA256    string `json:"workflow_sha256"`
	InputSHA256       string `json:"input_sha256"`
	PolicySHA256      string `json:"policy_sha256"`
	Model             string `json:"model"`
	PriceVersion      string `json:"price_version"`
	ContextTokens     int64  `json:"context_tokens"`
	MaxOutputTokens   int64  `json:"max_output_tokens"`
	ReserveUnits      int64  `json:"reserve_units"`
}
type workflowBindingKey struct{}

func WithWorkflowBinding(ctx context.Context, b WorkflowBinding) context.Context {
	return context.WithValue(ctx, workflowBindingKey{}, b)
}
func workflowBindingFrom(ctx context.Context) *WorkflowBinding {
	b, ok := ctx.Value(workflowBindingKey{}).(WorkflowBinding)
	if !ok {
		return nil
	}
	return &b
}
