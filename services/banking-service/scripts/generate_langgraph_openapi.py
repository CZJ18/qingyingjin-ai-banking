"""Document the three implemented server-only LangGraph bridge routes."""
from pathlib import Path
import yaml

path = Path(__file__).resolve().parents[1] / "api/openapi.yaml"
api = yaml.safe_load(path.read_text(encoding="utf8"))
api["info"]["version"] = "0.13.0"
api["components"]["securitySchemes"]["AgentServiceKey"] = {"type": "apiKey", "in": "header", "name": "X-Banking-Agent-Key", "description": "Independent server-only delegation credential; never sent to a model or browser."}
schemas = api["components"]["schemas"]
task = {"type": "string", "pattern": r"^lg-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.[A-Za-z0-9][A-Za-z0-9_-]{0,63}$", "maxLength": 128}
schemas["LangGraphDelegationInput"] = {"type": "object", "additionalProperties": False, "required": ["task_id", "agent_type"], "properties": {"task_id": task, "agent_type": {"type": "string", "enum": ["supervisor", "billing", "subscription", "lifestyle", "savings"]}}}
schemas["LangGraphDelegation"] = {"type": "object", "required": ["token", "task_id", "agent_type", "expires_in_seconds"], "properties": {"token": {"type": "string", "readOnly": True, "description": "120s role/task-bound Agent JWT; runtime only, no authorization or execution scope."}, "task_id": task, "agent_type": {"type": "string"}, "expires_in_seconds": {"type": "integer", "enum": [120]}}}
schemas["LangGraphRunInput"] = {"type": "object", "additionalProperties": False, "required": ["skill_id", "skill_version", "tool", "execution_mode", "parameters"], "properties": {"skill_id": {"type": "string"}, "skill_version": {"type": "integer", "enum": [1]}, "tool": {"type": "string"}, "execution_mode": {"type": "string", "enum": ["read_only", "advice"]}, "parameters": {"type": "object", "description": "Strict native tool DTO; all monetary values are integer minor units."}, "use_coze": {"type": "boolean", "default": False}}}
schemas["LangGraphPlanInput"] = {"type": "object", "additionalProperties": False, "required": ["instruction", "bindings"], "properties": {"instruction": {"type": "string", "minLength": 1, "maxLength": 8192}, "bindings": {"$ref": "#/components/schemas/OrchestrationBindings"}}}
schemas["LangGraphAgentOutput"] = {"type": "object", "required": ["task_id", "agent_type", "status", "result", "evidence", "errors", "next_actions", "financial_execution_enabled", "skill_id", "skill_version", "rule_version"], "properties": {"task_id": task, "agent_type": {"type": "string"}, "status": {"type": "string", "enum": ["succeeded"]}, "skill_id": {"type": "string"}, "skill_version": {"type": "integer"}, "rule_version": {"type": "string"}, "result": {"type": "object"}, "evidence": {"type": "array", "items": {"type": "object", "properties": {"type": {"type": "string"}, "id": {"type": "string"}, "audit_id": {"type": "string"}}}}, "errors": {"type": "array", "items": {"type": "object"}}, "next_actions": {"type": "array", "items": {"type": "object"}}, "explanation": {"type": "string"}, "explanation_evidence_ids": {"type": "array", "items": {"type": "string"}}, "explanation_finding_ids": {"type": "array", "items": {"type": "string"}}, "workflow_execute_id": {"type": "string"}, "financial_execution_enabled": {"type": "boolean", "enum": [False]}}}
schemas["LangGraphPlannedTasks"] = {"type": "object", "required": ["tasks", "workflow_execute_id", "owner_engine", "financial_execution_enabled"], "properties": {"tasks": {"type": "array", "items": {"$ref": "#/components/schemas/OrchestrationTaskSpec"}}, "workflow_execute_id": {"type": "string"}, "owner_engine": {"type": "string", "enum": ["langgraph"]}, "financial_execution_enabled": {"type": "boolean", "enum": [False]}}}
schemas["LangGraphAgentOutput"]["properties"]["result"] = {"oneOf": [{"type": "object"}, {"type": "array", "items": {}}]}
for name in ("evidence", "errors", "next_actions"):
    schemas["LangGraphAgentOutput"]["properties"][name]["nullable"] = True
for suffix, input_schema, output_schema, description in [
    ("delegations", "LangGraphDelegationInput", "LangGraphDelegation", "User JWT plus server key delegates a namespaced task. Agent/tool identities cannot issue credentials."),
    ("run", "LangGraphRunInput", "LangGraphAgentOutput", "Signed specialist plus server key; role/user/task derived from JWT. Native Skill/DTO/evidence validation; READ/PLAN only. Sensitive preparation uses the separate existing gateway."),
    ("plan", "LangGraphPlanInput", "LangGraphPlannedTasks", "Signed Supervisor plus server key; configured native Coze planner only. Does not create a Go Job or transfer engine ownership."),
]:
    response_schema = {"allOf": [
        {"$ref": "#/components/schemas/Envelope"},
        {"type": "object", "properties": {"data": {"$ref": "#/components/schemas/" + output_schema}}},
    ]}
    responses = {"200": {"description": "Validated task-bound result", "content": {"application/json": {"schema": response_schema}}}}
    for status, text in [(400, "INVALID_ARGUMENT"), (401, "UNAUTHENTICATED"), (403, "FORBIDDEN"), (422, "INVALID_TASK_PLAN / TOOL_OUTPUT_INVALID"), (503, "DEPENDENCY_UNAVAILABLE / COZE_UNAVAILABLE"), (504, "TOOL_TIMEOUT")]:
        responses[str(status)] = {"description": text, "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Envelope"}}}}
    api["paths"]["/api/v1/langgraph/" + suffix] = {"post": {
        "operationId": "langgraph_" + suffix, "tags": ["Orchestrator"],
        "description": description, "security": [{"UserBearer": [], "AgentServiceKey": []}],
        "requestBody": {"required": True, "content": {"application/json": {"schema": {"$ref": "#/components/schemas/" + input_schema}}}},
        "responses": responses,
    }}
path.write_text(yaml.safe_dump(api, allow_unicode=True, sort_keys=False), encoding="utf8")
print("Updated implemented Go LangGraph bridge contract")
