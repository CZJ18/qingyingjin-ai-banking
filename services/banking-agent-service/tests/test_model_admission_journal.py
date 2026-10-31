"""Known monetary refusal is a failed read; uncertain dispatch remains unknown."""
import pytest
from test_agent_graph import TaskStore, system

from banking_agent.agent_graph import RunContext, journal_call
from banking_agent.agent_models import AgentSubmit
from banking_agent.errors import DomainError


@pytest.mark.asyncio
@pytest.mark.parametrize("code,status", [
    ("MODEL_BUDGET_UNAVAILABLE", 503),
    ("MODEL_MONETARY_ADMISSION_REQUIRED", 503),
    ("MODEL_EXECUTION_CONFIRMATION_REQUIRED", 503),
    ("MODEL_APPROVAL_REQUIRED", 403),
    ("MODEL_APPROVAL_LIMIT_EXCEEDED", 429),
    ("MODEL_SPEND_QUOTA_EXCEEDED", 429),
    ("MODEL_USER_MONTHLY_QUOTA_EXCEEDED", 429),
    ("MODEL_QUOTA_EXCEEDED", 429),
])
async def test_known_planning_refusal_is_failed_without_retry(code, status):
    request = AgentSubmit(instruction="Analyze isolated synthetic bills", max_attempts=3).model_dump()
    engine, store, tools, _ = system(request)
    attempts = 0

    async def refused(*_args):
        nonlocal attempts
        attempts += 1
        raise DomainError(code, "Explicit synthetic pre-dispatch refusal", status)

    tools.plan = refused
    result = await engine.run("42", store.task["task_id"], "synthetic-bearer", "synthetic-trace")
    assert result["status"] == "failed" and result["error_code"] == code
    assert store.calls["__internal:supervisor_plan"]["state"] == "failed"
    assert attempts == 1


@pytest.mark.asyncio
@pytest.mark.parametrize("mode,code,status", [
    ("read_only", "MODEL_OUTCOME_UNKNOWN", 409),
    ("read_only", "COZE_UNAVAILABLE", 503),
    ("read_only", "TOOL_TIMEOUT", 504),
    ("read_only", "TOOL_OUTPUT_INVALID", 502),
    ("intent", "MODEL_APPROVAL_REQUIRED", 403),
    ("advice", "MODEL_BUDGET_UNAVAILABLE", 503),
])
async def test_uncertain_or_mutating_call_keeps_unknown_and_cannot_replay(mode, code, status):
    store = TaskStore({})
    lease = store.claim("42", store.task["task_id"])
    context = RunContext("42", store.task["task_id"], lease, "synthetic-bearer", "synthetic-trace", store, None, 3)
    attempts = 0

    async def failed():
        nonlocal attempts
        attempts += 1
        raise DomainError(code, "Synthetic outcome not known safe", status)

    with pytest.raises(DomainError):
        await journal_call(context, "bounded_node", {}, mode, failed, idempotent=False)
    assert store.calls["bounded_node"]["state"] == "unknown"
    with pytest.raises(DomainError) as refused:
        await journal_call(context, "bounded_node", {}, mode, failed, idempotent=False)
    assert refused.value.code == "TOOL_OUTCOME_UNKNOWN" and attempts == 1
