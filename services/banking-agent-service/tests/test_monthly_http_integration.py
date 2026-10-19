"""Real SQL/checkpoint and ASGI HTTP tests; Banking/model outputs are explicit fakes.

Use only BANKING_AGENT_TEST_DATABASE_URL / BANKING_AGENT_TEST_ADMIN_URL pointing
to a disposable test database. These checks do not call a supplier or move funds.
"""

import os
from datetime import datetime
from uuid import uuid4
from zoneinfo import ZoneInfo

import psycopg
import pytest
from fastapi.testclient import TestClient
from test_agent_models import spec
from test_api import Bank, Cache, headers

from banking_agent.config import Settings
from banking_agent.main import create_app
from banking_agent.migrate import apply_migrations
from banking_agent.repository import Repository


class MonthlyTools:
    def __init__(self):
        self.calls = []

    async def plan(self, _token, _user, _task, _instruction, _bindings, _trace):
        return {"tasks": [planning_spec()], "workflow_execute_id": "synthetic-workflow"}

    async def execute(self, _token, _user, task, item, _trace):
        self.calls.append(item["key"])
        result = {"currency": "CNY", "balance_minor": 300000, "source_version": 1}
        if item["agent_type"] == "savings":
            result = {
                "plan": {"id": "301", "user_id": _user, "current_version": 1},
                "version": {"id": "401", "user_id": _user, "plan_id": "301", "version": 1},
                "result": {
                    "currency": "CNY",
                    "period": datetime.now(ZoneInfo("Asia/Shanghai")).strftime("%Y-%m"),
                    "suggested_savings_minor": 200000,
                    "options": [
                        {"name": "balanced", "feasible": True, "savings_minor": 200000,
                         "retained_minor": 100000, "allocations": [],
                         "explanation": "Synthetic fixture suggestion"}
                    ],
                    "blockers": [],
                    "financial_execution_enabled": False,
                },
                "financial_execution_enabled": False,
            }
        return {
            "task_id": task + "." + item["key"],
            "agent_type": item["agent_type"],
            "skill_id": item["skill_id"],
            "skill_version": item["skill_version"],
            "status": "succeeded",
            "result": result,
            "evidence": [{"type": "synthetic_account", "id": "101", "version": 1}],
            "errors": [],
            "next_actions": [],
            "financial_execution_enabled": False,
        }


@pytest.fixture
def monthly_system():
    app_url = os.getenv("BANKING_AGENT_TEST_DATABASE_URL")
    admin_url = os.getenv("BANKING_AGENT_TEST_ADMIN_URL")
    if not app_url or not admin_url:
        pytest.skip("isolated PostgreSQL integration credentials are not configured")
    apply_migrations(admin_url)
    repository = Repository(app_url)
    tools = MonthlyTools()
    settings = Settings(
        app_url,
        "redis://:fixture-only@127.0.0.1:1/0",
        "http://127.0.0.1:1",
        agent_service_key="monthly-fixture-service-key-32-characters",
    )
    app = create_app(
        settings, banking=Bank(), repository=repository, cache=Cache(), agent_tools=tools
    )
    try:
        with TestClient(app) as client:
            yield client, tools, repository, admin_url
    finally:
        repository.close()


def planning_spec():
    return spec(
        agent_type="savings", tool="get_financial_plan", skill_id="savings_planning",
        parameters={"plan_id": "301"},
    ).model_dump()


def monthly_request(with_intent=False):
    read = planning_spec()
    tasks = [read]
    if with_intent:
        tasks.append(
            spec(
                key="prepare_transfer",
                agent_type="supervisor",
                tool="require_gateway",
                skill_id="operation_preparation",
                execution_mode="intent",
                dependencies=[read["key"]],
                parameters={"kind": "transfer", "parameters": {"amount_minor": 100}},
            ).model_dump()
        )
    return {
        "tasks": tasks,
        "monthly_goal": {
            "period": datetime.now(ZoneInfo("Asia/Shanghai")).strftime("%Y-%m"),
            "currency": "CNY",
            "timezone": "Asia/Shanghai",
        },
    }


def waiting(client, with_intent=False):
    response = client.post(
        "/api/v1/agent-tasks",
        headers={**headers(), "Idempotency-Key": str(uuid4())},
        json=monthly_request(with_intent),
    )
    assert response.status_code == 202, response.text
    task = response.json()["data"]
    response = client.post(
        f"/api/v1/agent-tasks/{task['task_id']}/run",
        headers=headers(),
        json={"expected_version": task["version"]},
    )
    assert response.status_code == 200, response.text
    task = response.json()["data"]
    assert task["status"] == "waiting_confirmation", task
    assert task["confirmation"]["confirmation_scope"] == "PLAN_ACCEPTANCE"
    return task


def confirmation(task, decision="approve"):
    challenge = task["confirmation"]
    return {
        "expected_version": task["version"],
        "node_key": challenge["node_key"],
        "digest": challenge["digest"],
        "nonce": challenge["nonce"],
        "decision": decision,
    }


def test_monthly_http_persisted_advice_requires_final_confirmation(monthly_system):
    client, tools, repository, _admin = monthly_system
    task = waiting(client)
    view = task["confirmation"]["parameters_view"]
    assert view["monthly_goal"]["currency"] == "CNY"
    assert view["suggestions"] and tools.calls == ["billing"]
    with repository._transaction("1") as conn:
        row = conn.execute(
            "SELECT status,digest,parameters_view FROM public.agent_confirmations "
            "WHERE owner_id=%s AND task_id=%s",
            ("1", task["task_id"]),
        ).fetchone()
        assert row["status"] == "pending"
        assert row["digest"] == task["confirmation"]["digest"]
        assert row["parameters_view"] == view
    response = client.post(
        f"/api/v1/agent-tasks/{task['task_id']}/resume",
        headers=headers(),
        json=confirmation(task),
    )
    assert response.status_code == 200, response.text
    result = response.json()["data"]
    assert result["status"] == "succeeded"
    assert result["financial_execution_enabled"] is False
    assert any(
        o.get("result", {}).get("plan_accepted") is True for o in result["result"]["outputs"]
    )
    assert tools.calls == ["billing"]


@pytest.mark.parametrize("invalid", ["tenant", "digest", "nonce", "version", "confirmed"])
def test_monthly_http_invalid_confirmation_cannot_accept(monthly_system, invalid):
    client, tools, repository, _admin = monthly_system
    task = waiting(client)
    body = confirmation(task)
    actor = headers()
    if invalid == "tenant":
        actor = headers("user-b")
    elif invalid == "digest":
        body["digest"] = "0" * 64
    elif invalid == "nonce":
        body["nonce"] = "invalid-nonce-32-characters-000000"
    elif invalid == "version":
        body["expected_version"] -= 1
    else:
        body["confirmed"] = True
    response = client.post(
        f"/api/v1/agent-tasks/{task['task_id']}/resume", headers=actor, json=body
    )
    assert response.status_code == {"tenant": 404, "confirmed": 422}.get(invalid, 409)
    assert repository is not None and tools.calls == ["billing"]
    current = client.get(f"/api/v1/agent-tasks/{task['task_id']}", headers=headers()).json()[
        "data"
    ]
    assert current["status"] == "waiting_confirmation"


def test_monthly_http_rejection_never_prepares_intent(monthly_system):
    client, tools, _repository, _admin = monthly_system
    task = waiting(client, with_intent=True)
    assert tools.calls == ["billing"]
    response = client.post(
        f"/api/v1/agent-tasks/{task['task_id']}/resume",
        headers=headers(),
        json=confirmation(task, "reject"),
    )
    assert response.status_code == 200, response.text
    assert response.json()["data"]["status"] == "cancelled"
    assert tools.calls == ["billing"]


def test_monthly_http_acceptance_does_not_authorize_financial_execution(monthly_system):
    client, tools, _repository, _admin = monthly_system
    task = waiting(client, with_intent=True)
    response = client.post(
        f"/api/v1/agent-tasks/{task['task_id']}/resume",
        headers=headers(),
        json=confirmation(task),
    )
    assert response.status_code == 200, response.text
    result = response.json()["data"]
    assert result["status"] == "waiting_confirmation"
    assert result["confirmation"]["confirmation_scope"] == "PREPARE_ONLY"
    assert tools.calls == ["billing"]


def test_monthly_http_expired_confirmation_stays_pending(monthly_system):
    client, tools, _repository, admin_url = monthly_system
    task = waiting(client)
    with psycopg.connect(admin_url) as conn:
        conn.execute(
            "UPDATE public.agent_confirmations SET expires_at=clock_timestamp()-interval '1 second' "
            "WHERE owner_id=%s AND task_id=%s",
            ("1", task["task_id"]),
        )
    response = client.post(
        f"/api/v1/agent-tasks/{task['task_id']}/resume",
        headers=headers(),
        json=confirmation(task),
    )
    assert response.status_code == 409
    assert tools.calls == ["billing"]


def test_monthly_http_rollover_cannot_record_approval(monthly_system, monkeypatch):
    client, tools, repository, _admin = monthly_system
    task = waiting(client)
    monkeypatch.setattr("banking_agent.monthly_plan.current_period", lambda: "2099-01")
    response = client.post(
        f"/api/v1/agent-tasks/{task['task_id']}/resume",
        headers=headers(),
        json=confirmation(task),
    )
    assert response.status_code == 409, response.text
    with repository._transaction("1") as conn:
        row = conn.execute(
            "SELECT status FROM public.agent_confirmations WHERE owner_id=%s AND task_id=%s",
            ("1", task["task_id"]),
        ).fetchone()
        assert row["status"] == "pending"
    assert tools.calls == ["billing"]


def test_monthly_http_instruction_preserves_user_goal_in_review(monthly_system):
    client, tools, _repository, _admin = monthly_system
    body = monthly_request()
    body.pop("tasks")
    body["instruction"] = "This month I want to save CNY 2000; suggest a plan for my confirmation."
    response = client.post(
        "/api/v1/agent-tasks",
        headers={**headers(), "Idempotency-Key": str(uuid4())},
        json=body,
    )
    assert response.status_code == 202, response.text
    task = response.json()["data"]
    response = client.post(
        f"/api/v1/agent-tasks/{task['task_id']}/run",
        headers=headers(),
        json={"expected_version": task["version"]},
    )
    assert response.status_code == 200, response.text
    result = response.json()["data"]
    assert result["status"] == "waiting_confirmation"
    view = result["confirmation"]["parameters_view"]
    assert view["goal_text"] == body["instruction"]
    assert view["suggestions"]["billing"]["result"]["result"]["suggested_savings_minor"] == 200000
    assert tools.calls == ["billing"]


def test_monthly_http_plain_account_summary_is_not_a_plan(monthly_system):
    client, tools, _repository, _admin = monthly_system
    body = monthly_request()
    body["tasks"] = [spec().model_dump()]
    response = client.post(
        "/api/v1/agent-tasks",
        headers={**headers(), "Idempotency-Key": str(uuid4())},
        json=body,
    )
    assert response.status_code == 202, response.text
    task = response.json()["data"]
    response = client.post(
        f"/api/v1/agent-tasks/{task['task_id']}/run",
        headers=headers(), json={"expected_version": task["version"]},
    )
    assert response.status_code == 200, response.text
    result = response.json()["data"]
    assert result["status"] == "failed", result
    assert result["confirmation"] is None
    assert tools.calls == ["billing"]
