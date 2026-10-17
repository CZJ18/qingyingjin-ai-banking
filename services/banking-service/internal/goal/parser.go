package goal

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/coze"
	"github.com/qingyingjin/banking-service/internal/entity"
	"io"
)

type Runner interface {
	RunSyncWorkflow(context.Context, coze.RunRequest) (coze.RunResponse, error)
}
type Parser struct {
	Runner     Runner
	WorkflowID string
}

func (p *Parser) Configured() bool { return p != nil && p.Runner != nil && p.WorkflowID != "" }
func (p *Parser) Parse(ctx context.Context, text, trace string) (Parsed, string, error) {
	if !p.Configured() {
		return Parsed{}, "", entity.Fail("COZE_UNAVAILABLE", "configure a published Goal parser workflow before parsing")
	}
	in, _ := json.Marshal(map[string]string{"text": text})
	result, err := p.Runner.RunSyncWorkflow(ctx, coze.RunRequest{WorkflowID: p.WorkflowID, Parameters: map[string]any{"input_string": string(in)}, TraceID: trace})
	if err != nil {
		if code := coze.BudgetCode(err); code != "" {
			return Parsed{}, "", entity.Fail(code, "model budget refused or previous outcome unknown")
		}
		return Parsed{}, "", entity.Fail("COZE_UNAVAILABLE", "Coze workflow unavailable; no financial tasks executed")
	}
	var end struct {
		Output string `json:"output"`
	}
	d := json.NewDecoder(bytes.NewReader([]byte(result.Data)))
	d.DisallowUnknownFields()
	var extra any
	if d.Decode(&end) != nil || d.Decode(&extra) != io.EOF || end.Output == "" {
		return Parsed{}, result.ExecuteID, entity.Fail("MODEL_OUTPUT_INVALID", "workflow End must return exactly the string variable output")
	}
	parsed, err := DecodeParsed([]byte(end.Output), text)
	return parsed, result.ExecuteID, err
}
