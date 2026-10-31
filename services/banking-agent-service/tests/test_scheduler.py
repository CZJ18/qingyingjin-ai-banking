import asyncio
import os
from concurrent.futures import ThreadPoolExecutor
from uuid import uuid4

import pytest


@pytest.fixture
def durable():
    from banking_agent.repository import Repository
    from banking_agent.task_repository import TaskRepository

    app = os.getenv("BANKING_AGENT_TEST_DATABASE_URL")
    if not app:
        pytest.skip("isolated PostgreSQL fixture required")
    repository = Repository(app)
    user = "scheduler-" + uuid4().hex
    repository.ensure_identity(user, None, "scheduler-test")
    yield TaskRepository(repository), user
    tasks = TaskRepository(repository)
    with repository._transaction(user) as conn:
        rows = conn.execute(
            "SELECT task_id FROM public.agent_tasks WHERE owner_id=%s", (user,)
        ).fetchall()
    for row in rows:
        view = tasks.get(user, row["task_id"])
        if view["status"] not in ("succeeded", "cancelled"):
            tasks.cancel(user, row["task_id"], view["version"])
    repository.close()


def enrolled(repo, user, key=None):
    from test_agent_models import spec

    from banking_agent.agent_graph import digest
    from banking_agent.agent_models import AgentSubmit

    request = AgentSubmit(tasks=[spec()]).model_dump()
    task = repo.submit(user, key or uuid4().hex, digest(request), request, "scheduler-test")
    repo.enroll(user, task["task_id"], digest(request), "bgt1.isolated-test-fixture", 86400)
    return task["task_id"]


def age(repo, user, task):
    with repo._transaction(user) as conn:
        conn.execute(
            "UPDATE public.agent_tasks SET lease_until=clock_timestamp()-interval '1 second',next_run_at=clock_timestamp()-interval '1 second' WHERE owner_id=%s AND task_id=%s",
            (user, task),
        )


def test_sql_competing_claims_and_expired_lease_recovery(durable):
    repo, user = durable
    task = enrolled(repo, user)
    with ThreadPoolExecutor(max_workers=8) as executor:
        claims = list(executor.map(lambda _: repo.background_claim(user, task), range(8)))
    assert sum(claim is not None for claim in claims) == 1
    old = next(claim["lease"] for claim in claims if claim)
    age(repo, user, task)
    assert {"owner_id": user, "task_id": task} in repo.candidates()
    current = repo.background_claim(user, task)["lease"]
    assert old != current
    assert not repo.renew(user, task, old)
    assert repo.renew(user, task, current)
    repo.finish(user, task, current, "succeeded")


@pytest.mark.parametrize(
    "mode,state", [("read_only", "running"), ("advice", "unknown"), ("intent", "running")]
)
def test_sql_ambiguous_inflight_call_is_never_replayed(durable, mode, state):
    repo, user = durable
    task = enrolled(repo, user)
    lease = repo.background_claim(user, task)["lease"]
    repo.call_start(user, task, lease, "paid-or-tool", "digest", mode)
    if state == "unknown":
        repo.call_fail(
            user,
            task,
            "paid-or-tool",
            "digest",
            "MODEL_OUTCOME_UNKNOWN",
            "unknown",
            lease_token=lease,
        )
    age(repo, user, task)
    assert repo.background_claim(user, task) is None
    assert repo.get(user, task)["status"] == "interrupted"
    assert repo.get(user, task)["error_code"] == "TOOL_OUTCOME_UNKNOWN"
    assert {"owner_id": user, "task_id": task} not in repo.candidates()


def test_sql_retry_backoff_has_three_total_attempts(durable):
    repo, user = durable
    task = enrolled(repo, user)
    for attempt in range(1, 4):
        claim = repo.background_claim(user, task)
        assert claim is not None
        repo.call_start(user, task, claim["lease"], "read", "digest", "read_only")
        repo.call_fail(
            user, task, "read", "digest", "DEPENDENCY_UNAVAILABLE", lease_token=claim["lease"]
        )
        finished = repo.finish(
            user, task, claim["lease"], "failed", error_code="DEPENDENCY_UNAVAILABLE"
        )
        # Failure and retry scheduling must commit together; a worker crash
        # between these two operations must not strand a retryable task.
        assert finished["status"] == ("queued" if attempt < 3 else "failed")
        if attempt < 3:
            assert repo.background_claim(user, task) is None
            age(repo, user, task)
    assert repo.get(user, task)["status"] == "failed"
    assert {"owner_id": user, "task_id": task} not in repo.candidates()


def test_sql_last_expired_lease_is_terminal_and_does_not_strand_running_task(durable):
    repo, user = durable
    task = enrolled(repo, user)
    for _ in range(3):
        assert repo.background_claim(user, task) is not None
        age(repo, user, task)
    assert repo.background_claim(user, task) is None
    assert repo.get(user, task)["error_code"] == "BACKGROUND_RETRIES_EXHAUSTED"
    assert repo.get(user, task)["status"] == "failed"


def test_sql_expired_delegation_fails_closed(durable):
    import psycopg

    repo, user = durable
    task = enrolled(repo, user)
    with psycopg.connect(os.environ["BANKING_AGENT_TEST_ADMIN_URL"]) as conn:
        conn.execute(
            "UPDATE public.agent_background_credentials SET expires_at=clock_timestamp()-interval '1 second' WHERE owner_id=%s AND task_id=%s",
            (user, task),
        )
    assert repo.background_claim(user, task) is None
    assert repo.get(user, task)["error_code"] == "BACKGROUND_DELEGATION_EXPIRED"


@pytest.mark.asyncio
async def test_sql_heartbeat_keeps_short_lease_alive_during_long_tool(durable):
    from test_agent_graph import Tools

    from banking_agent.agent_graph import LangGraphOrchestrator

    repo, user = durable
    task = enrolled(repo, user)
    claim = repo.background_claim(user, task, 1)

    class SlowTools(Tools):
        async def execute(self, *args):
            await asyncio.sleep(1.5)
            return await super().execute(*args)

    result = await LangGraphOrchestrator(repo, SlowTools()).run(
        user,
        task,
        claim["capability"],
        claim["trace_id"],
        lease=claim["lease"],
        lease_seconds=1,
    )
    assert result["status"] == "succeeded"


@pytest.mark.parametrize(
    "code",
    [
        "MODEL_OUTCOME_UNKNOWN",
        "COZE_UNAVAILABLE",
        "TOOL_TIMEOUT",
        "TOOL_OUTPUT_INVALID",
        "FORBIDDEN",
    ],
)
def test_sql_unknown_model_and_permanent_failures_do_not_retry(durable, code):
    repo, user = durable
    task = enrolled(repo, user)
    claim = repo.background_claim(user, task)
    repo.finish(user, task, claim["lease"], "failed", error_code=code)
    assert not repo.retry_background(user, task)


def test_sql_scheduler_metadata_cannot_read_credentials_or_tenant_payload(durable):
    import psycopg

    from banking_agent.errors import DomainError

    repo, user = durable
    task = enrolled(repo, user)
    with pytest.raises(DomainError):
        repo.enroll("other", task, "digest", "bgt1.isolated-test-fixture", 60)
    with repo.repository.pool.connection() as conn:
        assert conn.execute("SELECT * FROM public.agent_background_credentials").fetchall() == []
        assert not conn.execute(
            "SELECT pg_has_role(current_user,'banking_agent_scheduler','MEMBER') AS member"
        ).fetchone()["member"]
    with psycopg.connect(os.environ["BANKING_AGENT_TEST_ADMIN_URL"]) as conn:
        conn.execute("SET LOCAL ROLE banking_agent_scheduler")
        for column in ("request", "result"):
            with pytest.raises(psycopg.errors.InsufficientPrivilege), conn.transaction():
                conn.execute(f"SELECT {column} FROM public.agent_tasks")
        with pytest.raises(psycopg.errors.InsufficientPrivilege), conn.transaction():
            conn.execute("SELECT capability FROM public.agent_background_credentials")
    assert "bgt1" not in str(repo.get(user, task)) + str(repo.events(user, task))


@pytest.mark.asyncio
async def test_sql_worker_executes_persisted_graph_after_repository_restart(durable):
    from test_agent_graph import Tools

    from banking_agent.agent_graph import LangGraphOrchestrator
    from banking_agent.repository import Repository
    from banking_agent.scheduler import BackgroundScheduler
    from banking_agent.task_repository import TaskRepository

    original, user = durable
    task = enrolled(original, user)
    restarted = Repository(os.environ["BANKING_AGENT_TEST_DATABASE_URL"])
    tasks = TaskRepository(restarted)

    class TaskTools(Tools):
        async def execute(self, token, owner, task_id, item, trace):
            self.task_ids = getattr(self, "task_ids", []) + [task_id]
            return await super().execute(token, owner, task_id, item, trace)

    tools = TaskTools()  # Explicit deterministic tool fixture; SQL graph/checkpoints are real.
    worker = BackgroundScheduler(tasks, LangGraphOrchestrator(tasks, tools), poll_seconds=0.02)
    worker.start()
    try:
        async with asyncio.timeout(5):
            while original.get(user, task)["status"] not in ("succeeded", "failed"):
                await asyncio.sleep(0.02)
        assert original.get(user, task)["status"] == "succeeded"
        assert tools.task_ids.count(task) == 1
        with original._transaction(user) as conn:
            assert (
                conn.execute(
                    "SELECT count(*) AS n FROM public.agent_checkpoints WHERE owner_id=%s AND task_id=%s",
                    (user, task),
                ).fetchone()["n"]
                > 0
            )
    finally:
        await worker.stop()
        restarted.close()


def test_durable_scheduler_repository_contract():
    from banking_agent.task_repository import TaskRepository

    for method in ("enroll", "candidates", "background_claim", "renew", "retry_background"):
        assert hasattr(TaskRepository, method), method


@pytest.mark.asyncio
async def test_scheduler_claims_before_execution_and_stops_cleanly():
    from banking_agent.scheduler import BackgroundScheduler

    class Tasks:
        def candidates(self):
            return [{"owner_id": "1", "task_id": "lg-test"}]

        def background_claim(self, owner, task, lease_seconds):
            self.claimed = True
            return {"lease": "lease", "capability": "opaque", "trace_id": "trace"}

        def retry_background(self, *args):
            return False

    class Graph:
        async def run(self, *args, **kw):
            assert tasks.claimed
            assert args[2] == "opaque"
            assert kw["lease"] == "lease"
            done.set()
            return {"status": "succeeded"}

    tasks, done = Tasks(), asyncio.Event()
    worker = BackgroundScheduler(tasks, Graph(), poll_seconds=0.01)
    worker.start()
    await asyncio.wait_for(done.wait(), 1)
    await worker.stop()
    assert worker.task.done()


@pytest.mark.asyncio
async def test_model_transport_failure_is_unknown_and_never_replayed():
    from test_agent_graph import system
    from test_agent_models import spec

    from banking_agent.agent_models import AgentSubmit

    graph, store, tools, _ = system(AgentSubmit(tasks=[spec(use_coze=True)]).model_dump())
    tools.fail = 1
    await graph.run("1", store.task["task_id"], "user-token", "trace")
    assert tools.called == ["billing"]
    assert store.calls["billing"]["state"] == "unknown"
    await graph.run("1", store.task["task_id"], "user-token", "trace")
    assert tools.called == ["billing"]
    assert store.task["error_code"] == "TOOL_OUTCOME_UNKNOWN"


def test_worker_enabled_http_run_acknowledges_queue_without_competing_execution(durable):
    import time

    from fastapi.testclient import TestClient
    from test_agent_models import spec
    from test_api import Cache

    from banking_agent.banking import Principal
    from banking_agent.config import Settings
    from banking_agent.main import create_app

    tasks, user = durable

    class Bank:
        async def authenticate(self, *_):
            return Principal(user)

        async def ready(self):
            return True

    class Tools:
        issued = 0

        async def background_delegation(self, token, task, request_digest, trace):
            assert token == "user-plaintext-fixture"
            self.issued += 1
            return {"capability": "bgt1.explicit-http-fixture", "expires_in_seconds": 86400}

    tools = Tools()
    settings = Settings(
        os.environ["BANKING_AGENT_TEST_DATABASE_URL"],
        "redis://:fixture@localhost/0",
        "http://fixture.invalid",
        agent_service_key="x" * 64,
        background_worker_enabled=True,
        background_poll_seconds=60,
    )
    app = create_app(
        settings,
        banking=Bank(),
        repository=tasks.repository,
        cache=Cache(),
        task_repository=tasks,
        agent_tools=tools,
    )
    with TestClient(app) as client:
        # Let the initial empty scan complete; this test targets the API/queue race.
        time.sleep(0.1)
        headers = {"Authorization": "Bearer user-plaintext-fixture", "Idempotency-Key": uuid4().hex}
        submitted = client.post(
            "/api/v1/agent-tasks", headers=headers, json={"tasks": [spec().model_dump()]}
        )
        assert submitted.status_code == 202
        task = submitted.json()["data"]
        assert task["status"] == "queued"
        path = "/api/v1/agent-tasks/" + task["task_id"] + "/run"
        stale = client.post(path, headers=headers, json={"expected_version": task["version"] + 1})
        assert stale.status_code == 200 and stale.json()["data"]["status"] == "queued"
        assert tools.issued == 1  # A stale request performs no credential write.
        response = client.post(path, headers=headers, json={"expected_version": task["version"]})
        assert response.status_code == 200 and response.json()["data"]["status"] == "queued"
        assert tools.issued == 2
        with tasks._transaction(user) as conn:
            assert (
                conn.execute(
                    "SELECT count(*) AS n FROM public.agent_tool_calls WHERE owner_id=%s AND task_id=%s",
                    (user, task["task_id"]),
                ).fetchone()["n"]
                == 0
            )
        assert client.get("/readyz").json()["data"]["dependencies"]["background_worker"] == "ready"
