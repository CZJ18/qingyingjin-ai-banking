package bankingscope

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"time"
)

// ValidateInput binds the exact submitted Parameters string to the gateway's
// authenticated task grant before Coze adapts or runs any workflow node.
// Admission neither reserves money nor consumes the model-call claim.
func ValidateInput(ctx context.Context, workflowID, parameters string) error {
	s, ok := ctx.Value(key{}).(*scope)
	if !ok || s.workflow != workflowID || len(parameters) > 1048576 {
		return ErrDenied
	}
	sum := sha256.Sum256([]byte(parameters))
	payload, err := json.Marshal(struct {
		WorkflowID     string `json:"workflow_id"`
		WorkflowSHA256 string `json:"workflow_sha256"`
		InputSHA256    string `json:"input_sha256"`
	}{workflowID, s.digest, hex.EncodeToString(sum[:])})
	if err != nil {
		return ErrDenied
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 3 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return ErrDenied }}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.config.Origin+"/v1/workflow-admit", bytes.NewReader(payload))
	if err != nil {
		return ErrDenied
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+s.config.Capability)
	req.GetBody = nil
	response, err := client.Do(req)
	if err != nil {
		return ErrDenied
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil || len(body) > 4096 || response.ContentLength > 4096 || response.StatusCode != http.StatusNoContent {
		return ErrDenied
	}
	return nil
}
