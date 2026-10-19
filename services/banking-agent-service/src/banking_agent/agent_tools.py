"""Task-bound native READ/PLAN/PREPARE bridge; credentials never become outputs."""

import json
import math
from typing import Any
from uuid import UUID

import httpx
from pydantic import ValidationError

from .agent_models import AgentSubmit, AgentTaskSpec, safe_parameters
from .banking import (
    _MAX_RESPONSE_BYTES,
    _TOKEN,
    _TRACE,
    BankingClient,
    _invalid_constant,
    _unique_object,
)
from .errors import DomainError
from .source import canonical_numeric_id, normalize_resource

_POST_PATHS = frozenset(
    {
        "/api/v1/langgraph/delegations",
        "/api/v1/langgraph/run",
        "/api/v1/langgraph/plan",
        "/api/v1/execution/operations",
    }
)
_SECRET_KEYS = frozenset(
    {
        "token",
        "access_token",
        "refresh_token",
        "agent_token",
        "bearer",
        "authorization",
        "authorization_id",
        "authorizations",
        "confirmation_nonce",
        "nonce",
        "signature",
        "service_key",
        "lease_token",
        "api_key",
        "secret",
        "password",
        "x_banking_agent_key",
    }
)
_OUTPUT_FIELDS = frozenset(
    {
        "skill_id",
        "skill_version",
        "rule_version",
        "task_id",
        "agent_type",
        "status",
        "result",
        "evidence",
        "errors",
        "next_actions",
        "explanation",
        "explanation_evidence_ids",
        "explanation_finding_ids",
        "workflow_execute_id",
        "financial_execution_enabled",
    }
)


def _invalid_output() -> DomainError:
    return DomainError("TOOL_OUTPUT_INVALID", "Banking agent output failed validation", 502)


def _bounded_json(value: Any, depth: int = 0) -> None:
    if depth > 20:
        raise ValueError("bounded JSON required")
    if type(value) is dict:
        for key, item in value.items():
            if type(key) is not str:
                raise ValueError("JSON key required")
            _bounded_json(item, depth + 1)
    elif type(value) is list:
        for item in value:
            _bounded_json(item, depth + 1)
    elif type(value) is float:
        if not math.isfinite(value):
            raise ValueError("finite JSON required")
    elif value is not None and type(value) not in (str, int, bool):
        raise ValueError("JSON required")


def _clean(value: Any, secrets: tuple[str, ...]) -> Any:
    if type(value) is dict:
        return {
            key: _clean(item, secrets)
            for key, item in value.items()
            if key.lower().replace("-", "_") not in _SECRET_KEYS
        }
    if type(value) is list:
        return [_clean(item, secrets) for item in value]
    if type(value) is str:
        for secret in secrets:
            if secret:
                value = value.replace(secret, "[redacted]")
    return value


def _task_namespace(value: Any) -> str:
    if type(value) is not str or not value.startswith("lg-"):
        raise ValueError("LangGraph task identity required")
    raw = value[3:]
    if str(UUID(raw)) != raw:
        raise ValueError("canonical task identity required")
    return value


def _matches_parameters(expected: Any, actual: Any) -> bool:
    """Native typed structs add defaults; every supplied field must still match."""
    if type(expected) is not type(actual):
        return False
    if type(expected) is dict:
        return all(
            key in actual and _matches_parameters(value, actual[key])
            for key, value in expected.items()
        )
    if type(expected) is list:
        return len(expected) == len(actual) and all(
            _matches_parameters(left, right) for left, right in zip(expected, actual, strict=True)
        )
    return expected == actual


def _owned_output(
    value: Any,
    user_id: str,
    spec: AgentTaskSpec | None = None,
    path: tuple[str, ...] = (),
) -> None:
    # Go declares these exact Lifestyle Spec.Evidence fields as map[string]string.
    # They quote user text; a matching key is neither money nor ownership metadata.
    lifestyle = spec is not None and spec.agent_type == "lifestyle"
    text_evidence_paths = {
        ("result", "plan", "snapshot", "goal", "evidence"),
        ("result", "versions", "spec", "evidence"),
    }
    if lifestyle and path in text_evidence_paths:
        if type(value) is not dict or any(type(item) is not str for item in value.values()):
            raise _invalid_output()
        return
    if type(value) is dict:
        for key, item in value.items():
            child_path = (*path, key)
            nullable_ceiling = (
                lifestyle
                and child_path
                == (
                    "result",
                    "plan",
                    "snapshot",
                    "savings_ceiling_minor",
                )
                and item is None
            )
            if key.endswith("_minor") and type(item) is not int and not nullable_ceiling:
                raise _invalid_output()
            if key in ("user_id", "owner_id") and item != user_id:
                raise _invalid_output()
            if key == "financial_execution_enabled" and item is not False:
                raise _invalid_output()
            _owned_output(item, user_id, spec, child_path)
    elif type(value) is list:
        for item in value:
            _owned_output(item, user_id, spec, path)


class AgentBankingClient(BankingClient):
    def __init__(
        self,
        base_url: str,
        service_key: str,
        timeout: float = 65,
        transport: httpx.AsyncBaseTransport | None = None,
    ):
        if type(timeout) not in (int, float) or not math.isfinite(timeout) or timeout <= 60:
            raise ValueError("Agent bridge timeout must exceed the native 60 second deadline")
        if type(service_key) is not str:
            raise ValueError("Agent service credential must be a string")
        super().__init__(base_url, timeout=timeout, transport=transport)
        self._service_key = service_key

    def __repr__(self) -> str:
        return "AgentBankingClient()"

    def _validate_entry(self, user_token: str, user_id: str, task_id: str, trace_id: str) -> None:
        if not self._service_key:
            raise DomainError(
                "DEPENDENCY_UNAVAILABLE", "Banking agent bridge is not configured", 503
            )
        try:
            canonical_numeric_id(user_id)
            _task_namespace(task_id)
            if type(trace_id) is not str or not _TRACE.fullmatch(trace_id):
                raise ValueError("invalid trace")
            if not _TOKEN.fullmatch(self._service_key):
                raise ValueError("invalid service credential")
        except (ValueError, TypeError, AttributeError):
            raise DomainError(
                "INVALID_ARGUMENT", "Valid task, user and trace identities are required", 400
            ) from None
        if type(user_token) is not str or not _TOKEN.fullmatch(user_token):
            raise DomainError("UNAUTHENTICATED", "A valid user bearer token is required", 401)

    async def _request(
        self,
        method: str,
        path: str,
        token: str,
        trace_id: str,
        body: dict | None = None,
        idempotency_key: str | None = None,
    ) -> dict:
        if not (
            (method == "POST" and path in _POST_PATHS)
            or (method == "GET" and path == "/api/v1/skills")
        ):
            raise DomainError("INVALID_ARGUMENT", "Banking agent route is not allowed", 400)
        if not self._service_key:
            raise DomainError(
                "DEPENDENCY_UNAVAILABLE", "Banking agent bridge is not configured", 503
            )
        if type(token) is not str or not _TOKEN.fullmatch(token):
            raise DomainError("UNAUTHENTICATED", "A valid bearer token is required", 401)
        if type(trace_id) is not str or not _TRACE.fullmatch(trace_id):
            raise DomainError("INVALID_ARGUMENT", "A valid trace ID is required", 400)
        if not _TOKEN.fullmatch(self._service_key):
            raise DomainError(
                "DEPENDENCY_UNAVAILABLE", "Banking agent bridge is not configured", 503
            )
        headers = {
            "Authorization": "Bearer " + token,
            "X-Trace-ID": trace_id,
            "X-Banking-Agent-Key": self._service_key,
        }
        if idempotency_key is not None:
            if not _TRACE.fullmatch(idempotency_key):
                raise DomainError(
                    "INVALID_ARGUMENT", "A valid invocation identity is required", 400
                )
            headers["Idempotency-Key"] = idempotency_key
        try:
            async with self._client.stream(method, path, headers=headers, json=body) as response:
                if response.status_code in (401, 403, 404, 409, 422, 429):
                    failures = {
                        401: ("UNAUTHENTICATED", "A valid bearer identity is required"),
                        403: ("FORBIDDEN", "Banking agent access is forbidden"),
                        404: ("SOURCE_NOT_FOUND", "Banking resource was not found"),
                        409: ("CONFLICT", "Banking operation conflicts with current state"),
                        422: ("TOOL_OUTPUT_INVALID", "Banking rejected the task contract"),
                        429: ("RATE_LIMITED", "Banking agent request limit exceeded"),
                    }
                    code, message = failures[response.status_code]
                    code, message = await self._business_failure(response, code, message)
                    raise DomainError(code, message, response.status_code)
                if response.status_code == 504:
                    raise DomainError("TOOL_TIMEOUT", "Banking agent deadline exceeded", 504)
                if response.status_code >= 500:
                    code, message = await self._business_failure(
                        response, "DEPENDENCY_UNAVAILABLE", "Banking agent dependency is unavailable"
                    )
                    raise DomainError(
                        code, message, 503
                    )
                if response.status_code == 400:
                    raise DomainError(
                        "INVALID_ARGUMENT", "Banking rejected the task parameters", 400
                    )
                if (
                    response.status_code not in (200, 201)
                    or response.headers.get("content-encoding", "identity").lower() != "identity"
                ):
                    raise _invalid_output()
                length = response.headers.get("content-length")
                if length is not None and (
                    len(length) > 10
                    or not length.isascii()
                    or not length.isdecimal()
                    or int(length) > _MAX_RESPONSE_BYTES
                ):
                    raise _invalid_output()
                payload = bytearray()
                async for chunk in response.aiter_bytes():
                    if len(payload) + len(chunk) > _MAX_RESPONSE_BYTES:
                        raise _invalid_output()
                    payload.extend(chunk)
        except httpx.TimeoutException:
            raise DomainError("TOOL_TIMEOUT", "Banking agent deadline exceeded", 504) from None
        except httpx.RequestError:
            raise DomainError(
                "DEPENDENCY_UNAVAILABLE", "Banking agent dependency is unavailable", 503
            ) from None
        try:
            envelope = json.loads(
                payload, object_pairs_hook=_unique_object, parse_constant=_invalid_constant
            )
            _bounded_json(envelope)
            if (
                type(envelope) is not dict
                or envelope.get("code") != "OK"
                or type(envelope.get("data")) is not dict
            ):
                raise ValueError("invalid envelope")
            return envelope["data"]
        except (ValueError, TypeError, UnicodeDecodeError, RecursionError):
            raise _invalid_output() from None

    @staticmethod
    async def _business_failure(response, default_code, default_message):
        # Preserve only known business codes and local messages. Never reflect
        # provider text, credentials or an unbounded error response into a task.
        failures = {
            (422, "INPUT_REQUIRED"): "Additional task inputs are required",
            (409, "MODEL_OUTCOME_UNKNOWN"): "Model outcome is unknown; inspect this task before retrying",
            (409, "MODEL_REQUEST_CONFLICT"): "Model request conflicts with its persisted invocation",
            (422, "MODEL_OUTPUT_INVALID"): "Model output failed the task contract",
            (422, "INVALID_TASK_PLAN"): "Model task graph failed role, skill, tool or dependency validation",
            (429, "MODEL_QUOTA_EXCEEDED"): "Model call allowance is exhausted",
            (503, "COZE_UNAVAILABLE"): "Configured model workflow is unavailable",
            (503, "MODEL_BUDGET_UNAVAILABLE"): "Model allowance tracking is unavailable",
        }
        try:
            payload = bytearray()
            async for chunk in response.aiter_bytes():
                if len(payload) + len(chunk) > 4096:
                    return default_code, default_message
                payload.extend(chunk)
            envelope = json.loads(payload, object_pairs_hook=_unique_object, parse_constant=_invalid_constant)
            code = envelope.get("code") if type(envelope) is dict else None
            message = failures.get((response.status_code, code)) if type(code) is str else None
            if message:
                return code, message
        except (ValueError, TypeError, UnicodeDecodeError, RecursionError):
            pass
        return default_code, default_message

    async def _delegate(self, user_token: str, task_id: str, role: str, trace_id: str) -> str:
        data = await self._request(
            "POST",
            "/api/v1/langgraph/delegations",
            user_token,
            trace_id,
            {"task_id": task_id, "agent_type": role},
        )
        token = data.get("token")
        ttl = data.get("expires_in_seconds")
        if (
            type(token) is not str
            or not _TOKEN.fullmatch(token)
            or type(ttl) is not int
            or not 1 <= ttl <= 120
            or data.get("agent_type") != role
            or data.get("task_id") != task_id
        ):
            raise _invalid_output()
        return token

    async def execute(
        self,
        user_token: str,
        user_id: str,
        task_id: str,
        spec_dict: dict,
        trace_id: str,
    ) -> dict:
        self._validate_entry(user_token, user_id, task_id, trace_id)
        try:
            if type(spec_dict) is not dict:
                raise ValueError("task object required")
            spec = AgentTaskSpec.model_validate(spec_dict)
            if _clean(spec.parameters, ()) != spec.parameters:
                raise ValueError("authorization materials are forbidden")
        except (ValidationError, ValueError, TypeError, RecursionError):
            raise DomainError(
                "INVALID_ARGUMENT", "A registered agent task is required", 400
            ) from None
        if spec.execution_mode not in ("read_only", "advice", "intent"):
            raise DomainError(
                "INVALID_ARGUMENT", "This task requires graph-level user confirmation", 400
            )
        bound_id = task_id + "." + spec.key
        if spec.execution_mode == "intent":
            body = (
                {"kind": "cancel_subscription", "parameters": spec.parameters}
                if spec.tool == "create_cancel_intent"
                else spec.parameters
            )
            if (
                set(body) - {"kind", "parameters", "expires_in_seconds"}
                or type(body.get("kind")) is not str
                or type(body.get("parameters")) is not dict
                or (
                    "expires_in_seconds" in body
                    and (
                        type(body["expires_in_seconds"]) is not int
                        or not 1 <= body["expires_in_seconds"] <= 900
                    )
                )
            ):
                raise DomainError(
                    "INVALID_ARGUMENT", "A typed operation preparation is required", 400
                )
            token = await self._delegate(user_token, bound_id, spec.agent_type, trace_id)
            data = await self._request(
                "POST",
                "/api/v1/execution/operations",
                token,
                trace_id,
                body,
                "langgraph:" + bound_id,
            )
            out = self._intent_output(data, user_id, bound_id, spec, body)
        else:
            token = await self._delegate(user_token, bound_id, spec.agent_type, trace_id)
            data = await self._request(
                "POST",
                "/api/v1/langgraph/run",
                token,
                trace_id,
                {
                    "skill_id": spec.skill_id,
                    "skill_version": spec.skill_version,
                    "tool": spec.tool,
                    "execution_mode": spec.execution_mode,
                    "parameters": spec.parameters,
                    "use_coze": spec.use_coze,
                },
            )
            out = self._agent_output(data, bound_id, spec)
            _owned_output(out, user_id, spec)
        return _clean(out, (self._service_key, user_token, token))

    @staticmethod
    def _agent_output(data: dict, task_id: str, spec: AgentTaskSpec) -> dict:
        if (
            data.get("task_id") != task_id
            or data.get("agent_type") != spec.agent_type
            or data.get("skill_id") != spec.skill_id
            or type(data.get("skill_version")) is not int
            or data.get("skill_version") != spec.skill_version
            or data.get("status") != "succeeded"
            or data.get("financial_execution_enabled") is not False
            or type(data.get("result")) not in (dict, list)
            or type(data.get("explanation")) is not str
            or type(data.get("workflow_execute_id")) is not str
        ):
            raise _invalid_output()
        for name in ("evidence", "errors", "next_actions"):
            if data.get(name) is not None and type(data.get(name)) is not list:
                raise _invalid_output()
        for row in data.get("evidence") or []:
            if (
                type(row) is not dict
                or type(row.get("type")) is not str
                or type(row.get("id")) is not str
                or type(row.get("audit_id", "")) is not str
            ):
                raise _invalid_output()
        out = {key: value for key, value in data.items() if key in _OUTPUT_FIELDS}
        for name in ("evidence", "errors", "next_actions"):
            out[name] = out.get(name) or []
        return out

    @staticmethod
    def _intent_output(
        data: dict, user_id: str, task_id: str, spec: AgentTaskSpec, body: dict
    ) -> dict:
        operation = data.get("operation")
        if (
            type(operation) is not dict
            or operation.get("status") != "pending_authorization"
            or operation.get("task_id") != task_id
            or operation.get("agent") != spec.agent_type
            or operation.get("kind") != body["kind"]
            or operation.get("skill_id") != spec.skill_id
            or type(operation.get("skill_version")) is not int
            or operation.get("skill_version") != spec.skill_version
            or not _matches_parameters(body["parameters"], operation.get("parameters"))
        ):
            raise _invalid_output()
        if body["kind"] == "transfer":
            for name in ("amount_minor", "source_account_id", "target_account_id", "currency"):
                if name in body["parameters"] and not _matches_parameters(
                    body["parameters"][name], operation.get(name)
                ):
                    raise _invalid_output()
        try:
            projection = normalize_resource("operation", operation.get("id"), user_id, data)[0]
        except DomainError:
            raise _invalid_output() from None
        return {
            "skill_id": spec.skill_id,
            "skill_version": spec.skill_version,
            "rule_version": "1",
            "task_id": task_id,
            "agent_type": spec.agent_type,
            "status": "succeeded",
            "result": projection.payload,
            "evidence": [
                {
                    "type": "operation",
                    "id": projection.source_id,
                    "audit_id": projection.audit_id or "",
                }
            ],
            "errors": [],
            "next_actions": [{"type": "authorize_operation", "requires_confirmation": True}],
            "explanation": "",
            "workflow_execute_id": "",
            "financial_execution_enabled": False,
        }

    async def plan(
        self,
        user_token: str,
        user_id: str,
        task_id: str,
        instruction: str,
        bindings: dict,
        trace_id: str,
    ) -> dict:
        self._validate_entry(user_token, user_id, task_id, trace_id)
        try:
            if type(bindings) is not dict:
                raise ValueError("bindings object required")
            safe_parameters(bindings)
            if _clean(bindings, ()) != bindings:
                raise ValueError("authorization materials are forbidden")
            submitted = AgentSubmit(instruction=instruction, bindings=bindings)
        except (ValidationError, ValueError, TypeError, RecursionError):
            raise DomainError(
                "INVALID_ARGUMENT", "A bounded instruction and safe bindings are required", 400
            ) from None
        token = await self._delegate(
            user_token, task_id + ".supervisor_plan", "supervisor", trace_id
        )
        data = await self._request(
            "POST",
            "/api/v1/langgraph/plan",
            token,
            trace_id,
            {"instruction": submitted.instruction, "bindings": submitted.bindings},
        )
        try:
            if (
                data.get("owner_engine") != "langgraph"
                or data.get("financial_execution_enabled") is not False
                or type(data.get("workflow_execute_id")) is not str
                or not data["workflow_execute_id"]
                or type(data.get("tasks")) is not list
            ):
                raise ValueError("invalid planned output")
            native_tasks = []
            for task in data["tasks"]:
                if type(task) is not dict:
                    raise ValueError("planned task object required")
                task = dict(task)
                if task.get("dependencies") is None:
                    task["dependencies"] = []
                native_tasks.append(task)
            tasks = AgentSubmit(tasks=native_tasks).tasks
        except (ValidationError, ValueError, TypeError, RecursionError):
            raise _invalid_output() from None
        return _clean(
            {
                "tasks": [task.model_dump() for task in tasks],
                "workflow_execute_id": data["workflow_execute_id"],
                "owner_engine": "langgraph",
                "financial_execution_enabled": False,
            },
            (self._service_key, user_token, token),
        )
