// Package bankingscope carries an opaque gateway grant only in request memory.
// It never infers Banking identity from workflow parameters or Coze users.
package bankingscope

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
)

const CapabilityHeader = "X-Banking-Model-Capability"
const DigestHeader = "X-Banking-Workflow-SHA256"

var ErrDenied = errors.New("banking workflow scope denied")

type key struct{}
type scope struct {
	config      GatewayConfig
	digest      string
	workflow    string
	verified    atomic.Bool
	called      atomic.Bool
	outputLimit atomic.Int64
}
type GatewayConfig struct {
	Capability, Origin, Model string
	MaxOutputTokens           int
}

func Present(ctx context.Context) bool { _, ok := ctx.Value(key{}).(*scope); return ok }

// Attach accepts only a pinned, synchronous workflow. Authentication is performed
// by the gateway; the opaque bytes are deliberately not decoded here.
func Attach(ctx context.Context, capability, digest, workflow string, async bool) (context.Context, error) {
	if async || !strings.HasPrefix(capability, "bgc1.") || len(capability) > 32768 || strings.ContainsAny(capability, "\r\n \t") {
		return nil, ErrDenied
	}
	view, err := ConfigView()
	if err != nil || view.WorkflowPins[workflow] != digest {
		return nil, ErrDenied
	}
	return context.WithValue(ctx, key{}, &scope{config: GatewayConfig{capability, view.GatewayOrigin, view.Model, view.MaxOutputTokens}, digest: digest, workflow: workflow}), nil
}

func Gateway(ctx context.Context) (GatewayConfig, error) {
	s, ok := ctx.Value(key{}).(*scope)
	if !ok || !s.verified.Load() {
		return GatewayConfig{}, ErrDenied
	}
	cfg := s.config
	cfg.MaxOutputTokens = int(s.outputLimit.Load())
	return cfg, nil
}

// ClaimCall prevents automatic retry, fallback, or a second model invocation.
func ClaimCall(ctx context.Context) error {
	s, ok := ctx.Value(key{}).(*scope)
	if !ok || !s.verified.Load() || !s.called.CompareAndSwap(false, true) {
		return ErrDenied
	}
	return nil
}

// VerifyPublished hashes the exact stored Canvas UTF-8 bytes, then permits only
// a linear entry -> single LLM -> exit graph. Unknown execution inputs fail closed.
func VerifyPublished(ctx context.Context, canvas string) error {
	s, ok := ctx.Value(key{}).(*scope)
	if !ok {
		return ErrDenied
	}
	sum := sha256.Sum256([]byte(canvas))
	if hex.EncodeToString(sum[:]) != s.digest {
		return ErrDenied
	}
	// Files and multimodal conversion can cause independent network requests.
	// This bounded path permits scalar/text inputs only.
	var raw any
	if len(canvas) > 1048576 || json.Unmarshal([]byte(canvas), &raw) != nil || hasFileAssist(raw) {
		return ErrDenied
	}
	var graph struct {
		Nodes []struct {
			ID, Type      string
			Blocks, Edges []json.RawMessage
			Data          struct{ Inputs map[string]json.RawMessage }
		}
		Edges []struct{ SourceNodeID, TargetNodeID string }
	}
	if json.Unmarshal([]byte(canvas), &graph) != nil || len(graph.Nodes) != 3 || len(graph.Edges) != 2 {
		return ErrDenied
	}
	ids := map[string]string{}
	seen := map[string]bool{}
	canvasOutputLimit := 0
	for _, n := range graph.Nodes {
		if n.ID == "" || seen[n.ID] || ids[n.Type] != "" || len(n.Blocks) > 0 || len(n.Edges) > 0 {
			return ErrDenied
		}
		seen[n.ID] = true
		if n.Type != "1" && n.Type != "2" && n.Type != "3" {
			return ErrDenied
		}
		ids[n.Type] = n.ID
		for k := range n.Data.Inputs {
			switch k {
			case "inputParameters", "llmParam", "terminatePlan", "content":
			default:
				return ErrDenied
			}
		}
		if raw, ok := n.Data.Inputs["llmParam"]; ok {
			var params []struct {
				Name  string `json:"name"`
				Input struct {
					Type  string `json:"type"`
					Value struct {
						Type    string          `json:"type"`
						Content json.RawMessage `json:"content"`
					} `json:"value"`
				} `json:"input"`
			}
			if json.Unmarshal(raw, &params) != nil {
				return ErrDenied
			}
			for _, p := range params {
				if p.Name == "maxTokens" {
					// The exact reviewed Canvas may narrow the shared scope's
					// ceiling. Never replace Billing's 512 with Supervisor's 1024.
					if n.Type != "3" || canvasOutputLimit != 0 || p.Input.Type != "integer" || p.Input.Value.Type != "literal" {
						return ErrDenied
					}
					var text string
					limit := 0
					if json.Unmarshal(p.Input.Value.Content, &text) == nil {
						var err error
						limit, err = strconv.Atoi(text)
						if err != nil || strconv.Itoa(limit) != text {
							return ErrDenied
						}
					} else if json.Unmarshal(p.Input.Value.Content, &limit) != nil {
						return ErrDenied
					}
					if limit < 1 || limit > s.config.MaxOutputTokens {
						return ErrDenied
					}
					canvasOutputLimit = limit
				}
				switch p.Name {
				case "temperature", "maxTokens", "responseFormat", "modelName", "modelType", "prompt", "systemPrompt", "generationDiversity", "frequencyPenalty", "presencePenalty", "topP":
				default:
					return ErrDenied
				}
			}
		}
	}
	if ids["1"] == "" || ids["2"] == "" || ids["3"] == "" || canvasOutputLimit == 0 {
		return ErrDenied
	}
	edges := map[string]bool{}
	for _, e := range graph.Edges {
		k := e.SourceNodeID + ">" + e.TargetNodeID
		if edges[k] {
			return ErrDenied
		}
		edges[k] = true
	}
	if !edges[ids["1"]+">"+ids["3"]] || !edges[ids["3"]+">"+ids["2"]] {
		return ErrDenied
	}
	// Keep the attached scope immutable even if a caller rechecks the same
	// pinned Canvas while a model client reads the verified limit.
	s.outputLimit.Store(int64(canvasOutputLimit))
	s.verified.Store(true)
	return nil
}

func hasFileAssist(value any) bool {
	switch v := value.(type) {
	case map[string]any:
		for k, child := range v {
			if strings.EqualFold(k, "assistType") && child != float64(0) {
				return true
			}
			if hasFileAssist(child) {
				return true
			}
		}
	case []any:
		for _, child := range v {
			if hasFileAssist(child) {
				return true
			}
		}
	}
	return false
}
