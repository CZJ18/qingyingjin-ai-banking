package modelgateway

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/qingyingjin/banking-service/internal/outbound"
	"github.com/qingyingjin/banking-service/internal/taskproof"
	"io"
	"net/http"
	"strings"
	"time"
)

// NewTaskIdentityVerifier accepts only a Banking-signed specialist. The task is
// read from the authoritative context endpoint, never from the grant request.
func NewTaskIdentityVerifier(base string, p outbound.Policy, proofs ...taskproof.Verify) (TaskIdentityVerifier, error) {
	if len(proofs) != 1 || proofs[0] == nil {
		return nil, errors.New("authoritative task state verifier required")
	}
	verify, e := NewIdentityVerifier(base, p)
	if e != nil {
		return nil, e
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil, ResponseHeaderTimeout: 2 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(ctx context.Context, credential string) (TaskIdentity, error) {
		denied := errors.New("signed task identity required")
		user, e := verify(ctx, credential)
		if e != nil {
			return TaskIdentity{}, denied
		}
		req, e := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(base, "/")+"/api/v1/context", nil)
		if e != nil {
			return TaskIdentity{}, denied
		}
		req.Header.Set("Authorization", credential)
		res, e := client.Do(req)
		if e != nil {
			return TaskIdentity{}, denied
		}
		defer res.Body.Close()
		raw, e := io.ReadAll(io.LimitReader(res.Body, (64<<10)+1))
		if e != nil || res.StatusCode != 200 || len(raw) > 64<<10 {
			return TaskIdentity{}, denied
		}
		var v struct {
			Code string `json:"code"`
			Data struct {
				Actor struct {
					Kind              string `json:"kind"`
					TaskID            string `json:"task_id"`
					AgentType         string `json:"agent_type"`
					TaskVersion       int64  `json:"task_version"`
					TaskRequestDigest string `json:"task_request_digest"`
					LeaseFingerprint  string `json:"lease_fingerprint"`
				} `json:"actor"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &v) != nil || v.Code != "OK" || v.Data.Actor.Kind != "agent" || !requestID.MatchString(v.Data.Actor.TaskID) || v.Data.Actor.AgentType == "" {
			return TaskIdentity{}, denied
		}
		actor := TaskIdentity{UserID: user, TaskID: v.Data.Actor.TaskID}
		if len(proofs) > 0 {
			if len(proofs) != 1 || proofs[0] == nil {
				return TaskIdentity{}, denied
			}
			proof, e := proofs[0](ctx, user, actor.TaskID)
			if e != nil {
				return TaskIdentity{}, denied
			}
			if v.Data.Actor.TaskVersion != proof.Version || v.Data.Actor.TaskRequestDigest != proof.RequestDigest || v.Data.Actor.LeaseFingerprint != proof.LeaseFingerprint {
				return TaskIdentity{}, denied
			}
			actor.RequestDigest, actor.Version, actor.LeaseFingerprint = proof.RequestDigest, proof.Version, proof.LeaseFingerprint
		} else {
			return TaskIdentity{}, denied
		}
		return actor, nil
	}, nil
}
