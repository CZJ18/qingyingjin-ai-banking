"""Versioned specialist contracts; financial facts always come from Go tools."""

import json
from typing import Any

from pydantic import Field, model_validator

from .models import StrictInput

PAIRS = (
    {
        ("billing", tool): ("billing_analysis", "read_only")
        for tool in (
            "get_transactions",
            "get_account_summary",
            "analyze_spending",
            "compare_budget",
            "detect_anomalies",
            "generate_billing_report",
        )
    }
    | {
        ("subscription", tool): ("subscription_analysis", "read_only")
        for tool in (
            "detect_subscriptions",
            "get_subscription_details",
            "analyze_subscription_cost",
            "suggest_subscription_actions",
            "get_subscription_status",
        )
    }
    | {
        ("lifestyle", "get_lifestyle_goal"): ("lifestyle_planning", "read_only"),
        ("lifestyle", "get_lifestyle_plan"): ("lifestyle_planning", "read_only"),
        ("lifestyle", "compile_lifestyle_plan"): ("lifestyle_planning", "advice"),
        ("savings", "get_financial_plan"): ("savings_planning", "read_only"),
        ("savings", "compare_financial_plans"): ("savings_planning", "read_only"),
        ("savings", "replan_financial_plan"): ("savings_planning", "advice"),
        ("subscription", "create_cancel_intent"): ("operation_preparation", "intent"),
        ("supervisor", "require_gateway"): ("operation_preparation", "intent"),
        ("supervisor", "request_confirmation"): ("task_planning", "user_confirmation"),
    }
)
BLOCKED_PARAMETER_KEYS = {
    "confirmed",
    "authorization_id",
    "authorization",
    "authorizations",
    "confirmation_nonce",
    "nonce",
    "signature",
    "user_id",
    "owner_id",
    "owner_engine",
    "agent_type",
    "lease_token",
    "token",
    "bearer",
    "service_key",
    "api_key",
    "password",
    "secret",
    "access_token",
    "refresh_token",
    "agent_token",
    "x_banking_agent_key",
}


def safe_parameters(value, depth=0):
    if depth > 20 or isinstance(value, float):
        raise ValueError("bounded JSON and integer financial values required")
    if isinstance(value, dict):
        for key, item in value.items():
            if not isinstance(key, str) or key.lower().replace("-", "_") in BLOCKED_PARAMETER_KEYS:
                raise ValueError("identity or authorization cannot be provided by a task")
            if (
                key.endswith("_minor")
                and type(item) is not int
                and not (isinstance(item, dict) and set(item) == {"$ref"})
            ):
                raise ValueError("integer minor units required")
            safe_parameters(item, depth + 1)
    elif isinstance(value, list):
        for item in value:
            safe_parameters(item, depth + 1)
    elif value is not None and not isinstance(value, (str, int, bool)):
        raise ValueError("JSON primitives required")


class AgentTaskSpec(StrictInput):
    key: str = Field(pattern=r"^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$")
    agent_type: str
    tool: str
    skill_id: str
    skill_version: int = Field(ge=1)
    execution_mode: str
    parameters: dict[str, Any]
    dependencies: list[str] = Field(default_factory=list, max_length=32)
    priority: int = Field(default=100, ge=0, le=1000)
    use_coze: bool = False
    blocked: bool = False
    required_permissions: list[str] = Field(default_factory=list)

    @model_validator(mode="after")
    def registered_binding(self):
        if (
            PAIRS.get((self.agent_type, self.tool)) != (self.skill_id, self.execution_mode)
            or self.skill_version != 1
        ):
            raise ValueError("unknown skill/version/role/tool/mode")
        permissions = {
            "read_only": ["READ"],
            "advice": ["READ", "PLAN"],
            "intent": ["READ", "PREPARE"],
            "user_confirmation": [],
        }[self.execution_mode]
        if self.required_permissions and self.required_permissions != permissions:
            raise ValueError("permissions are derived from the registered tool")
        self.required_permissions = permissions
        safe_parameters(self.parameters)
        if len(json.dumps(self.parameters, ensure_ascii=False).encode()) > 65_536:
            raise ValueError("tool parameters exceed 64 KiB")
        if len(self.dependencies) != len(set(self.dependencies)):
            raise ValueError("repeated dependency")
        return self


class AgentSubmit(StrictInput):
    tasks: list[AgentTaskSpec] = Field(default_factory=list, max_length=32)
    instruction: str = Field(default="", max_length=8192)
    bindings: dict[str, Any] = Field(default_factory=dict)
    max_attempts: int = Field(default=2, ge=1, le=3)
    timeout_ms: int = Field(default=90_000, ge=100, le=180_000)

    @model_validator(mode="after")
    def valid_graph(self):
        if bool(self.tasks) == bool(self.instruction.strip()):
            raise ValueError("provide exactly one task plan or instruction")
        safe_parameters(self.bindings)
        keys = {task.key for task in self.tasks}
        if len(keys) != len(self.tasks):
            raise ValueError("duplicate task key")
        seen = set()
        while len(seen) < len(keys):
            ready = {
                task.key
                for task in self.tasks
                if task.key not in seen and set(task.dependencies) <= seen
            }
            if not ready:
                raise ValueError("cycle or unknown dependency")
            seen |= ready
        return self


class TaskVersionInput(StrictInput):
    expected_version: int = Field(ge=1, le=9_223_372_036_854_775_806)


class AgentResume(TaskVersionInput):
    node_key: str = Field(pattern=r"^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$")
    digest: str = Field(pattern="^[0-9a-f]{64}$")
    nonce: str = Field(min_length=16, max_length=128)
    decision: str = Field(pattern="^(approve|reject)$")
