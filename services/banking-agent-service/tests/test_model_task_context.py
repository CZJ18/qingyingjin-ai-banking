import hashlib
import os
from uuid import uuid4

import pytest


@pytest.fixture
def active_task():
    from banking_agent.repository import Repository
    from banking_agent.task_repository import TaskRepository

    url = os.getenv("BANKING_AGENT_TEST_DATABASE_URL")
    if not url:
        pytest.skip("isolated PostgreSQL fixture required")
    repository = Repository(url)
    tasks = TaskRepository(repository)
    user = str(uuid4().int % 10**17 + 1)
    repository.ensure_identity(user, None, "model-task-test")
    task = tasks.submit(user, uuid4().hex, "a" * 64, {}, "model-task-test")["task_id"]
    lease = tasks.claim(user, task)
    tasks.call_start(user, task, lease, "billing", "node-digest", "read_only")
    yield tasks, user, task, lease
    current = tasks.get(user, task)
    if current["status"] not in ("succeeded", "cancelled"):
        tasks.cancel(user, task, current["version"])
    repository.close()


def test_model_task_context_is_active_node_and_lease_bound(active_task):
    from banking_agent.errors import DomainError

    tasks, user, task, lease = active_task
    context = tasks.model_context(user, task + ".billing")
    assert context == {
        "owner_id": user,
        "task_id": task + ".billing",
        "request_digest": "a" * 64,
        "version": 2,
        "lease_fingerprint": hashlib.sha256(lease.encode()).hexdigest(),
    }
    assert lease not in str(context)
    with pytest.raises(DomainError) as error:
        tasks.model_context("other", task + ".billing")
    assert error.value.status == 404
    with pytest.raises(DomainError) as error:
        tasks.model_context(user, task + ".missing")
    assert error.value.status == 409
    tasks.call_finish(user, task, "billing", "node-digest", {}, lease_token=lease)
    with pytest.raises(DomainError) as error:
        tasks.model_context(user, task + ".billing")
    assert error.value.status == 409


def test_model_task_context_rejects_cancel_expiry_and_binds_reclaim(active_task):
    from banking_agent.errors import DomainError

    tasks, user, task, old = active_task
    first = tasks.model_context(user, task + ".billing")
    with tasks._transaction(user) as conn:
        conn.execute(
            "UPDATE public.agent_tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE owner_id=%s AND task_id=%s",
            (user, task),
        )
    with pytest.raises(DomainError) as error:
        tasks.model_context(user, task + ".billing")
    assert error.value.status == 409
    new = tasks.claim(user, task)
    second = tasks.model_context(user, task + ".billing")
    assert old != new and first["lease_fingerprint"] != second["lease_fingerprint"]
    assert first["version"] != second["version"]
    tasks.cancel(user, task, second["version"])
    with pytest.raises(DomainError) as error:
        tasks.model_context(user, task + ".billing")
    assert error.value.status == 409


def test_model_task_context_maps_only_exact_supervisor_journal_node(active_task):
    from banking_agent.errors import DomainError

    tasks, user, task, lease = active_task
    with pytest.raises(DomainError):
        tasks.model_context(user, task + ".supervisor_plan")
    tasks.call_start(
        user, task, lease, "__internal:supervisor_plan", "supervisor-digest", "read_only"
    )
    assert (
        tasks.model_context(user, task + ".supervisor_plan")["task_id"] == task + ".supervisor_plan"
    )


def test_internal_endpoint_requires_service_key_and_never_exposes_lease(active_task):
    from fastapi.testclient import TestClient
    from test_api import Bank, Cache

    from banking_agent.config import Settings
    from banking_agent.main import create_app

    tasks, user, task, lease = active_task
    key = "internal-model-task-fixture-" + "x" * 32
    settings = Settings(
        os.environ["BANKING_AGENT_TEST_DATABASE_URL"],
        "redis://:fixture@localhost/0",
        "http://fixture.invalid",
        agent_service_key=key,
    )
    with TestClient(
        create_app(
            settings,
            banking=Bank(),
            repository=tasks.repository,
            cache=Cache(),
            task_repository=tasks,
        )
    ) as client:
        body = {"owner_id": user, "task_id": task + ".billing"}
        for headers in ({}, {"X-Banking-Agent-Key": "invalid"}, {"Authorization": "Bearer user-a"}):
            assert (
                client.post("/internal/model-task-context", json=body, headers=headers).status_code
                == 401
            )
        response = client.post(
            "/internal/model-task-context", json=body, headers={"X-Banking-Agent-Key": key}
        )
        assert response.status_code == 200
        assert response.json()["data"]["owner_id"] == user
        assert lease not in response.text and key not in response.text
        wrong = client.post(
            "/internal/model-task-context",
            json={**body, "owner_id": "2"},
            headers={"X-Banking-Agent-Key": key},
        )
        assert wrong.status_code == 404
        for invalid_task in (
            task,
            task + ".a.extra",
            task.upper() + ".billing",
            task + ".__internal:supervisor_plan",
        ):
            invalid = client.post(
                "/internal/model-task-context",
                json={**body, "task_id": invalid_task},
                headers={"X-Banking-Agent-Key": key},
            )
            assert invalid.status_code == 422


def test_known_failed_read_restarts_journal_under_current_lease(active_task):
    tasks, user, task, lease = active_task
    tasks.call_fail(
        user, task, "billing", "node-digest", "DEPENDENCY_UNAVAILABLE", lease_token=lease
    )
    restarted = tasks.call_start(user, task, lease, "billing", "node-digest", "read_only")
    assert restarted["state"] == "running"
    assert tasks.model_context(user, task + ".billing")["version"] == 2


@pytest.mark.parametrize(
    "mode,state,error,node",
    [
        ("read_only", "unknown", "DEPENDENCY_UNAVAILABLE", "billing"),
        ("advice", "failed", "DEPENDENCY_UNAVAILABLE", "mutation"),
        ("intent", "failed", "DEPENDENCY_UNAVAILABLE", "prepare"),
        ("read_only", "failed", "COZE_UNAVAILABLE", "model"),
        ("read_only", "failed", "DEPENDENCY_UNAVAILABLE", "__internal:supervisor_plan"),
    ],
)
def test_retry_start_never_rearms_unknown_model_or_mutating_call(
    active_task, mode, state, error, node
):
    tasks, user, task, lease = active_task
    tasks.call_start(user, task, lease, node, "node-digest", mode)
    tasks.call_fail(user, task, node, "node-digest", error, state, lease_token=lease)
    assert tasks.call_start(user, task, lease, node, "node-digest", mode)["state"] == state


@pytest.mark.asyncio
async def test_inner_read_retry_exposes_running_proof_for_each_attempt(active_task):
    from banking_agent.agent_graph import RunContext, journal_call
    from banking_agent.errors import DomainError

    tasks, user, task, lease = active_task
    context = RunContext(user, task, lease, "user-token", "proof-retry", tasks, None, 2)
    attempts = 0

    async def operation():
        nonlocal attempts
        # The real task proof used by Go must succeed before BOTH attempts.
        tasks.model_context(user, task + ".retry_read")
        attempts += 1
        if attempts == 1:
            raise DomainError("DEPENDENCY_UNAVAILABLE", "explicit transient fixture", 503)
        return {"ok": True}

    result = await journal_call(context, "retry_read", {}, "read_only", operation, idempotent=True)
    assert result == {"ok": True} and attempts == 2
