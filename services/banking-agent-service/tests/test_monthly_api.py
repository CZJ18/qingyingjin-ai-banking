"""HTTP and public challenge contracts preserve the server confirmation scope."""

import copy
from datetime import UTC, datetime

import pytest
from fastapi.testclient import TestClient
from pydantic import ValidationError
from test_api import Bank, Cache, Store, headers
from test_monthly_plan import metadata, monthly_request

from banking_agent.agent_routes import PreparationChallenge
from banking_agent.config import Settings
from banking_agent.main import create_app
from banking_agent.task_repository import TaskRepository


def test_public_scope_is_derived_from_persisted_parameters():
    row = {
        key: None
        for key in (
            "task_id",
            "owner_engine",
            "status",
            "version",
            "request",
            "result",
            "error_code",
        )
    }
    row.update(owner_id="1", created_at=datetime.now(UTC), updated_at=datetime.now(UTC))

    class Result:
        def fetchone(self):
            return {
                "node_key": "monthly_plan_review",
                "digest": "a" * 64,
                "nonce": "test-challenge-nonce",
                "status": "pending",
                "expires_at": datetime.now(UTC),
                "parameters_view": {"confirmation_scope": "PLAN_ACCEPTANCE"},
            }

    class Connection:
        def execute(self, *_):
            return Result()

    view = TaskRepository._view(Connection(), row)
    assert view["confirmation"]["confirmation_scope"] == "PLAN_ACCEPTANCE"
    parsed = PreparationChallenge.model_validate(view["confirmation"])
    assert parsed.confirmation_scope == "PLAN_ACCEPTANCE"
    invalid = view["confirmation"] | {"confirmation_scope": "EXECUTE_MONEY"}
    with pytest.raises(ValidationError):
        PreparationChallenge.model_validate(invalid)


def test_http_schema_and_submission_validate_monthly_metadata_before_storage():
    settings = Settings(
        "postgresql://app:test@localhost/test", "redis://:test@localhost/0", "http://bank.invalid"
    )
    with TestClient(
        create_app(settings, banking=Bank(), repository=Store(), cache=Cache())
    ) as client:
        schema = client.get("/openapi.json").json()["components"]["schemas"]
        assert "monthly_goal" in schema["AgentSubmit"]["properties"]
        assert set(schema["PreparationChallenge"]["properties"]["confirmation_scope"]["enum"]) == {
            "PREPARE_ONLY",
            "PLAN_ACCEPTANCE",
        }
        response = client.post(
            "/api/v1/agent-tasks",
            headers=headers(),
            json=monthly_request(monthly_goal=metadata())
            | {"monthly_goal": metadata(period="2000-01")},
        )
        assert response.status_code == 422


def test_persisted_historical_monthly_request_remains_readable():
    settings = Settings(
        "postgresql://app:test@localhost/test", "redis://:test@localhost/0", "http://bank.invalid"
    )
    request = monthly_request()
    request["monthly_goal"]["period"] = "2000-01"
    task_id = "lg-00000000-0000-0000-0000-000000000000"
    app = create_app(settings, banking=Bank(), repository=Store(), cache=Cache())

    class Tasks:
        def get(self, user, tid):
            assert user == "1" and tid == task_id
            return {
                "task_id": tid,
                "owner_engine": "langgraph",
                "status": "failed",
                "version": 3,
                "request": copy.deepcopy(request),
                "result": None,
                "error_code": "MONTHLY_GOAL_EXPIRED",
                "confirmation": None,
                "created_at": "2000-01-01T00:00:00Z",
                "updated_at": "2000-01-01T00:00:00Z",
            }

    with TestClient(app) as client:
        app.state.tasks = Tasks()
        response = client.get(f"/api/v1/agent-tasks/{task_id}", headers=headers())
        assert response.status_code == 200
        assert response.json()["data"]["request"]["monthly_goal"]["period"] == "2000-01"


def test_ordinary_submission_preserves_legacy_request_digest_shape():
    from test_agent_models import spec

    from banking_agent.agent_graph import digest

    settings = Settings(
        "postgresql://app:test@localhost/test", "redis://:test@localhost/0", "http://bank.invalid"
    )
    app = create_app(settings, banking=Bank(), repository=Store(), cache=Cache())
    saved = []

    class Tasks:
        def submit(self, user, key, request_digest, request, trace):
            assert "monthly_goal" not in request
            assert request_digest == digest(request)
            saved.append(request)
            return {
                "task_id": "lg-00000000-0000-0000-0000-000000000000",
                "owner_engine": "langgraph",
                "status": "queued",
                "version": 1,
                "request": request,
                "result": None,
                "error_code": "",
                "confirmation": None,
                "created_at": "2026-10-01T00:00:00Z",
                "updated_at": "2026-10-01T00:00:00Z",
            }

    with TestClient(app) as client:
        app.state.tasks = Tasks()
        response = client.post(
            "/api/v1/agent-tasks", headers=headers(), json={"tasks": [spec().model_dump()]}
        )
        assert response.status_code == 202
        assert saved and "monthly_goal" not in saved[0]
