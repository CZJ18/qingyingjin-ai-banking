"""Pure scoped-task regressions: no database, environment config or real network."""
import httpx
import pytest
from pydantic import ValidationError

from banking_agent.agent_models import AgentSubmit
from banking_agent.agent_tools import AgentBankingClient
from banking_agent.errors import DomainError

BINDINGS = {"account_id": "23", "from": "2026-08-31T16:00:00.000Z", "to": "2026-09-30T16:00:00.000Z"}
TASK = "lg-11111111-1111-4111-8111-111111111111"


def global_spec(tool):
    return {"key": "subscriptions", "agent_type": "subscription", "tool": tool,
            "skill_id": "subscription_analysis", "skill_version": 1,
            "execution_mode": "read_only", "parameters": {}}


@pytest.mark.parametrize("tool", ["analyze_subscription_cost", "suggest_subscription_actions"])
@pytest.mark.parametrize("bindings", [{"account_id": "23"}, {"from": BINDINGS["from"], "to": BINDINGS["to"]}, BINDINGS])
def test_explicit_scoped_task_cannot_select_global_subscription_tools(tool, bindings):
    with pytest.raises(ValidationError):
        AgentSubmit(tasks=[global_spec(tool)], bindings=bindings)


@pytest.mark.parametrize("tool", ["analyze_subscription_cost", "suggest_subscription_actions"])
def test_unbound_subscription_cost_and_suggestions_remain_compatible(tool):
    assert AgentSubmit(tasks=[global_spec(tool)]).tasks[0].tool == tool


def test_scoped_subscription_detection_still_accepts_exact_binding():
    scoped = global_spec("detect_subscriptions") | {"parameters": BINDINGS}
    assert AgentSubmit(tasks=[scoped], bindings=BINDINGS).tasks[0].parameters == BINDINGS


@pytest.mark.asyncio
async def test_banking_planner_client_does_not_discard_bindings_on_response_validation():
    def handler(request):
        if request.url.path.endswith("delegations"):
            data = {"token": "synthetic-delegation", "expires_in_seconds": 120, "agent_type": "supervisor", "task_id": TASK + ".supervisor_plan"}
        else:
            assert request.url.path == "/api/v1/langgraph/plan"
            data = {"owner_engine": "langgraph", "financial_execution_enabled": False,
                    "workflow_execute_id": "synthetic-execution", "tasks": [global_spec("analyze_subscription_cost")]}
        return httpx.Response(200, json={"code": "OK", "data": data})

    client = AgentBankingClient("http://banking.test", "synthetic-service-key-" + "x" * 32, transport=httpx.MockTransport(handler))
    try:
        with pytest.raises(DomainError) as caught:
            await client.plan("synthetic-bearer", "42", TASK, "仅查所选账户", BINDINGS, "scope-test")
        assert caught.value.code == "TOOL_OUTPUT_INVALID"
    finally:
        await client.close()


@pytest.mark.asyncio
async def test_graph_cannot_drop_bindings_when_revalidating_a_planner_result():
    from test_agent_graph import system
    engine, store, tools, _ = system(AgentSubmit(instruction="仅查所选账户", bindings=BINDINGS, max_attempts=1).model_dump())

    async def plan(*_args):
        return {"tasks": [global_spec("suggest_subscription_actions")], "workflow_execute_id": "synthetic-execution"}

    tools.plan = plan
    result = await engine.run("42", store.task["task_id"], "synthetic-bearer", "scope-test")
    assert result["status"] == "failed"
    assert result["error_code"] == "TOOL_OUTPUT_INVALID"
    assert tools.called == []


def test_historical_scoped_global_request_remains_readable_in_task_view():
    from banking_agent.agent_models import AgentTaskRequest
    from banking_agent.agent_routes import AgentTaskView

    request = {"tasks": [global_spec("analyze_subscription_cost")], "bindings": BINDINGS,
               "max_attempts": 1, "timeout_ms": 90000}
    stored = AgentTaskRequest.model_validate(request)
    view = AgentTaskView.model_validate({
        "task_id": TASK, "owner_engine": "langgraph", "status": "succeeded", "version": 4,
        "request": request, "result": {"outputs": [{"historical": True}]},
        "error_code": "", "confirmation": None,
        "created_at": "2026-10-28T00:00:00Z", "updated_at": "2026-10-28T00:00:00Z",
    })
    assert view.request == stored
    assert view.result == {"outputs": [{"historical": True}]}
    # Read compatibility does not permit a malformed graph or new execution.
    with pytest.raises(ValidationError):
        AgentTaskRequest.model_validate(request | {"instruction": "mixed instruction"})
    with pytest.raises(ValidationError):
        AgentSubmit.model_validate(request)


@pytest.mark.asyncio
async def test_legacy_selected_global_task_is_rejected_before_any_tool_execution():
    from test_agent_graph import system

    request = {"tasks": [global_spec("suggest_subscription_actions")], "instruction": "",
               "bindings": BINDINGS, "max_attempts": 1, "timeout_ms": 90000, "monthly_goal": None}
    engine, store, tools, _ = system(request)
    result = await engine.run("42", store.task["task_id"], "synthetic-bearer", "history-scope-test")
    assert result["status"] == "failed" and result["error_code"] == "INVALID_TASK_PLAN"
    assert result["request"] == request and tools.called == []
