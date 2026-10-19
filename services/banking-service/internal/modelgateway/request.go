package modelgateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"

	"github.com/qingyingjin/banking-service/internal/modelspend"
)

type chatRequest struct {
	Model         string    `json:"model"`
	Messages      []message `json:"messages"`
	MaxTokens     *int64    `json:"max_tokens"`
	Stream        bool      `json:"stream,omitempty"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options,omitempty"`
	Temperature      *float64 `json:"temperature,omitempty"`
	TopP             *float64 `json:"top_p,omitempty"`
	FrequencyPenalty *float64 `json:"frequency_penalty,omitempty"`
	PresencePenalty  *float64 `json:"presence_penalty,omitempty"`
	ResponseFormat   *struct {
		Type string `json:"type"`
	} `json:"response_format,omitempty"`
	Stop       []string        `json:"stop,omitempty"`
	Tools      []tool          `json:"tools,omitempty"`
	ToolChoice json.RawMessage `json:"tool_choice,omitempty"`
	N          *int64          `json:"n,omitempty"`
}
type message struct {
	Role       string     `json:"role"`
	Content    *string    `json:"content"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	ToolCalls  []toolCall `json:"tool_calls,omitempty"`
}
type toolCall struct {
	Index    *int64 `json:"index,omitempty"`
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}
type tool struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters,omitempty"`
	} `json:"function"`
}

var fnName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,63}$`)
var invalidRequest = errors.New("invalid model request")

func parseRequest(raw []byte, p modelspend.Policy) (chatRequest, error) {
	var r chatRequest
	if modelspend.StrictJSON(raw, &r, requestLimit) != nil || len(bytes.TrimSpace(raw)) == 0 || bytes.TrimSpace(raw)[0] != '{' {
		return r, invalidRequest
	}
	if r.Model != p.Model || r.MaxTokens == nil || *r.MaxTokens < 1 || *r.MaxTokens > p.MaxOutputTokens || len(r.Messages) == 0 || len(r.Messages) > 256 || r.N != nil && *r.N != 1 || r.StreamOptions != nil && !r.Stream {
		return r, invalidRequest
	}
	if !rangeOK(r.Temperature, 0, 2) || !rangeOK(r.TopP, 0, 1) || !rangeOK(r.FrequencyPenalty, -2, 2) || !rangeOK(r.PresencePenalty, -2, 2) {
		return r, invalidRequest
	}
	if r.ResponseFormat != nil && r.ResponseFormat.Type != "text" && r.ResponseFormat.Type != "json_object" {
		return r, invalidRequest
	}
	if len(r.Stop) > 16 || len(r.Tools) > 128 {
		return r, invalidRequest
	}
	for _, s := range r.Stop {
		if len(s) == 0 || len(s) > 1024 {
			return r, invalidRequest
		}
	}
	for _, m := range r.Messages {
		if m.Content == nil || len(*m.Content) > requestLimit || len(m.ToolCalls) > 128 {
			return r, invalidRequest
		}
		switch m.Role {
		case "user", "system":
			if m.ToolCallID != "" || len(m.ToolCalls) > 0 {
				return r, invalidRequest
			}
		case "assistant":
			if m.ToolCallID != "" {
				return r, invalidRequest
			}
		case "tool":
			if !requestID.MatchString(m.ToolCallID) || len(m.ToolCalls) > 0 {
				return r, invalidRequest
			}
		default:
			return r, invalidRequest
		}
		for _, c := range m.ToolCalls {
			if !requestID.MatchString(c.ID) || c.Type != "function" || !fnName.MatchString(c.Function.Name) || len(c.Function.Arguments) > 65536 || c.Index != nil && (*c.Index < 0 || *c.Index >= 128) {
				return r, invalidRequest
			}
		}
	}
	names := map[string]bool{}
	for _, t := range r.Tools {
		if t.Type != "function" || !fnName.MatchString(t.Function.Name) || names[t.Function.Name] || len(t.Function.Description) > 8192 {
			return r, invalidRequest
		}
		names[t.Function.Name] = true
		if len(t.Function.Parameters) > 0 {
			var schema map[string]json.RawMessage
			if modelspend.StrictJSON(t.Function.Parameters, &schema, 65536) != nil || schema == nil || string(schema["type"]) != `"object"` {
				return r, invalidRequest
			}
		}
	}
	if len(r.ToolChoice) > 0 {
		var choice string
		if json.Unmarshal(r.ToolChoice, &choice) == nil {
			if choice != "none" && choice != "auto" && choice != "required" {
				return r, invalidRequest
			}
		} else {
			var c struct {
				Type     string `json:"type"`
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			}
			if modelspend.StrictJSON(r.ToolChoice, &c, 1024) != nil || c.Type != "function" || !names[c.Function.Name] {
				return r, invalidRequest
			}
		}
		if len(r.Tools) == 0 && choice != "none" && choice != "auto" {
			return r, invalidRequest
		}
	}
	return r, nil
}
func rangeOK(v *float64, min, max float64) bool { return v == nil || *v >= min && *v <= max }
