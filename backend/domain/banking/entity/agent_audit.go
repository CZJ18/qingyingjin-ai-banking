/*
 * Copyright 2025 coze-dev Authors
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package entity

import "encoding/json"

type AgentAuditOutcome string

const (
	AgentAuditOutcomeSuccess AgentAuditOutcome = "success"
	AgentAuditOutcomeFailed  AgentAuditOutcome = "failed"
)

type AgentAudit struct {
	ID         int64
	UserID     int64
	AgentID    string
	WorkflowID string
	ToolID     string
	RunID      string
	TraceID    string
	Action     string
	Outcome    AgentAuditOutcome
	// RequestJSON and ResponseJSON may contain only redacted, size-limited,
	// valid JSON. Application/service adapters must enforce this before
	// persistence; the entity deliberately contains no sanitization logic.
	RequestJSON  json.RawMessage
	ResponseJSON json.RawMessage
	ErrorCode    string
	ErrorMessage string
	CreatedAt    int64
}
