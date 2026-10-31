"""Synthetic nonpaid contract checks; all HTTP is intercepted in memory."""

import asyncio
import json
from copy import deepcopy

import httpx
import pytest
from pydantic import ValidationError

from banking_agent.agent_models import AgentTaskSpec
from banking_agent.agent_tools import AgentBankingClient
from banking_agent.errors import DomainError


TASK = "lg-11111111-1111-4111-8111-111111111111"
SCENARIOS = ("transfer", "cards", "investment", "subscriptions", "birthday")


def spec(scenario="cards", **changes):
    return {
        "key": "overview",
        "agent_type": "supervisor",
        "tool": "get_scenario_overview",
        "skill_id": "task_planning",
        "skill_version": 1,
        "execution_mode": "read_only",
        "parameters": {"scenario": scenario, "account_id": "23"},
        "dependencies": [],
    } | changes


def result(scenario="cards"):
    return {
        "scenario": scenario,
        "account_id": "23",
        "user_id": "42",
        "is_simulated": True,
        "items": [],
        "truncated": False,
        "limitations": ["Synthetic local resources; no bank or merchant connection."],
    }


def run(task, value):
    requests = []

    def handler(request):
        requests.append(request)
        body = json.loads(request.content)
        if request.url.path == "/api/v1/langgraph/delegations":
            assert body == {"task_id": TASK + ".overview", "agent_type": "supervisor"}
            data = {
                "token": "synthetic-agent",
                "expires_in_seconds": 120,
                "agent_type": "supervisor",
                "task_id": TASK + ".overview",
            }
        else:
            assert request.url.path == "/api/v1/langgraph/run"
            assert body["tool"] == "get_scenario_overview"
            assert body["execution_mode"] == "read_only"
            assert body["parameters"] == task["parameters"]
            data = {
                "task_id": TASK + ".overview",
                "agent_type": "supervisor",
                "skill_id": "task_planning",
                "skill_version": 1,
                "status": "succeeded",
                "result": value,
                "evidence": [],
                "errors": [],
                "next_actions": [],
                "explanation": "",
                "workflow_execute_id": "",
                "financial_execution_enabled": False,
            }
        return httpx.Response(200, json={"code": "OK", "data": data})

    async def call():
        bridge = AgentBankingClient(
            "http://banking.test",
            "synthetic-service-" + "x" * 32,
            transport=httpx.MockTransport(handler),
        )
        try:
            return await bridge.execute("synthetic-user", "42", TASK, task, "overview.test")
        finally:
            await bridge.close()

    return asyncio.run(call()), requests


@pytest.mark.parametrize("scenario", SCENARIOS)
def test_scenario_overview_runs_only_existing_read_bridge(scenario):
    task = spec(scenario)
    assert AgentTaskSpec.model_validate(task).required_permissions == ["READ"]
    out, requests = run(task, result(scenario))
    assert out["result"] == result(scenario)
    assert out["financial_execution_enabled"] is False
    assert [request.url.path for request in requests] == [
        "/api/v1/langgraph/delegations", "/api/v1/langgraph/run"
    ]


@pytest.mark.parametrize("parameters", [
    {}, {"scenario": "cards"}, {"scenario": "billing", "account_id": "23"},
    {"scenario": "cards", "account_id": "0"},
    {"scenario": "cards", "account_id": "023"},
    {"scenario": "cards", "account_id": 23},
    {"scenario": "cards", "account_id": "9223372036854775808"},
    {"scenario": "cards", "account_id": "23", "from": "2026-10-01"},
    {"scenario": "cards", "account_id": {"$ref": "read.result.id"}},
])
def test_scenario_overview_requires_explicit_bounded_parameters(parameters):
    with pytest.raises(ValidationError):
        AgentTaskSpec.model_validate(spec(parameters=parameters))


def test_scenario_overview_does_not_request_a_specialist_model_call():
    with pytest.raises(ValidationError):
        AgentTaskSpec.model_validate(spec(use_coze=True))


@pytest.mark.parametrize("changes", [
    {"scenario": "transfer"}, {"account_id": "24"}, {"user_id": "43"},
    {"is_simulated": False}, {"is_simulated": "true"},
    {"items": [{}] * 51}, {"items": ["not a resource"]},
    {"limitations": "not an array"}, {"limitations": ["x"] * 17},
    {"limitations": [""]}, {"limitations": ["x" * 2049]},
    {"authorization_granted": True},
    {"truncated": 1},
])
def test_scenario_overview_refuses_scope_type_and_limit_spoof(changes):
    with pytest.raises(DomainError) as caught:
        run(spec(), result() | changes)
    assert (caught.value.code, caught.value.status) == ("TOOL_OUTPUT_INVALID", 502)


@pytest.mark.parametrize("field", ["scenario", "account_id", "user_id", "is_simulated", "items", "limitations", "truncated"])
def test_scenario_overview_required_result_fields_cannot_disappear(field):
    value = result()
    del value[field]
    with pytest.raises(DomainError) as caught:
        run(spec(), value)
    assert caught.value.code == "TOOL_OUTPUT_INVALID"


def test_scenario_overview_preserves_simulated_items_but_rejects_nested_foreign_owner():
    value = result()
    value["items"] = [{"id": "11111111-1111-4111-8111-111111111111", "kind": "card", "status": "active", "transaction_limit_minor": 1200}]
    out, _ = run(spec(), value)
    assert out["result"] == value
    foreign = deepcopy(value)
    foreign["items"][0]["owner_id"] = "43"
    with pytest.raises(DomainError) as caught:
        run(spec(), foreign)
    assert caught.value.code == "TOOL_OUTPUT_INVALID"


def test_scenario_overview_source_account_item_cannot_expand_outer_binding():
    from banking_agent.agent_tools import _scenario_overview_output
    value = result("transfer")
    value["items"] = [{"id": "24", "kind": "source_account", "status": "active", "currency": "CNY", "amount_minor": 100}]
    with pytest.raises(DomainError) as caught:
        _scenario_overview_output(value, "42", AgentTaskSpec.model_validate(spec("transfer")))
    assert caught.value.code == "TOOL_OUTPUT_INVALID"
