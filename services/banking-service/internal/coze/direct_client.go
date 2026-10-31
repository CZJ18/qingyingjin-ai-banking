package coze

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/qingyingjin/banking-service/internal/modelspend"
	"github.com/qingyingjin/banking-service/internal/outbound"
	"github.com/qingyingjin/banking-service/internal/skills"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"strings"
	"sync/atomic"
	"time"
)

// DirectRunner uses the SAME monetary gateway, never a supplier SDK or key.
// RunResponse is the existing executor DTO, not evidence of a Coze run.
type DirectRunner struct {
	bridge *workflowBridge
	model  string
	max    int64
}

const DirectPlannerPrompt = `Return only a JSON object with a tasks array. Plan read-only billing analysis using the supplied instruction and bindings. Allowed skill: billing_analysis version 1, agent_type billing, execution_mode read_only, tool generate_billing_report. Each task needs key, agent_type, tool, skill_id, skill_version, execution_mode, parameters, dependencies, priority, use_coze=false. Parameters may use only the supplied account_id, from, to. Never add accounts, SQL, financial operations, intentions, tool calls or specialist model calls. Do not invent financial results.`

const DirectSmokeInput = "Reply with BANKING_SMOKE_OK. No personal financial data; no tools or financial operations."

// DirectSmokeRequest has no user-controlled text, financial data, or tool schema.
// Its exact bytes must be separately approved; a planner approval cannot authorize it.
func DirectSmokeRequest(model string, max int64) ([]byte, error) {
	if model == "" || max < 1 || max > 128 {
		return nil, ErrMonetaryAdmissionRequired
	}
	return json.Marshal(struct {
		Model    string              `json:"model"`
		Messages []map[string]string `json:"messages"`
		Max      int64               `json:"max_tokens"`
		Thinking map[string]string   `json:"thinking"`
	}{model, []map[string]string{{"role": "user", "content": DirectSmokeInput}}, max, map[string]string{"type": "disabled"}})
}

func DirectRequest(model string, max int64, input string) ([]byte, error) {
	return directRequestWithPrompt(model, max, input, DirectPlannerPrompt)
}

func directRequestWithPrompt(model string, max int64, input, prompt string) ([]byte, error) {
	if model == "" || max < 1 || max > 4096 || input == "" || len(input) > 65536 {
		return nil, ErrMonetaryAdmissionRequired
	}
	return json.Marshal(struct {
		Model    string              `json:"model"`
		Messages []map[string]string `json:"messages"`
		Max      int64               `json:"max_tokens"`
		Format   map[string]string   `json:"response_format"`
		Thinking map[string]string   `json:"thinking"`
	}{model, []map[string]string{{"role": "system", "content": prompt}, {"role": "user", "content": input}}, max, map[string]string{"type": "json_object"}, map[string]string{"type": "disabled"}})
}
func NewDirectRunner(base, token, model string, max int64, p outbound.Policy) (*DirectRunner, error) {
	u, e := url.Parse(base)
	if e != nil || p.Environment != "test" || p.Validate(base) != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || u.Path != "" || len(token) < 32 || len(token) > 512 || strings.ContainsAny(token, " \r\n\t") || model == "" || max < 1 || max > 4096 {
		return nil, errors.New("invalid isolated direct gateway configuration")
	}
	return &DirectRunner{&workflowBridge{base: base, token: token, client: &http.Client{Timeout: 95 * time.Second, Transport: &http.Transport{Proxy: nil}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, model, max}, nil
}
func (*DirectRunner) DirectProvider() bool { return true }
func (d *DirectRunner) AdmissionSnapshot(ctx context.Context, bearer string) (map[string]any, error) {
	return (&Client{bridge: d.bridge}).AdmissionSnapshot(ctx, bearer)
}
func (d *DirectRunner) RunSyncWorkflow(ctx context.Context, in RunRequest) (RunResponse, error) {
	var out RunResponse
	credential, _ := ctx.Value(delegationKey{}).(string)
	smoke := in.WorkflowID == "direct_provider_smoke"
	explanation := in.WorkflowID == DirectBillingExplanationWorkflow
	lifestyleExplanation := in.WorkflowID == DirectLifestyleExplanationWorkflow
	scenarios := in.WorkflowID == DirectScenarioPlannerWorkflow
	if (!smoke && !explanation && !lifestyleExplanation && !scenarios && in.WorkflowID != "direct_provider") || credential == "" || len(credential) > 8192 || strings.ContainsAny(credential, " \r\n\t") {
		return out, ErrMonetaryAdmissionRequired
	}
	var raw []byte
	var e error
	if smoke {
		if len(in.Parameters) != 0 {
			return out, ErrMonetaryAdmissionRequired
		}
		raw, e = DirectSmokeRequest(d.model, d.max)
	} else {
		input, ok := in.Parameters["input_string"].(string)
		if !ok || len(in.Parameters) != 1 {
			return out, ErrMonetaryAdmissionRequired
		}
		if scenarios {
			raw, e = DirectScenarioRequest(d.model, d.max, input)
		} else if lifestyleExplanation {
			raw, e = DirectLifestyleExplanationRequest(d.model, d.max, input)
		} else if explanation {
			raw, e = DirectBillingExplanationRequest(d.model, d.max, input)
		} else {
			raw, e = DirectRequest(d.model, d.max, input)
		}
	}
	if e != nil {
		return out, e
	}
	var wrote atomic.Bool
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		WroteHeaders: func() { wrote.Store(true) },
		WroteRequest: func(httptrace.WroteRequestInfo) { wrote.Store(true) },
	})
	r, e := http.NewRequestWithContext(ctx, "POST", d.bridge.base+"/v1/chat/completions", bytes.NewReader(raw))
	if e != nil {
		return out, ErrMonetaryAdmissionRequired
	}
	r.Header.Set("Authorization", "Bearer "+d.bridge.token)
	r.Header.Set("X-Banking-Authorization", "Bearer "+credential)
	r.Header.Set("Content-Type", "application/json")
	response, e := d.bridge.client.Do(r)
	if e != nil {
		if !wrote.Load() {
			return out, ErrBudgetUnavailable
		}
		return out, ErrOutcomeUnknown
	}
	defer response.Body.Close()
	data, e := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
	if e != nil || len(data) > 2<<20 {
		return out, ErrOutcomeUnknown
	}
	if response.StatusCode != 200 {
		switch gatewayQuotaCode(data) {
		case "MODEL_CONTEXT_LIMIT_EXCEEDED":
			return out, ErrContextLimit
		case "MODEL_EXECUTION_CONFIRMATION_REQUIRED":
			return out, ErrExecutionConfirmationRequired
		case "MODEL_APPROVAL_REQUIRED", "DIRECT_REQUEST_BINDING_MISMATCH":
			return out, ErrApprovalRequired
		case "POLICY_UNAVAILABLE":
			if response.StatusCode == http.StatusServiceUnavailable {
				return out, ErrApprovalRequired
			}
		case "BUDGET_UNAVAILABLE", "MODEL_BUDGET_UNAVAILABLE":
			if response.StatusCode == http.StatusServiceUnavailable {
				return out, ErrBudgetUnavailable
			}
		case "MODEL_APPROVAL_LIMIT_EXCEEDED":
			return out, ErrApprovalLimit
		case "BUDGET_EXCEEDED":
			return out, ErrSpendQuotaExceeded
		case "USER_MONTHLY_BUDGET_EXCEEDED":
			return out, ErrMonthlyQuotaExceeded
		case "REQUEST_CONFLICT":
			return out, ErrRequestConflict
		case "SUPPLIER_OUTCOME_UNKNOWN":
			return out, ErrOutcomeUnknown
		}
		return out, ErrMonetaryAdmissionRequired
	}
	var v struct {
		ID      string `json:"id"`
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			Finish string `json:"finish_reason"`
		} `json:"choices"`
	}
	if json.Unmarshal(data, &v) != nil || v.ID == "" || v.Model != d.model || len(v.Choices) != 1 || v.Choices[0].Finish != "stop" {
		return out, ErrOutcomeUnknown
	}
	if smoke {
		if v.Choices[0].Message.Content != "BANKING_SMOKE_OK" {
			return out, ErrOutcomeUnknown
		}
		result, _ := json.Marshal(map[string]any{"model_response": v.Choices[0].Message.Content, "model": v.Model, "smoke_only": true})
		wrapped, _ := json.Marshal(map[string]json.RawMessage{"output": result})
		zero := int64(0)
		return RunResponse{Code: &zero, ExecuteID: "direct_provider:" + v.ID, Data: string(wrapped)}, nil
	}
	if explanation {
		return directExplanationResponse(in.Parameters["input_string"].(string), v.Choices[0].Message.Content, v.ID)
	}
	if lifestyleExplanation {
		return directLifestyleResponse(in.Parameters["input_string"].(string), v.Choices[0].Message.Content, v.ID)
	}
	var plan struct {
		Tasks []json.RawMessage `json:"tasks"`
	}
	content := []byte(v.Choices[0].Message.Content)
	maxTasks := 4
	if scenarios {
		maxTasks = 8
	}
	if modelspend.StrictJSON(content, &plan, 1<<20) != nil || len(plan.Tasks) < 1 || len(plan.Tasks) > maxTasks {
		return out, ErrOutcomeUnknown
	}
	for _, rawTask := range plan.Tasks {
		var task struct {
			Agent   string `json:"agent_type"`
			Tool    string `json:"tool"`
			Skill   string `json:"skill_id"`
			Version int64  `json:"skill_version"`
			Mode    string `json:"execution_mode"`
			UseCoze bool   `json:"use_coze"`
		}
		if json.Unmarshal(rawTask, &task) != nil {
			return out, ErrOutcomeUnknown
		}
		if scenarios {
			if !scenarioPlannerTaskAllowed(task.Agent, task.Tool, task.Skill, task.Version, task.Mode, task.UseCoze) {
				return out, ErrOutcomeUnknown
			}
		} else if task.Agent != "billing" || task.Mode != "read_only" || task.UseCoze {
			return out, ErrOutcomeUnknown
		}
		if _, e = skills.Resolve(skills.Request{SkillID: task.Skill, SkillVersion: task.Version, Agent: task.Agent, Tool: task.Tool, Mode: task.Mode}); e != nil {
			return out, ErrOutcomeUnknown
		}
	}
	wrapped, _ := json.Marshal(map[string]json.RawMessage{"output": content})
	zero := int64(0)
	return RunResponse{Code: &zero, ExecuteID: "direct_provider:" + v.ID, Data: string(wrapped)}, nil
}
