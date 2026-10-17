package identity

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/qingyingjin/banking-service/internal/config"
	"sync"
	"time"
)

var ErrUnauthenticated = errors.New("invalid credentials")

type Actor struct {
	AuthVersion int64    `json:"auth_version,omitempty"`
	TokenID     string   `json:"token_id,omitempty"`
	AgentType   string   `json:"agent_type,omitempty"`
	TaskID      string   `json:"task_id,omitempty"`
	Kind        string   `json:"kind"`
	Subject     string   `json:"subject"`
	UserID      string   `json:"user_id"`
	CozeUserID  string   `json:"coze_user_id,omitempty"`
	Scopes      []string `json:"scopes"`
}
type Claims struct {
	AuthVersion *int64 `json:"auth_version,omitempty"`
	TokenUse    string `json:"token_use,omitempty"`
	AgentType   string `json:"agent_type,omitempty"`
	TaskID      string `json:"task_id,omitempty"`
	CozeUserID  string `json:"coze_user_id,omitempty"`
	jwt.RegisteredClaims
}
type Auth struct {
	sessionMu                                    sync.RWMutex
	sessionValidator                             func(context.Context, string, int64) error
	legacyValidator                              func(context.Context, string, int64) error
	agentOwnerValidator                          func(context.Context, string) error
	secret                                       []byte
	issuer, audience, toolUserID, toolCozeUserID string
	toolHash                                     [32]byte
}

func New(secret, issuer, audience, toolToken, userID, cozeUserID string) *Auth {
	return &Auth{secret: []byte(secret), issuer: issuer, audience: audience, toolHash: sha256.Sum256([]byte(toolToken)), toolUserID: userID, toolCozeUserID: cozeUserID}
}
func (a *Auth) User(raw string) (Actor, error) {
	return a.UserContext(context.Background(), raw)
}

// SetSessionVersionValidator installs the authoritative persisted session check.
// Versioned browser tokens fail closed until this validator is configured.
func (a *Auth) SetSessionVersionValidator(validate func(context.Context, string, int64) error) {
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	a.sessionValidator = validate
}

// SetLegacyValidator checks password-reset revocation for unversioned user
// tokens. The timestamp is Unix seconds; zero denotes a missing iat claim.
func (a *Auth) SetLegacyValidator(validate func(context.Context, string, int64) error) {
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	a.legacyValidator = validate
}

// Delegated tokens retain their restricted scopes, but cannot outlive their
// owner's active status. This is separate from operator-token bootstrap rules.
func (a *Auth) SetAgentOwnerValidator(validate func(context.Context, string) error) {
	a.sessionMu.Lock()
	defer a.sessionMu.Unlock()
	a.agentOwnerValidator = validate
}

func (a *Auth) UserContext(ctx context.Context, raw string) (Actor, error) {
	claims := new(Claims)
	token, err := jwt.ParseWithClaims(raw, claims, func(*jwt.Token) (any, error) { return a.secret, nil },
		jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer(a.issuer), jwt.WithAudience(a.audience), jwt.WithExpirationRequired(), jwt.WithIssuedAt())
	if err != nil || !token.Valid || !config.PositiveID(claims.Subject) || (claims.CozeUserID != "" && !config.PositiveID(claims.CozeUserID)) {
		return Actor{}, ErrUnauthenticated
	}
	actor := Actor{Kind: "user", TokenID: claims.ID, Subject: claims.Subject, UserID: claims.Subject, CozeUserID: claims.CozeUserID, Scopes: []string{"context:read", "banking:read", "simulation:write", "transfer:approve", "execution:read", "execution:plan", "execution:prepare", "execution:authorize", "execution:execute"}}
	switch claims.TokenUse {
	case "", "user":
	case "agent":
		if !validAgent(claims.AgentType) || claims.TaskID == "" || claims.ID == "" || claims.IssuedAt == nil || claims.ExpiresAt.Time.Sub(claims.IssuedAt.Time) > 2*time.Minute || !claims.ExpiresAt.Time.After(claims.IssuedAt.Time) {
			return Actor{}, ErrUnauthenticated
		}
		actor.Kind, actor.AgentType, actor.TaskID = "agent", claims.AgentType, claims.TaskID
		actor.Scopes = []string{"context:read", "banking:read", "billing:read", "subscription:read", "savings:plan:read", "execution:read", "execution:plan", "execution:prepare"}
	default:
		return Actor{}, ErrUnauthenticated
	}
	if actor.Kind == "agent" {
		a.sessionMu.RLock()
		validate := a.agentOwnerValidator
		a.sessionMu.RUnlock()
		if validate != nil {
			checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			if err := validate(checkCtx, claims.Subject); err != nil {
				return Actor{}, ErrUnauthenticated
			}
		}
	}
	if claims.AuthVersion != nil {
		if *claims.AuthVersion <= 0 || actor.Kind != "user" || claims.ID == "" || claims.IssuedAt == nil || claims.ExpiresAt.Time.Sub(claims.IssuedAt.Time) > 15*time.Minute || !claims.ExpiresAt.Time.After(claims.IssuedAt.Time) {
			return Actor{}, ErrUnauthenticated
		}
		a.sessionMu.RLock()
		validate := a.sessionValidator
		a.sessionMu.RUnlock()
		if validate == nil {
			return Actor{}, ErrUnauthenticated
		}
		checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		if err := validate(checkCtx, claims.Subject, *claims.AuthVersion); err != nil {
			return Actor{}, ErrUnauthenticated
		}
		actor.AuthVersion = *claims.AuthVersion
	} else if actor.Kind == "user" {
		a.sessionMu.RLock()
		validate := a.legacyValidator
		a.sessionMu.RUnlock()
		if validate != nil {
			issued := int64(0)
			if claims.IssuedAt != nil {
				issued = claims.IssuedAt.Unix()
			}
			checkCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			if err := validate(checkCtx, claims.Subject, issued); err != nil {
				return Actor{}, ErrUnauthenticated
			}
		}
	}
	return actor, nil
}

// IssueSession issues a short-lived browser token bound to a persisted version.
// Operator tokens deliberately retain their existing separate issuance contract.
func (a *Auth) IssueSession(userID string, version int64, ttl time.Duration) (string, error) {
	if len(a.secret) < 32 || !config.PositiveID(userID) || version <= 0 || ttl <= 0 || ttl > 15*time.Minute {
		return "", errors.New("invalid session issuance parameters")
	}
	now := time.Now()
	claims := Claims{TokenUse: "user", AuthVersion: &version, RegisteredClaims: jwt.RegisteredClaims{ID: uuid.NewString(), Issuer: a.issuer, Subject: userID, Audience: jwt.ClaimStrings{a.audience}, IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(ttl))}}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(a.secret)
}
func (a *Auth) Tool(raw string) (Actor, error) {
	hash := sha256.Sum256([]byte(raw))
	if raw == "" || subtle.ConstantTimeCompare(hash[:], a.toolHash[:]) != 1 {
		return Actor{}, ErrUnauthenticated
	}
	return Actor{Kind: "tool", Subject: "coze-integration", UserID: a.toolUserID, CozeUserID: a.toolCozeUserID, Scopes: []string{"integration:read", "billing:read", "subscription:read", "subscription:propose", "savings:plan:read", "savings:plan:propose", "execution:read", "execution:plan", "execution:prepare"}}, nil
}

// IssueUser is an operator CLI facility, never an HTTP or Agent tool.
func (a *Auth) IssueUser(userID, cozeUserID string, ttl time.Duration) (string, error) {
	if len(a.secret) < 32 || !config.PositiveID(userID) || (cozeUserID != "" && !config.PositiveID(cozeUserID)) || ttl <= 0 || ttl > 24*time.Hour {
		return "", errors.New("invalid token issuance parameters")
	}
	now := time.Now()
	claims := Claims{TokenUse: "user", CozeUserID: cozeUserID, RegisteredClaims: jwt.RegisteredClaims{ID: uuid.NewString(), Issuer: a.issuer, Subject: userID, Audience: jwt.ClaimStrings{a.audience}, IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(ttl))}}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(a.secret)
}

func validAgent(role string) bool {
	switch role {
	case "supervisor", "billing", "savings", "subscription", "lifestyle":
		return true
	}
	return false
}

// IssueAgent issues a task-bound token without authorization or execution scope.
func (a *Auth) IssueAgent(userID, agentType, taskID string, ttl time.Duration) (string, error) {
	if len(a.secret) < 32 || !config.PositiveID(userID) || !validAgent(agentType) || taskID == "" || len(taskID) > 128 || ttl <= 0 || ttl > 2*time.Minute {
		return "", errors.New("invalid agent token issuance parameters")
	}
	now := time.Now()
	claims := Claims{TokenUse: "agent", AgentType: agentType, TaskID: taskID, RegisteredClaims: jwt.RegisteredClaims{ID: uuid.NewString(), Issuer: a.issuer, Subject: userID, Audience: jwt.ClaimStrings{a.audience}, IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(ttl))}}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(a.secret)
}
