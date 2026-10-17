package entity

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
)

type OperationIntent struct {
	SkillID         string          `json:"skill_id"`
	SkillVersion    int64           `json:"skill_version"`
	ID              string          `json:"id" gorm:"primaryKey"`
	UserID          int64           `json:"user_id,string"`
	Kind            string          `json:"kind"`
	Permission      string          `json:"permission"`
	Agent           string          `json:"agent"`
	Payload         json.RawMessage `json:"parameters" gorm:"column:payload_json"`
	Snapshot        json.RawMessage `json:"snapshot" gorm:"column:snapshot_json"`
	ResourceID      int64           `json:"resource_id,string"`
	ResourceDigest  string          `json:"resource_digest"`
	SourceAccountID int64           `json:"source_account_id,string"`
	TargetAccountID int64           `json:"target_account_id,string"`
	AmountMinor     int64           `json:"amount_minor"`
	Currency        string          `json:"currency"`
	IntentDigest    string          `json:"intent_digest"`
	Nonce           string          `json:"confirmation_nonce"`
	Status          string          `json:"status"`
	Version         int64           `json:"version"`
	ExpiresAt       int64           `json:"expires_at"`
	AuthorizationID string          `json:"authorization_id"`
	RiskLevel       string          `json:"risk_level"`
	ErrorCode       string          `json:"error_code"`
	Result          json.RawMessage `json:"result" gorm:"column:result_json"`
	IdempotencyKey  string          `json:"-"`
	RequestHash     string          `json:"-"`
	TraceID         string          `json:"trace_id"`
	TaskID          string          `json:"task_id"`
	AuditID         string          `json:"audit_id"`
	CreatedAt       int64           `json:"created_at" gorm:"autoCreateTime:milli"`
	UpdatedAt       int64           `json:"updated_at" gorm:"autoUpdateTime:milli"`
}

func (OperationIntent) TableName() string { return "banking_operation_intents" }

type UserAuthorization struct {
	ID           string `json:"id" gorm:"primaryKey"`
	UserID       int64  `json:"user_id,string"`
	OperationID  string `json:"operation_id"`
	Decision     string `json:"decision"`
	IntentDigest string `json:"intent_digest"`
	TokenID      string `json:"-"`
	ExpiresAt    int64  `json:"expires_at"`
	ConsumedAt   int64  `json:"consumed_at"`
	CreatedAt    int64  `json:"created_at" gorm:"autoCreateTime:milli"`
}

func (UserAuthorization) TableName() string { return "banking_user_authorizations" }

type RiskRule struct {
	Name        string `json:"name" gorm:"primaryKey"`
	Version     int64  `json:"version"`
	Enabled     bool   `json:"enabled"`
	RiskLevel   string `json:"risk_level"`
	LimitMinor  int64  `json:"limit_minor"`
	Description string `json:"description"`
}

func (RiskRule) TableName() string { return "banking_risk_rules" }

type RiskEvent struct {
	ID          string          `json:"id" gorm:"primaryKey"`
	UserID      int64           `json:"user_id,string"`
	OperationID string          `json:"operation_id"`
	RuleName    string          `json:"rule_name"`
	RiskLevel   string          `json:"risk_level"`
	Decision    string          `json:"decision"`
	ErrorCode   string          `json:"error_code"`
	Evidence    json.RawMessage `json:"evidence" gorm:"column:evidence_json"`
	TraceID     string          `json:"trace_id"`
	CreatedAt   int64           `json:"created_at" gorm:"autoCreateTime:milli"`
}

func (RiskEvent) TableName() string { return "banking_risk_events" }

type AuditLog struct {
	ID                  string `json:"id" gorm:"primaryKey"`
	UserID              *int64 `json:"user_id,omitempty,string"`
	Agent               string `json:"agent"`
	OperationID         string `json:"operation_id"`
	OperationType       string `json:"operation_type"`
	Permission          string `json:"permission"`
	AuthorizationID     string `json:"authorization_id"`
	AuthorizationDigest string `json:"authorization_digest"`
	RiskLevel           string `json:"risk_level"`
	State               string `json:"state"`
	ErrorCode           string `json:"error_code"`
	TraceID             string `json:"trace_id"`
	TaskID              string `json:"task_id"`
	CreatedAt           int64  `json:"created_at" gorm:"autoCreateTime:milli"`
}

func (AuditLog) TableName() string { return "banking_execution_audits" }

// Canonical hashing preserves integer precision and sorts object keys.
func CanonicalJSONHash(raw []byte) string {
	var data any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&data) != nil {
		return ""
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return ""
	}
	normalized, e := json.Marshal(data)
	if e != nil {
		return ""
	}
	return fmt.Sprintf("%x", sha256.Sum256(normalized))
}
func OperationDigest(v *OperationIntent) string {
	if v == nil {
		return ""
	}
	raw, _ := json.Marshal(struct {
		ID                                            string
		UserID                                        int64
		Kind, Agent                                   string
		Payload, Snapshot                             json.RawMessage
		ResourceID                                    int64
		ResourceDigest                                string
		SourceAccountID, TargetAccountID, AmountMinor int64
		Currency, Nonce                               string
		ExpiresAt                                     int64
	}{v.ID, v.UserID, v.Kind, v.Agent, v.Payload, v.Snapshot, v.ResourceID, v.ResourceDigest, v.SourceAccountID, v.TargetAccountID, v.AmountMinor, v.Currency, v.Nonce, v.ExpiresAt})
	return CanonicalJSONHash(raw)
}
