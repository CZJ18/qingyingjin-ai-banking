package billing

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/coze"
	"github.com/qingyingjin/banking-service/internal/entity"
	"io"
	"strings"
	"time"
)

type Runner interface {
	RunSyncWorkflow(context.Context, coze.RunRequest) (coze.RunResponse, error)
}
type QueryParser struct {
	Runner     Runner
	WorkflowID string
}
type QueryInput struct {
	Question  string `json:"question"`
	Timezone  string `json:"timezone,omitempty"`
	AccountID *int64 `json:"account_id,omitempty,string"`
}
type Route struct {
	Tool               string   `json:"tool"`
	From               string   `json:"from"`
	To                 string   `json:"to"`
	Timezone           string   `json:"timezone"`
	MissingInformation []string `json:"missing_information"`
	Evidence           string   `json:"question_evidence"`
}
type ParsedQuery struct {
	Route     Route
	ExecuteID string
}

func (p *QueryParser) Parse(ctx context.Context, in QueryInput, trace string) (ParsedQuery, error) {
	var out ParsedQuery
	if strings.TrimSpace(in.Question) == "" || len(in.Question) > 4000 {
		return out, entity.Fail("INVALID_ARGUMENT", "question required, maximum 4000 bytes")
	}
	if in.Timezone == "" {
		in.Timezone = "Asia/Shanghai"
	}
	if in.Timezone != "UTC" && in.Timezone != "Asia/Shanghai" || in.AccountID != nil && *in.AccountID <= 0 {
		return out, entity.Fail("INVALID_ARGUMENT", "invalid timezone or account")
	}
	if p == nil || p.Runner == nil || p.WorkflowID == "" {
		return out, entity.Fail("COZE_UNAVAILABLE", "Billing query workflow is not configured")
	}
	payload, _ := json.Marshal(map[string]any{"question": in.Question, "timezone": in.Timezone, "now": time.Now().UTC().Format(time.RFC3339)})
	run, e := p.Runner.RunSyncWorkflow(ctx, coze.RunRequest{WorkflowID: p.WorkflowID, TraceID: trace, Parameters: map[string]any{"input_string": string(payload)}})
	if e != nil {
		if code := coze.BudgetCode(e); code != "" {
			return out, entity.Fail(code, "model budget refused or previous outcome unknown")
		}
		return out, entity.Fail("COZE_UNAVAILABLE", "Billing workflow unavailable")
	}
	out.ExecuteID = run.ExecuteID
	var end struct {
		Output string `json:"output"`
	}
	d := json.NewDecoder(bytes.NewReader([]byte(run.Data)))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(&end) != nil || d.Decode(&extra) != io.EOF || len(end.Output) > 16384 {
		return out, entity.Fail("MODEL_OUTPUT_INVALID", "invalid Billing workflow End output")
	}
	if e = validate("billing-query", []byte(end.Output)); e != nil {
		return out, e
	}
	if json.Unmarshal([]byte(end.Output), &out.Route) != nil {
		return out, entity.Fail("MODEL_OUTPUT_INVALID", "invalid Billing query route")
	}
	if out.Route.Timezone != in.Timezone || out.Route.Evidence == "" || !strings.Contains(in.Question, out.Route.Evidence) {
		return out, entity.Fail("MODEL_OUTPUT_INVALID", "query route lacks original question evidence or changes timezone")
	}
	if len(out.Route.MissingInformation) == 0 {
		if _, e = Normalize(Input{From: out.Route.From, To: out.Route.To, Timezone: out.Route.Timezone}); e != nil {
			return out, entity.Fail("MODEL_OUTPUT_INVALID", "model returned invalid analysis period")
		}
	}
	return out, nil
}
