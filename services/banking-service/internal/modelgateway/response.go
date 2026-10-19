package modelgateway

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"mime"
	"strings"

	"github.com/qingyingjin/banking-service/internal/modelspend"
)

var invalidResponse = errors.New("unverified supplier response")

func object(raw []byte) (map[string]json.RawMessage, error) {
	var m map[string]json.RawMessage
	if modelspend.StrictJSON(raw, &m, responseLimit) != nil || m == nil {
		return nil, invalidResponse
	}
	return m, nil
}
func textField(m map[string]json.RawMessage, k string) (string, error) {
	var s string
	if json.Unmarshal(m[k], &s) != nil {
		return "", invalidResponse
	}
	return s, nil
}
func parseUsage(raw []byte) (modelspend.Usage, error) {
	var u modelspend.Usage
	m, e := object(raw)
	if e != nil {
		return u, e
	}
	read := func(k string) (int64, error) { return modelspend.CanonicalToken(string(bytes.TrimSpace(m[k]))) }
	if u.PromptTokens, e = read("prompt_tokens"); e != nil {
		return u, invalidResponse
	}
	if u.CompletionTokens, e = read("completion_tokens"); e != nil {
		return u, invalidResponse
	}
	if u.TotalTokens, e = read("total_tokens"); e != nil {
		return u, invalidResponse
	}
	if u.PromptTokens > math.MaxInt64-u.CompletionTokens || u.TotalTokens != u.PromptTokens+u.CompletionTokens {
		return u, invalidResponse
	}
	hit, hasHit := m["prompt_cache_hit_tokens"]
	miss, hasMiss := m["prompt_cache_miss_tokens"]
	if hasHit != hasMiss {
		return u, invalidResponse
	}
	if hasHit {
		h, e := modelspend.CanonicalToken(string(hit))
		if e != nil {
			return u, invalidResponse
		}
		v, e := modelspend.CanonicalToken(string(miss))
		if e != nil || h > u.PromptTokens || v != u.PromptTokens-h {
			return u, invalidResponse
		}
		u.CacheHitTokens = &h
		u.CacheMissTokens = &v
	}
	if details, ok := m["completion_tokens_details"]; ok && string(details) != "null" {
		d, e := object(details)
		if e != nil {
			return u, e
		}
		if raw, ok := d["reasoning_tokens"]; ok {
			n, e := modelspend.CanonicalToken(string(raw))
			if e != nil || n > u.CompletionTokens {
				return u, invalidResponse
			}
		}
	}
	return u, nil
}
func parseResponse(body []byte, contentType string, stream bool, model string) (modelspend.Report, error) {
	var report modelspend.Report
	media, _, e := mime.ParseMediaType(contentType)
	if e != nil {
		return report, invalidResponse
	}
	if stream {
		if media != "text/event-stream" {
			return report, invalidResponse
		}
		return parseStream(body, model)
	}
	if media != "application/json" {
		return report, invalidResponse
	}
	m, e := object(body)
	if e != nil {
		return report, e
	}
	id, e := textField(m, "id")
	if e != nil || !requestID.MatchString(id) {
		return report, invalidResponse
	}
	actual, e := textField(m, "model")
	if e != nil || actual == "" || len(actual) > 128 {
		return report, invalidResponse
	}
	if actual != model {
		// The model mismatch already invalidates the monetary bound. Preserve
		// that evidence even when choices or usage cannot be trusted; the
		// explicit flag prevents missing counters from becoming reported zero.
		usage, usageErr := parseUsage(m["usage"])
		return modelspend.Report{ProviderID: id, Model: actual, Usage: usage, UsageUnknown: usageErr != nil, ResponseBody: body, ContentType: "application/json"}, nil
	}
	var choices []json.RawMessage
	if json.Unmarshal(m["choices"], &choices) != nil || len(choices) != 1 {
		return report, invalidResponse
	}
	if e = validateChoice(choices[0], false); e != nil {
		return report, e
	}
	usage, e := parseUsage(m["usage"])
	if e != nil {
		return report, e
	}
	return modelspend.Report{ProviderID: id, Model: actual, Usage: usage, ResponseBody: body, ContentType: "application/json"}, nil
}

func validateChoice(raw []byte, stream bool) error {
	c, e := object(raw)
	if e != nil {
		return e
	}
	if n, e := modelspend.CanonicalToken(string(c["index"])); e != nil || n != 0 {
		return invalidResponse
	}
	reason := string(c["finish_reason"])
	if reason != "" && reason != "null" {
		s, e := textField(c, "finish_reason")
		if e != nil || (s != "stop" && s != "length" && s != "tool_calls" && s != "content_filter" && s != "insufficient_system_resource") {
			return invalidResponse
		}
	} else if !stream {
		return invalidResponse
	}
	key := "message"
	if stream {
		key = "delta"
	}
	m, e := object(c[key])
	if e != nil {
		return e
	}
	if !stream {
		role, e := textField(m, "role")
		if e != nil || role != "assistant" {
			return invalidResponse
		}
	}
	calls, hasCalls := m["tool_calls"]
	if hasCalls && string(calls) != "null" {
		var list []json.RawMessage
		if json.Unmarshal(calls, &list) != nil || len(list) > 128 {
			return invalidResponse
		}
		for _, raw := range list {
			tc, e := object(raw)
			if e != nil {
				return e
			}
			if !stream {
				id, e := textField(tc, "id")
				if e != nil || !requestID.MatchString(id) {
					return invalidResponse
				}
				typ, e := textField(tc, "type")
				if e != nil || typ != "function" {
					return invalidResponse
				}
			}
			if f, ok := tc["function"]; ok {
				fn, e := object(f)
				if e != nil {
					return e
				}
				if !stream {
					name, e := textField(fn, "name")
					if e != nil || !fnName.MatchString(name) {
						return invalidResponse
					}
					if _, e = textField(fn, "arguments"); e != nil {
						return e
					}
				}
			}
		}
	}
	if content, ok := m["content"]; ok && string(content) != "null" {
		var text string
		if json.Unmarshal(content, &text) != nil {
			return invalidResponse
		}
	} else if !stream && !hasCalls {
		return invalidResponse
	}
	return nil
}

// Streams are deliberately buffered, not relayed in real time. The supplier's
// terminal usage and DONE must be verified and durably observed before replay.
func parseStream(body []byte, model string) (modelspend.Report, error) {
	var report modelspend.Report
	normalized := strings.ReplaceAll(string(body), "\r\n", "\n")
	terminated := strings.HasSuffix(normalized, "\n\n")
	events := strings.Split(normalized, "\n\n")
	done, hasUsage, finished := false, false, false
	var id string
	var actualModel string
	var usage modelspend.Usage
	count := 0
	for eventIndex, event := range events {
		if eventIndex == len(events)-1 && !terminated {
			break
		}
		var parts []string
		for _, line := range strings.Split(event, "\n") {
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}
			if !strings.HasPrefix(line, "data:") {
				return report, invalidResponse
			}
			parts = append(parts, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
		if len(parts) == 0 {
			continue
		}
		if done {
			return report, invalidResponse
		}
		data := strings.Join(parts, "\n")
		if data == "[DONE]" {
			done = true
			continue
		}
		m, e := object([]byte(data))
		if e != nil {
			return report, e
		}
		got, e := textField(m, "id")
		if e != nil || !requestID.MatchString(got) || (id != "" && id != got) {
			return report, invalidResponse
		}
		id = got
		actual, e := textField(m, "model")
		if e != nil || actual == "" || len(actual) > 128 {
			return report, invalidResponse
		}
		if actual != model {
			// A completed chunk proves the wrong model even if later chunks,
			// usage or DONE never arrive. This report can only freeze, not cache.
			return modelspend.Report{ProviderID: id, Model: actual, UsageUnknown: true, ResponseBody: body, ContentType: "text/event-stream"}, nil
		}
		// Preserve any observed identity mismatch so the ledger freezes the pool.
		if actualModel == "" || actual != model {
			actualModel = actual
		}
		var choices []map[string]json.RawMessage
		if json.Unmarshal(m["choices"], &choices) != nil || len(choices) > 1 {
			return report, invalidResponse
		}
		for _, c := range choices {
			raw, _ := json.Marshal(c)
			if validateChoice(raw, true) != nil {
				return report, invalidResponse
			}
			if f, ok := c["finish_reason"]; ok && string(f) != "null" {
				var reason string
				if json.Unmarshal(f, &reason) != nil || reason == "" {
					return report, invalidResponse
				}
				finished = true
			}
		}
		if raw, ok := m["usage"]; ok && string(raw) != "null" {
			if hasUsage {
				return report, invalidResponse
			}
			u, e := parseUsage(raw)
			if e != nil {
				return report, e
			}
			usage = u
			hasUsage = true
		} else if hasUsage {
			return report, invalidResponse
		}
		count++
	}
	if !terminated || !done || !hasUsage || !finished || id == "" || count < 1 {
		return report, invalidResponse
	}
	return modelspend.Report{ProviderID: id, Model: actualModel, Usage: usage, ResponseBody: body, ContentType: "text/event-stream"}, nil
}
