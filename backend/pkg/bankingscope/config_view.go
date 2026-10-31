package bankingscope

import (
	"encoding/hex"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"strconv"
)

type ConfigurationView struct {
	Mode            string            `json:"mode"`
	WorkflowPins    map[string]string `json:"workflow_pins"`
	GatewayOrigin   string            `json:"gateway_origin"`
	Model           string            `json:"model"`
	MaxOutputTokens int               `json:"max_output_tokens"`
}

// MaxConfiguredOutputTokens is the existing protected Coze scope limit. The
// gateway's manifest preflight shares it instead of accepting an unusable pin.
const MaxConfiguredOutputTokens = 4096

// ConfigView exposes no credentials and is not an authorization grant. The same
// validation powers actual scope admission, so incomplete config stays closed.
func ConfigView() (*ConfigurationView, error) {
	var pins map[string]string
	if json.Unmarshal([]byte(os.Getenv("BANKING_COZE_WORKFLOW_DIGESTS")), &pins) != nil || len(pins) == 0 {
		return nil, ErrDenied
	}
	for workflow, digest := range pins {
		if workflow == "" || len(digest) != 64 {
			return nil, ErrDenied
		}
		if _, err := hex.DecodeString(digest); err != nil {
			return nil, ErrDenied
		}
	}
	origin := os.Getenv("BANKING_MODEL_GATEWAY_ORIGIN")
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.Opaque != "" {
		return nil, ErrDenied
	}
	if u.Scheme == "http" {
		ip := net.ParseIP(u.Hostname())
		if ip == nil || !ip.IsLoopback() {
			return nil, ErrDenied
		}
	}
	model := os.Getenv("BANKING_MODEL_NAME")
	max, err := strconv.Atoi(os.Getenv("BANKING_MODEL_MAX_OUTPUT_TOKENS"))
	if err != nil || max < 1 || max > MaxConfiguredOutputTokens || model == "" {
		return nil, ErrDenied
	}
	return &ConfigurationView{"banking-task-scope-v1", pins, origin, model, max}, nil
}
