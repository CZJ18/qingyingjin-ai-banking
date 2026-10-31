package modelgateway

import (
	"context"
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

type IdentityVerifier func(context.Context, string) (int64, error)

// The gateway never possesses the bank JWT signing key. Revalidation uses the
// fixed Banking endpoint on every request, including cache/budget reads.
func NewIdentityVerifier(base string, policy outbound.Policy) (IdentityVerifier, error) {
	u, e := url.Parse(base)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.RawPath != "" || (u.Path != "" && u.Path != "/") || policy.Validate(base) != nil {
		return nil, errors.New("invalid banking identity origin")
	}
	client := &http.Client{Timeout: 3 * time.Second, Transport: &http.Transport{Proxy: nil, ResponseHeaderTimeout: 2 * time.Second}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	return func(ctx context.Context, credential string) (int64, error) {
		denied := errors.New("user identity unavailable")
		if !strings.HasPrefix(credential, "Bearer ") || len(credential) <= 7 || len(credential) > 8192 || strings.ContainsAny(credential, "\r\n") {
			return 0, denied
		}
		req, e := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/api/v1/context", nil)
		if e != nil {
			return 0, denied
		}
		req.Header.Set("Authorization", credential)
		res, e := client.Do(req)
		if e != nil {
			return 0, denied
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			return 0, denied
		}
		raw, e := io.ReadAll(io.LimitReader(res.Body, (64<<10)+1))
		if e != nil || len(raw) > 64<<10 {
			return 0, denied
		}
		var envelope struct {
			Code string `json:"code"`
			Data struct {
				Actor struct {
					Kind   string `json:"kind"`
					UserID string `json:"user_id"`
				} `json:"actor"`
			} `json:"data"`
		}
		if json.Unmarshal(raw, &envelope) != nil || envelope.Code != "OK" || (envelope.Data.Actor.Kind != "user" && envelope.Data.Actor.Kind != "agent") {
			return 0, denied
		}
		user, e := strconv.ParseInt(envelope.Data.Actor.UserID, 10, 64)
		if e != nil || user <= 0 || strconv.FormatInt(user, 10) != envelope.Data.Actor.UserID {
			return 0, denied
		}
		// Context proves token identity; the persisted user endpoint also proves
		// current ownership and active status, including legacy operator tokens.
		me, e := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+"/api/v1/users/me", nil)
		if e != nil {
			return 0, denied
		}
		me.Header.Set("Authorization", credential)
		profile, e := client.Do(me)
		if e != nil {
			return 0, denied
		}
		defer profile.Body.Close()
		if profile.StatusCode != http.StatusOK {
			return 0, denied
		}
		body, e := io.ReadAll(io.LimitReader(profile.Body, (64<<10)+1))
		if e != nil || len(body) > 64<<10 {
			return 0, denied
		}
		var active struct {
			Code string `json:"code"`
			Data struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &active) != nil || active.Code != "OK" || active.Data.ID != envelope.Data.Actor.UserID || active.Data.Status != "active" {
			return 0, denied
		}
		return user, nil
	}, nil
}
