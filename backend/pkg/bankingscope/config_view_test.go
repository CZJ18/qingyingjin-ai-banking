package bankingscope

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestConfigViewIsClosedUntilTrustedConfigurationComplete(t *testing.T) {
	for _, name := range []string{"BANKING_COZE_WORKFLOW_DIGESTS", "BANKING_MODEL_GATEWAY_ORIGIN", "BANKING_MODEL_NAME", "BANKING_MODEL_MAX_OUTPUT_TOKENS"} {
		_, err := configured(t, safeCanvas)
		if err != nil {
			t.Fatal(err)
		}
		t.Setenv(name, "")
		if _, err = ConfigView(); err == nil {
			t.Fatalf("missing %s accepted", name)
		}
	}
}

func TestConfigViewReportsOnlyReviewedScopeMetadata(t *testing.T) {
	_, err := configured(t, safeCanvas)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OPENAI_API_KEY", "never-export-provider-key")
	view, err := ConfigView()
	if err != nil {
		t.Fatal(err)
	}
	if view.Mode != "banking-task-scope-v1" || view.WorkflowPins["42"] == "" || view.GatewayOrigin != "http://127.0.0.1:8099" || view.Model != "reviewed-model" || view.MaxOutputTokens != 512 {
		t.Fatal("scope metadata mismatch")
	}
	encoded, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"never-export-provider-key", "bgc1.", "Capability", "api_key"} {
		if strings.Contains(string(encoded), secret) {
			t.Fatal("secret metadata exposed")
		}
	}
	t.Setenv("BANKING_COZE_WORKFLOW_DIGESTS", `{"42":"invalid"}`)
	if _, err = ConfigView(); err == nil {
		t.Fatal("invalid workflow pin accepted")
	}
}
