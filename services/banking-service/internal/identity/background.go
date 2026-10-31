// Background credentials are opaque, task-scoped and accepted only by the
// delegation endpoint. They are never ordinary Banking bearer identities.
package identity

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"github.com/qingyingjin/banking-service/internal/config"
	"io"
	"strings"
	"time"
)

type backgroundGrant struct {
	CreatedAt     int64  `json:"created_at"`
	UserID        string `json:"user_id"`
	TaskID        string `json:"task_id"`
	RequestDigest string `json:"request_digest"`
	AuthVersion   int64  `json:"auth_version"`
	IssuedAt      int64  `json:"issued_at"`
	ExpiresAt     int64  `json:"expires_at"`
}

func (a *Auth) backgroundCipher() (cipher.AEAD, error) {
	key := sha256.Sum256(append([]byte("banking/background/v1\x00"), a.secret...))
	b, e := aes.NewCipher(key[:])
	if e != nil {
		return nil, e
	}
	return cipher.NewGCM(b)
}
func (a *Auth) IssueBackground(actor Actor, task, digest string, ttl time.Duration) (string, error) {
	if actor.Kind != "user" || !config.PositiveID(actor.UserID) || len(a.secret) < 32 || ttl <= 0 || ttl > 24*time.Hour || !strings.HasPrefix(task, "lg-") || len(task) != 39 || len(digest) != 64 {
		return "", ErrUnauthenticated
	}

	c := backgroundGrant{CreatedAt: time.Now().Unix(), UserID: actor.UserID, TaskID: task, RequestDigest: digest, AuthVersion: actor.AuthVersion, IssuedAt: actor.ParentIssuedAt, ExpiresAt: time.Now().Add(ttl).Unix()}
	raw, e := json.Marshal(c)
	if e != nil {
		return "", ErrUnauthenticated
	}
	g, e := a.backgroundCipher()
	if e != nil {
		return "", ErrUnauthenticated
	}
	nonce := make([]byte, g.NonceSize())
	if _, e = io.ReadFull(rand.Reader, nonce); e != nil {
		return "", ErrUnauthenticated
	}
	return "bgt1." + base64.RawURLEncoding.EncodeToString(g.Seal(nonce, nonce, raw, []byte(a.issuer+"\x00"+a.audience))), nil
}
func (a *Auth) BackgroundContext(ctx context.Context, token string) (Actor, error) {
	denied := ErrUnauthenticated
	if !strings.HasPrefix(token, "bgt1.") || len(token) > 4096 {
		return Actor{}, denied
	}
	raw, e := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(token, "bgt1."))
	if e != nil {
		return Actor{}, denied
	}
	g, e := a.backgroundCipher()
	if e != nil || len(raw) < g.NonceSize()+g.Overhead() {
		return Actor{}, denied
	}
	plain, e := g.Open(nil, raw[:g.NonceSize()], raw[g.NonceSize():], []byte(a.issuer+"\x00"+a.audience))
	if e != nil {
		return Actor{}, denied
	}
	var c backgroundGrant
	if json.Unmarshal(plain, &c) != nil || !config.PositiveID(c.UserID) || c.ExpiresAt <= time.Now().Unix() || c.IssuedAt > time.Now().Unix() || c.ExpiresAt-c.CreatedAt > 86400 || c.CreatedAt > time.Now().Unix() || c.CreatedAt <= 0 || c.ExpiresAt <= c.CreatedAt {
		return Actor{}, denied
	}
	a.sessionMu.RLock()
	session := a.sessionValidator
	legacy := a.legacyValidator
	owner := a.agentOwnerValidator
	a.sessionMu.RUnlock()
	check, stop := context.WithTimeout(ctx, 2*time.Second)
	defer stop()
	// Fail closed without persisted owner and revocation validators.
	if owner == nil || owner(check, c.UserID) != nil {
		return Actor{}, denied
	}
	if c.AuthVersion > 0 {
		if session == nil || session(check, c.UserID, c.AuthVersion) != nil {
			return Actor{}, denied
		}
	} else {
		if legacy == nil || legacy(check, c.UserID, c.IssuedAt) != nil {
			return Actor{}, denied
		}
	}
	return Actor{Kind: "background", Subject: c.UserID, UserID: c.UserID, TaskID: c.TaskID, AuthVersion: c.AuthVersion, ParentIssuedAt: c.IssuedAt, BackgroundDigest: c.RequestDigest, Scopes: []string{"langgraph:delegate"}}, nil
}
