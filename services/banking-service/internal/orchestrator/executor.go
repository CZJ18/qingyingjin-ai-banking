package orchestrator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/skills"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/qingyingjin/banking-service/internal/billing"
	"github.com/qingyingjin/banking-service/internal/coze"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/identity"
	"github.com/qingyingjin/banking-service/internal/lifestyle"
	"github.com/qingyingjin/banking-service/internal/planning"
	"github.com/qingyingjin/banking-service/internal/service"
	"github.com/qingyingjin/banking-service/internal/subscription"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

type WorkflowRunner interface {
	RunSyncWorkflow(context.Context, coze.RunRequest) (coze.RunResponse, error)
}

// HTTPExecutor uses only the protected Banking API, never a database connection.
// Coze receives authoritative results and identifiers, never Banking credentials.
type HTTPExecutor struct {
	base      string
	auth      *identity.Auth
	http      *http.Client
	coze      WorkflowRunner
	workflows map[string]string
	gateway   ProposalGateway
}

func NewHTTPExecutor(baseURL string, auth *identity.Auth, runner WorkflowRunner, workflowIDs map[string]string, gateway ProposalGateway) (*HTTPExecutor, error) {
	u, e := url.Parse(baseURL)
	if e != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || auth == nil {
		return nil, errors.New("invalid Banking executor configuration")
	}
	ids := map[string]string{}
	for role, id := range workflowIDs {
		if id != "" {
			if _, e := planning.ID(id); e != nil {
				return nil, errors.New("invalid configured workflow ID")
			}
		}
		ids[role] = id
	}
	return &HTTPExecutor{base: strings.TrimRight(baseURL, "/"), auth: auth, coze: runner, workflows: ids, gateway: gateway, http: &http.Client{Timeout: 60 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func invalidOutput() error {
	return entity.Fail("TOOL_OUTPUT_INVALID", "protected Banking tool returned invalid structured evidence")
}

func (x *HTTPExecutor) Run(ctx context.Context, c Call) (AgentOutput, error) {
	ctx = coze.WithIdentity(ctx, c.Meta.UserID, "specialist:"+c.TaskID+":"+c.Tool)
	out := AgentOutput{TaskID: c.TaskID, AgentType: c.AgentType, Status: "succeeded", Evidence: []Evidence{}, Errors: []TaskError{}, NextActions: []NextAction{}}
	b, err := skills.Resolve(skills.Request{SkillID: c.SkillID, SkillVersion: c.SkillVersion, Agent: c.AgentType, Tool: c.Tool, Mode: c.Mode})
	if err != nil {
		return out, err
	}
	c.SkillID, c.SkillVersion, c.Mode = b.SkillID, b.SkillVersion, b.Mode
	p := ToolPolicy{Agent: b.Agent, Mode: b.Mode}
	if c.Meta.UserID <= 0 || c.TaskID == "" {
		return out, entity.Fail("INVALID_ARGUMENT", "owned task identity required")
	}
	if e := ValidateParameters(c.Tool, c.Parameters, true); e != nil {
		return out, e
	}
	if c.UseCoze && (x.coze == nil || x.workflows[c.AgentType] == "") {
		return out, entity.Fail("COZE_UNAVAILABLE", "published specialist workflow is not configured")
	}
	if p.Mode == "user_confirmation" {
		return out, entity.Fail("CONFIRMATION_REQUIRED", "explicit user confirmation required")
	}
	if p.Mode == "intent" {
		if !c.Confirmed {
			return out, entity.Fail("CONFIRMATION_REQUIRED", "intent proposal requires explicit user confirmation")
		}
		if c.Tool != "create_cancel_intent" || x.gateway == nil {
			return out, entity.Fail("GATEWAY_UNAVAILABLE", "module 8 proposal gateway is required")
		}
		raw, e := x.gateway.Propose(ctx, c)
		if e != nil {
			return out, e
		}
		var intent entity.SubscriptionActionIntent
		if e = strictJSON(raw, &intent); e != nil || intent.UserID != c.Meta.UserID || intent.ID <= 0 || intent.Status != "pending_approval" || intent.Action != "cancel" {
			return out, invalidOutput()
		}
		var requested subscription.IntentInput
		_ = json.Unmarshal(c.Parameters, &requested)
		if strconv.FormatInt(intent.SubscriptionID, 10) != requested.SubscriptionID || intent.SubscriptionVersion != requested.ExpectedVersion {
			return out, invalidOutput()
		}
		out.Result = raw
		out.Evidence = append(out.Evidence, Evidence{Type: "intent", ID: strconv.FormatInt(intent.ID, 10)})
		out.NextActions = append(out.NextActions, NextAction{Type: "confirm_cancel_intent", RequiresConfirmation: true})
	} else {
		raw, e := x.bank(ctx, c)
		if e != nil {
			return out, e
		}
		out.Result = raw
		if e = x.validateBank(c, &out); e != nil {
			return out, e
		}
	}
	if c.UseCoze {
		if e := x.explain(ctx, c, &out); e != nil {
			return out, e
		}
	}
	return out, nil
}

func (x *HTTPExecutor) bank(ctx context.Context, c Call) (json.RawMessage, error) {
	method, path, body := "POST", "/api/v1/"+c.AgentType+"/"+c.Tool, c.Parameters
	if c.AgentType == "subscription" {
		path = "/api/v1/subscriptions/" + c.Tool
	}
	if c.AgentType == "lifestyle" {
		if c.Tool == "compile_lifestyle_plan" {
			var in lifestyle.CompileToolInput
			if e := strictJSON(c.Parameters, &in); e != nil {
				return nil, e
			}
			path = "/api/v2/lifestyle-goals/" + strconv.FormatInt(in.GoalID, 10) + "/compile"
			body, _ = json.Marshal(in.CompileInput)
		} else {
			var in lifestyle.IDInput
			if e := strictJSON(c.Parameters, &in); e != nil {
				return nil, e
			}
			method, body = "GET", nil
			if c.Tool == "get_lifestyle_goal" {
				path = "/api/v2/lifestyle-goals/" + strconv.FormatInt(in.GoalID, 10)
			} else {
				path = "/api/v2/lifestyle-plans/" + strconv.FormatInt(in.PlanID, 10)
			}
		}
	}
	if c.AgentType == "savings" {
		var in planning.IDInput
		if e := strictJSON(c.Parameters, &in); e != nil {
			return nil, e
		}
		path = "/api/v1/financial-plans/" + in.PlanID
		method = "GET"
		body = nil
		if c.Tool == "replan_financial_plan" {
			path += "/replan"
			method = "POST"
			body = json.RawMessage(`{}`)
		}
	}
	token, e := x.auth.IssueAgent(strconv.FormatInt(c.Meta.UserID, 10), c.AgentType, c.TaskID, 2*time.Minute)
	if e != nil {
		return nil, entity.Fail("DEPENDENCY_UNAVAILABLE", "Banking task identity cannot be issued")
	}
	q, e := http.NewRequestWithContext(ctx, method, x.base+path, bytes.NewReader(body))
	if e != nil {
		return nil, entity.Fail("INVALID_ARGUMENT", "invalid Banking request")
	}
	q.Header.Set("Authorization", "Bearer "+token)
	q.Header.Set("Content-Type", "application/json")
	q.Header.Set("X-Trace-ID", c.Meta.TraceID)
	q.Header.Set("X-Task-ID", c.TaskID)
	if c.InvocationKey != "" {
		q.Header.Set("Idempotency-Key", c.InvocationKey)
	}
	r, e := x.http.Do(q)
	if e != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, entity.Fail("DEPENDENCY_UNAVAILABLE", "Banking tool transport unavailable")
	}
	defer r.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(r.Body, 2*1024*1024+1))
	if e != nil || len(raw) > 2*1024*1024 {
		return nil, invalidOutput()
	}
	var envelope struct {
		Code      string          `json:"code"`
		Message   string          `json:"message"`
		RequestID string          `json:"request_id"`
		TraceID   string          `json:"trace_id"`
		TaskID    string          `json:"task_id"`
		Data      json.RawMessage `json:"data"`
	}
	if e = strictJSON(raw, &envelope); e != nil {
		return nil, invalidOutput()
	}
	if (r.StatusCode != http.StatusOK && !(c.Tool == "compile_lifestyle_plan" && r.StatusCode == http.StatusCreated)) || envelope.Code != "OK" {
		switch envelope.Code {
		case "NOT_FOUND", "FORBIDDEN", "INPUT_REQUIRED", "INVALID_ARGUMENT", "VERSION_CONFLICT", "STATE_CONFLICT":
			return nil, entity.Fail(envelope.Code, "Banking tool rejected task")
		}
		return nil, entity.Fail("DEPENDENCY_UNAVAILABLE", "Banking tool failed")
	}
	if len(envelope.Data) == 0 || bytes.Equal(envelope.Data, []byte("null")) {
		return nil, invalidOutput()
	}
	return envelope.Data, nil
}

var outputSchemaOnce sync.Once
var outputSchemas map[string]*jsonschema.Schema
var outputSchemaErr error

func rawSchema(name string, raw []byte) error {
	outputSchemaOnce.Do(func() {
		outputSchemas = map[string]*jsonschema.Schema{}
		for _, n := range []string{"billing-report", "subscription-report", "subscription-detail"} {
			fs := billing.Schemas
			if strings.HasPrefix(n, "subscription") {
				fs = subscription.Schemas
			}
			b, e := fs.ReadFile("schemas/" + n + ".schema.json")
			if e != nil {
				outputSchemaErr = e
				return
			}
			var doc any
			d := json.NewDecoder(bytes.NewReader(b))
			d.UseNumber()
			if e = d.Decode(&doc); e != nil {
				outputSchemaErr = e
				return
			}
			compiler := jsonschema.NewCompiler()
			uri := "urn:qingyingjin:orchestrator:" + n
			if e = compiler.AddResource(uri, doc); e != nil {
				outputSchemaErr = e
				return
			}
			outputSchemas[n], e = compiler.Compile(uri)
			if e != nil {
				outputSchemaErr = e
				return
			}
		}
	})
	if outputSchemaErr != nil {
		return entity.Fail("DEPENDENCY_UNAVAILABLE", "specialist schemas unavailable")
	}
	var doc any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if d.Decode(&doc) != nil {
		return invalidOutput()
	}
	var extra any
	if d.Decode(&extra) != io.EOF || outputSchemas[name].Validate(doc) != nil {
		return invalidOutput()
	}
	return nil
}

func (x *HTTPExecutor) validateBank(c Call, out *AgentOutput) error {
	switch c.AgentType {
	case "lifestyle":
		var in lifestyle.IDInput
		_ = json.Unmarshal(c.Parameters, &in)
		if c.Tool == "get_lifestyle_goal" {
			var v lifestyle.GoalView
			if strictJSON(out.Result, &v) != nil || v.Goal.ID != in.GoalID || v.Goal.UserID != c.Meta.UserID || len(v.Versions) == 0 {
				return invalidOutput()
			}
			for _, version := range v.Versions {
				if version.UserID != c.Meta.UserID || version.GoalID != in.GoalID {
					return invalidOutput()
				}
				if _, e := lifestyle.DecodeSpec(version.SpecJSON, version.Text); e != nil {
					return invalidOutput()
				}
			}
			out.Evidence = append(out.Evidence, Evidence{Type: "goal", ID: strconv.FormatInt(v.Goal.ID, 10)})
		} else {
			var compile lifestyle.CompileToolInput
			if c.Tool == "compile_lifestyle_plan" {
				_ = json.Unmarshal(c.Parameters, &compile)
			}
			var v lifestyle.PlanView
			var snap lifestyle.Snapshot
			var result lifestyle.Result
			if strictJSON(out.Result, &v) != nil || v.Plan.ID <= 0 || c.Tool != "compile_lifestyle_plan" && v.Plan.ID != in.PlanID || c.Tool == "compile_lifestyle_plan" && (v.Plan.GoalID != compile.GoalID || v.Plan.GoalVersion != compile.ExpectedVersion) || v.Plan.UserID != c.Meta.UserID || json.Unmarshal(v.Plan.SnapshotJSON, &snap) != nil || json.Unmarshal(v.Plan.ResultJSON, &result) != nil {
				return invalidOutput()
			}
			expected, e := lifestyle.Calculate(snap)
			if e != nil || !reflect.DeepEqual(expected, result) {
				return invalidOutput()
			}
			for _, r := range v.Recommendations {
				if r.UserID != c.Meta.UserID || r.PlanID != v.Plan.ID || r.ExpectedImpactMinor < 0 {
					return invalidOutput()
				}
			}
			out.Evidence = append(out.Evidence, Evidence{Type: "plan", ID: strconv.FormatInt(v.Plan.ID, 10)})
			for _, id := range snap.EvidenceTransactionIDs {
				out.Evidence = append(out.Evidence, Evidence{Type: "transaction", ID: id})
			}
		}
	case "billing":
		if e := rawSchema("billing-report", out.Result); e != nil {
			return e
		}
		var r billing.Report
		if e := strictJSON(out.Result, &r); e != nil || r.UserID != c.Meta.UserID || r.Tool != c.Tool || r.FinancialExecutionEnabled || billing.ValidateReport(r) != nil {
			return invalidOutput()
		}
		for _, a := range r.Accounts {
			if a.UserID != c.Meta.UserID {
				return invalidOutput()
			}
		}
		out.Evidence = append(out.Evidence, Evidence{Type: "billing_report", ID: r.ID, AuditID: r.AuditID})
		for _, id := range r.EvidenceTransactionIDs {
			out.Evidence = append(out.Evidence, Evidence{Type: "transaction", ID: id, AuditID: r.AuditID})
		}
		for _, a := range r.Anomalies {
			if a.Type == "possible_duplicate" && len(a.Evidence) >= 2 {
				out.NextActions = append(out.NextActions, NextAction{Type: "verify_subscriptions"})
				break
			}
		}
	case "subscription":
		if c.Tool == "get_subscription_details" || c.Tool == "get_subscription_status" {
			if e := rawSchema("subscription-detail", out.Result); e != nil {
				return e
			}
			var d subscription.Detail
			if strictJSON(out.Result, &d) != nil || validateDetail(c.Meta.UserID, d) != nil {
				return invalidOutput()
			}
			var in subscription.IDInput
			_ = json.Unmarshal(c.Parameters, &in)
			if strconv.FormatInt(d.Subscription.ID, 10) != in.SubscriptionID {
				return invalidOutput()
			}
			out.Evidence = append(out.Evidence, Evidence{Type: "subscription", ID: in.SubscriptionID})
			for _, p := range d.Payments {
				out.Evidence = append(out.Evidence, Evidence{Type: "transaction", ID: strconv.FormatInt(p.TransactionID, 10)})
			}
		} else {
			if e := rawSchema("subscription-report", out.Result); e != nil {
				return e
			}
			var r subscription.Report
			if strictJSON(out.Result, &r) != nil || r.UserID != c.Meta.UserID || r.Tool != c.Tool || r.FinancialExecutionEnabled || subscription.ValidateReport(r) != nil {
				return invalidOutput()
			}
			out.Evidence = append(out.Evidence, Evidence{Type: "subscription_analysis", ID: strconv.FormatInt(r.ID, 10), AuditID: r.AuditID})
			for _, d := range r.Subscriptions {
				if validateDetail(c.Meta.UserID, d) != nil {
					return invalidOutput()
				}
				out.Evidence = append(out.Evidence, Evidence{Type: "subscription", ID: strconv.FormatInt(d.Subscription.ID, 10), AuditID: r.AuditID})
			}
			for _, id := range r.EvidenceTransactionIDs {
				out.Evidence = append(out.Evidence, Evidence{Type: "transaction", ID: id, AuditID: r.AuditID})
			}
		}
	case "savings":
		var v planning.View
		if strictJSON(out.Result, &v) != nil || v.Plan.UserID != c.Meta.UserID || v.Version.UserID != c.Meta.UserID || v.Plan.ID <= 0 || v.Version.ID <= 0 || v.Version.PlanID != v.Plan.ID || v.Plan.CurrentVersion <= 0 || v.Plan.CurrentVersion != v.Version.Version || v.FinancialExecutionEnabled || v.Result.FinancialExecutionEnabled || v.Result.Currency != "CNY" {
			return invalidOutput()
		}
		var in planning.IDInput
		_ = json.Unmarshal(c.Parameters, &in)
		if strconv.FormatInt(v.Plan.ID, 10) != in.PlanID {
			return invalidOutput()
		}
		var snap planning.Snapshot
		if strictJSON(v.Version.SnapshotJSON, &snap) != nil {
			return invalidOutput()
		}
		calculated, e := planning.Calculate(snap)
		if e != nil || !reflect.DeepEqual(calculated, v.Result) {
			return invalidOutput()
		}
		for _, constraint := range v.Constraints {
			if constraint.UserID != c.Meta.UserID {
				return invalidOutput()
			}
		}
		for _, decision := range v.Decisions {
			if decision.UserID != c.Meta.UserID {
				return invalidOutput()
			}
		}
		out.Evidence = append(out.Evidence, Evidence{Type: "financial_plan", ID: in.PlanID, AuditID: v.Version.AuditID}, Evidence{Type: "plan_version", ID: strconv.FormatInt(v.Version.ID, 10), AuditID: v.Version.AuditID})
	}
	return nil
}
func validateDetail(uid int64, d subscription.Detail) error {
	if d.Subscription.UserID != uid || d.Subscription.ID <= 0 {
		return invalidOutput()
	}
	for _, p := range d.Payments {
		if p.UserID != uid || p.SubscriptionID != d.Subscription.ID || p.TransactionID <= 0 {
			return invalidOutput()
		}
	}
	for _, i := range d.OperationHistory {
		if i.UserID != uid || i.SubscriptionID != d.Subscription.ID {
			return invalidOutput()
		}
	}
	return nil
}

func workflowOutput(r coze.RunResponse, out any) error {
	if r.Code == nil || *r.Code != 0 || r.ExecuteID == "" || len(r.Data) > 2*1024*1024 {
		return entity.Fail("MODEL_OUTPUT_INVALID", "invalid workflow execution evidence")
	}
	var end struct {
		Output json.RawMessage `json:"output"`
	}
	if strictJSON([]byte(r.Data), &end) != nil || len(end.Output) == 0 {
		return entity.Fail("MODEL_OUTPUT_INVALID", "workflow End.output is required")
	}
	raw := end.Output
	if len(raw) > 0 && raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) != nil {
			return entity.Fail("MODEL_OUTPUT_INVALID", "invalid workflow output")
		}
		raw = []byte(s)
	}
	if strictJSON(raw, out) != nil {
		return entity.Fail("MODEL_OUTPUT_INVALID", "workflow output schema rejected")
	}
	return nil
}
func (x *HTTPExecutor) explain(ctx context.Context, c Call, out *AgentOutput) error {
	if c.AgentType == "lifestyle" {
		return x.explainLifestyle(ctx, c, out)
	}
	safeResult, err := explanationData(out.Result)
	if err != nil {
		return invalidOutput()
	}
	payload, _ := json.Marshal(struct {
		TaskID    string          `json:"task_id"`
		AgentType string          `json:"agent_type"`
		Result    json.RawMessage `json:"authoritative_result"`
		Evidence  []Evidence      `json:"evidence"`
	}{c.TaskID, c.AgentType, safeResult, out.Evidence})
	r, e := x.coze.RunSyncWorkflow(ctx, coze.RunRequest{WorkflowID: x.workflows[c.AgentType], TraceID: c.Meta.TraceID, Parameters: map[string]any{"input_string": string(payload)}})
	if e != nil {
		if code := coze.BudgetCode(e); code != "" {
			return entity.Fail(code, "model budget refused or previous outcome unknown")
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return entity.Fail("COZE_UNAVAILABLE", "specialist workflow unavailable")
	}
	var result struct {
		TaskID      string   `json:"task_id"`
		AgentType   string   `json:"agent_type"`
		Explanation string   `json:"explanation"`
		EvidenceIDs []string `json:"evidence_ids"`
	}
	if e = workflowOutput(r, &result); e != nil {
		return e
	}
	if result.TaskID != c.TaskID || result.AgentType != c.AgentType {
		return entity.Fail("MODEL_OUTPUT_INVALID", "specialist output identity or explanation invalid")
	}
	if err = validateExplanation(result.Explanation, result.EvidenceIDs, out.Evidence); err != nil {
		return err
	}
	out.Explanation = result.Explanation
	out.ExplanationEvidenceIDs = result.EvidenceIDs
	out.WorkflowExecuteID = r.ExecuteID
	return nil
}

func (x *HTTPExecutor) Plan(ctx context.Context, m service.Meta, instruction string, bindings Bindings) ([]TaskSpec, error) {
	tasks, _, e := x.PlanWithEvidence(ctx, m, instruction, bindings)
	return tasks, e
}

func (x *HTTPExecutor) PlanWithEvidence(ctx context.Context, m service.Meta, instruction string, bindings Bindings) ([]TaskSpec, string, error) {
	key := "planner:" + m.TaskID
	if m.TaskID == "" {
		key = "planner:" + uuid.NewString()
	}
	ctx = coze.WithIdentity(ctx, m.UserID, key)
	if x.coze == nil || x.workflows["supervisor"] == "" {
		return nil, "", entity.Fail("COZE_UNAVAILABLE", "published Supervisor workflow is not configured")
	}
	if m.UserID <= 0 || strings.TrimSpace(instruction) == "" || len(instruction) > 8192 {
		return nil, "", entity.Fail("INVALID_ARGUMENT", "bounded instruction required")
	}
	payload, _ := json.Marshal(struct {
		Instruction string   `json:"instruction"`
		Bindings    Bindings `json:"bindings"`
	}{instruction, bindings})
	r, e := x.coze.RunSyncWorkflow(ctx, coze.RunRequest{WorkflowID: x.workflows["supervisor"], TraceID: m.TraceID, Parameters: map[string]any{"input_string": string(payload)}})
	if e != nil {
		if code := coze.BudgetCode(e); code != "" {
			return nil, "", entity.Fail(code, "model budget refused or previous outcome unknown")
		}
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", entity.Fail("COZE_UNAVAILABLE", "Supervisor workflow unavailable")
	}
	var result struct {
		Tasks []TaskSpec `json:"tasks"`
	}
	if e = workflowOutput(r, &result); e != nil {
		return nil, "", e
	}
	tasks, e := ValidateTasks(result.Tasks)
	if e == nil {
		e = validateLifestyleBindings(tasks, bindings)
	}
	return tasks, r.ExecuteID, e
}
