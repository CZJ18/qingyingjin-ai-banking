// Package taskproof verifies authoritative LangGraph SQL execution state over a
// private fixed origin. No frontend or workflow-provided owner is trusted here.
package taskproof

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/qingyingjin/banking-service/internal/outbound"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type Proof struct {
	OwnerID          string `json:"owner_id"`
	TaskID           string `json:"task_id"`
	RequestDigest    string `json:"request_digest"`
	Version          int64  `json:"version"`
	LeaseFingerprint string `json:"lease_fingerprint"`
}
type Verify func(context.Context, int64, string) (Proof, error)

func New(base, key string, p outbound.Policy) (Verify, error) {
	u, e := url.Parse(base)
	if e != nil || p.Validate(base) != nil || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || len(key) < 32 || len(key) > 512 || strings.ContainsAny(key, " \t\r\n") {
		return nil, errors.New("invalid task proof origin or authority")
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil, ResponseHeaderTimeout: 2 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(ctx context.Context, owner int64, task string) (Proof, error) {
		denied := errors.New("active persisted task proof unavailable")
		var result Proof
		raw, _ := json.Marshal(map[string]string{"owner_id": strconv.FormatInt(owner, 10), "task_id": task})
		req, e := http.NewRequestWithContext(ctx, "POST", strings.TrimRight(base, "/")+"/internal/model-task-context", bytes.NewReader(raw))
		if e != nil {
			return result, denied
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Banking-Agent-Key", key)
		res, e := client.Do(req)
		if e != nil {
			return result, denied
		}
		defer res.Body.Close()
		data, e := io.ReadAll(io.LimitReader(res.Body, 65537))
		if e != nil || res.StatusCode != 200 || len(data) > 65536 {
			return result, denied
		}
		var envelope struct {
			Code string `json:"code"`
			Data Proof  `json:"data"`
		}
		if json.Unmarshal(data, &envelope) != nil || envelope.Code != "OK" {
			return result, denied
		}
		result = envelope.Data
		digest, e1 := hex.DecodeString(result.RequestDigest)
		lease, e2 := hex.DecodeString(result.LeaseFingerprint)
		if owner <= 0 || result.OwnerID != strconv.FormatInt(owner, 10) || result.TaskID != task || result.Version <= 0 || e1 != nil || len(digest) != 32 || e2 != nil || len(lease) != 32 {
			return Proof{}, denied
		}
		return result, nil
	}, nil
}
