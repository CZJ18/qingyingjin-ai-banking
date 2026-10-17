package lifestyle

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/coze"
	"github.com/qingyingjin/banking-service/internal/entity"
	"github.com/qingyingjin/banking-service/internal/goal"
	"io"
	"strings"
	"unicode/utf8"
)

type Parser struct {
	Runner     goal.Runner
	WorkflowID string
}
type ParseInput struct {
	Text string `json:"text"`
}
type ParseResult struct {
	Status             string   `json:"status"`
	Spec               *Spec    `json:"spec,omitempty"`
	MissingInformation []string `json:"missing_information"`
	ExecuteID          string   `json:"execute_id"`
	WorkflowID         string   `json:"workflow_id"`
}

func (s *Service) Parse(ctx context.Context, in ParseInput, trace string) (ParseResult, error) {
	var out ParseResult
	if strings.TrimSpace(in.Text) == "" || !utf8.ValidString(in.Text) || len(in.Text) > 10000 {
		return out, entity.Fail("INVALID_ARGUMENT", "text required, maximum 10000 bytes")
	}
	if s.Parser == nil || s.Parser.Runner == nil || s.Parser.WorkflowID == "" {
		return out, entity.Fail("COZE_UNAVAILABLE", "publish and configure COZE_LIFESTYLE_GOAL_WORKFLOW_ID for v2 parsing")
	}
	raw, _ := json.Marshal(in)
	res, e := s.Parser.Runner.RunSyncWorkflow(ctx, coze.RunRequest{WorkflowID: s.Parser.WorkflowID, Parameters: map[string]any{"input_string": string(raw)}, TraceID: trace})
	if e != nil {
		if code := coze.BudgetCode(e); code != "" {
			return out, entity.Fail(code, "model budget refused or previous outcome unknown")
		}
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
		return out, entity.Fail("COZE_UNAVAILABLE", "v2 workflow unavailable")
	}
	if res.Code == nil || *res.Code != 0 || res.ExecuteID == "" || len(res.Data) > 1024*1024 {
		return out, entity.Fail("MODEL_OUTPUT_INVALID", "invalid v2 execution evidence")
	}
	out.ExecuteID = res.ExecuteID
	out.WorkflowID = s.Parser.WorkflowID
	var end struct {
		Output string `json:"output"`
	}
	d := json.NewDecoder(bytes.NewReader([]byte(res.Data)))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(&end) != nil || d.Decode(&extra) != io.EOF || end.Output == "" {
		return out, entity.Fail("MODEL_OUTPUT_INVALID", "workflow End must return only output string")
	}
	var discriminator struct {
		Status string `json:"status"`
	}
	_ = json.Unmarshal([]byte(end.Output), &discriminator)
	if discriminator.Status == "needs_information" {
		var clarification struct {
			Status  string   `json:"status"`
			Missing []string `json:"missing_information"`
		}
		d = json.NewDecoder(strings.NewReader(end.Output))
		d.DisallowUnknownFields()
		if d.Decode(&clarification) != nil || d.Decode(&extra) != io.EOF || len(clarification.Missing) == 0 || len(clarification.Missing) > 2 {
			return out, entity.Fail("MODEL_OUTPUT_INVALID", "invalid v2 clarification")
		}
		seen := map[string]bool{}
		for _, field := range clarification.Missing {
			if (field != "period_months" && field != "goal_details") || seen[field] {
				return out, entity.Fail("MODEL_OUTPUT_INVALID", "unsupported missing goal field")
			}
			seen[field] = true
		}
		out.Status, out.MissingInformation = clarification.Status, clarification.Missing
		return out, nil
	}
	parsed, e := decodeModelSpec([]byte(end.Output), in.Text)
	if e == nil {
		out.Spec, out.Status, out.MissingInformation = &parsed, "parsed", []string{}
	}
	return out, e
}
