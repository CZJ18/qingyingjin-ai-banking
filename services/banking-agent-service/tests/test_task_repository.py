import importlib
import os
from concurrent.futures import ThreadPoolExecutor
from uuid import uuid4

import pytest


def test_task_repository_contract_exists():
    module = importlib.import_module("banking_agent.task_repository")
    assert all(
        hasattr(module.TaskRepository, method)
        for method in (
            "submit",
            "get",
            "claim",
            "finish",
            "cancel",
            "check_active",
            "call_get",
            "call_start",
            "call_finish",
            "call_fail",
            "challenge",
            "confirm",
            "approved",
            "decision",
            "events",
        )
    )


@pytest.fixture
def tasks():
    from banking_agent.migrate import apply_migrations
    from banking_agent.repository import Repository
    from banking_agent.task_repository import TaskRepository

    app = os.getenv("BANKING_AGENT_TEST_DATABASE_URL")
    admin = os.getenv("BANKING_AGENT_TEST_ADMIN_URL")
    if not app or not admin:
        pytest.skip("PostgreSQL integration credentials are not configured")
    apply_migrations(admin)
    repo = Repository(app)
    user = f"p5-test-{uuid4()}"
    repo.ensure_identity(user, None, "test")
    yield TaskRepository(repo), user
    repo.close()


def test_body_limit_and_json_contract():
    from banking_agent.errors import DomainError
    from banking_agent.task_repository import body

    assert body({"safe": 1}) == {"safe": 1}
    for invalid in ({"big": "x" * 1048576}, {"nan": float("nan")}, {"bad": object()}):
        with pytest.raises(DomainError):
            body(invalid)


def test_task_replay_lease_confirmation_and_tenant_isolation(tasks):
    from banking_agent.errors import DomainError

    repo, user = tasks
    task = repo.submit(user, "key", "digest", {"tool": "plan"}, "trace")
    tid = task["task_id"]
    assert repo.submit(user, "key", "digest", {}, "trace")["task_id"] == tid
    with pytest.raises(DomainError):
        repo.submit(user, "key", "changed", {}, "trace")
    with pytest.raises(DomainError) as error:
        repo.get("other-user", tid)
    assert error.value.status == 404
    lease = repo.claim(user, tid)
    with pytest.raises(DomainError):
        repo.claim(user, tid)
    challenge = repo.challenge(user, tid, lease, "write", "params", {"amount": 1})
    running = repo.get(user, tid)
    assert running["status"] == "running"
    with pytest.raises(DomainError):
        repo.confirm(
            user, tid, running["version"], "write", "params", challenge["nonce"], "approve"
        )
    repo.finish(user, tid, lease, "waiting_confirmation")
    waiting = repo.get(user, tid)
    assert waiting["status"] == "waiting_confirmation"
    with pytest.raises(DomainError):
        repo.confirm(user, tid, waiting["version"], "write", "params", "wrong", "approve")
    confirmed = repo.confirm(
        user, tid, waiting["version"], "write", "params", challenge["nonce"], "approve"
    )
    assert confirmed["status"] == "queued"
    assert repo.approved(user, tid, "write", "params")
    lease = repo.claim(user, tid)
    repo.call_start(user, tid, lease, "write", "params", "write")
    repo.call_finish(user, tid, "write", "params", {"ok": True})
    assert repo.call_get(user, tid, "write")["state"] == "succeeded"
    done = repo.finish(user, tid, lease, "succeeded", {"ok": True})
    assert done["version"] > task["version"]
    assert repo.events(user, tid)[0]["seq"] == 1
    with pytest.raises(DomainError):
        repo.cancel(user, tid, done["version"])


def test_cancel_fences_lease_and_tool_journal_conflicts(tasks):
    from banking_agent.errors import DomainError

    repo, user = tasks
    task = repo.submit(user, "cancel", "digest", {}, "trace")
    tid = task["task_id"]
    lease = repo.claim(user, tid)
    repo.call_start(user, tid, lease, "node", "digest", "advice")
    with pytest.raises(DomainError):
        repo.call_start(user, tid, lease, "node", "different", "advice")
    repo.call_fail(user, tid, "node", "digest", "AMBIGUOUS", "unknown")
    assert repo.call_get(user, tid, "node")["state"] == "unknown"
    cancelled = repo.cancel(user, tid, repo.get(user, tid)["version"])
    assert not repo.check_active(user, tid, lease)
    assert repo.finish(user, tid, lease, "succeeded")["status"] == "cancelled"
    with pytest.raises(DomainError):
        repo.claim(user, tid)
    assert repo.events(user, tid, after=1, limit=1)[0]["seq"] == 2
    assert cancelled["status"] == "cancelled"


def test_expired_lease_recovers_and_old_worker_is_fenced(tasks):
    from banking_agent.errors import DomainError

    repo, user = tasks
    tid = repo.submit(user, "expired", "digest", {}, "trace")["task_id"]
    old = repo.claim(user, tid)
    with repo._transaction(user) as conn:
        conn.execute(
            "UPDATE public.agent_tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE owner_id=%s AND task_id=%s",
            (user, tid),
        )
    current = repo.claim(user, tid)
    assert current != old
    with pytest.raises(DomainError):
        repo.finish(user, tid, old, "succeeded")
    assert repo.check_active(user, tid, current)


def test_tool_completion_requires_current_lease_even_for_completed_call(tasks):
    from banking_agent.errors import DomainError

    repo, user = tasks
    tid = repo.submit(user, "journal-fence", "digest", {}, "trace")["task_id"]
    old = repo.claim(user, tid)
    repo.call_start(user, tid, old, "node", "params", "read")
    with repo._transaction(user) as conn:
        conn.execute(
            "UPDATE public.agent_tasks SET lease_until=clock_timestamp()-interval '1 second' WHERE owner_id=%s AND task_id=%s",
            (user, tid),
        )
    current = repo.claim(user, tid)
    repo.call_finish(user, tid, "node", "params", {"worker": "current"}, lease_token=current)
    for complete in (
        lambda: repo.call_finish(user, tid, "node", "params", {"worker": "old"}, lease_token=old),
        lambda: repo.call_fail(user, tid, "node", "params", "OLD_FAILURE", lease_token=old),
    ):
        with pytest.raises(DomainError) as error:
            complete()
        assert error.value.code == "LEASE_CONFLICT"
    assert repo.call_get(user, tid, "node")["result"] == {"worker": "current"}
    repo.cancel(user, tid, repo.get(user, tid)["version"])
    with pytest.raises(DomainError):
        repo.call_finish(user, tid, "node", "params", {"worker": "cancelled"})


def test_confirmation_is_bound_expiring_and_single_decision(tasks):
    from banking_agent.errors import DomainError

    repo, user = tasks
    tid = repo.submit(user, "confirm", "digest", {}, "trace")["task_id"]
    lease = repo.claim(user, tid)
    challenge = repo.challenge(user, tid, lease, "node", "params", {"safe": 1})
    assert repo.challenge(user, tid, lease, "node", "params", {"safe": 1}) == challenge
    with pytest.raises(DomainError):
        repo.challenge(user, tid, lease, "node", "params", {"safe": 2})
    waiting = repo.finish(user, tid, lease, "waiting_confirmation")
    args = (user, tid, waiting["version"], "node", "params", challenge["nonce"])
    approved = repo.confirm(*args, "approve")
    assert repo.confirm(*args, "approve") == approved
    with pytest.raises(DomainError):
        repo.confirm(*args, "reject")


def test_approved_confirmation_expires_before_operation(tasks):
    import psycopg

    repo, user = tasks
    tid = repo.submit(user, "approval-expiry", "digest", {}, "trace")["task_id"]
    lease = repo.claim(user, tid)
    challenge = repo.challenge(user, tid, lease, "node", "params", {})
    waiting = repo.finish(user, tid, lease, "waiting_confirmation")
    repo.confirm(user, tid, waiting["version"], "node", "params", challenge["nonce"], "approve")
    assert repo.approved(user, tid, "node", "params")
    assert repo.decision(user, tid, "node", "params")["decision"] == "approve"
    # Expiry is immutable for the application role; only test administrator ages it.
    with psycopg.connect(os.environ["BANKING_AGENT_TEST_ADMIN_URL"]) as conn:
        conn.execute("SELECT set_config('app.user_id',%s,true)", (user,))
        conn.execute(
            "UPDATE public.agent_confirmations SET expires_at=clock_timestamp()-interval '1 second' WHERE owner_id=%s AND task_id=%s",
            (user, tid),
        )
    assert not repo.approved(user, tid, "node", "params")
    assert repo.decision(user, tid, "node", "params") is None


@pytest.mark.parametrize("decision", ["approve", "reject"])
def test_confirmed_decision_recovery_is_exact_and_tenant_bound(tasks, decision):
    from banking_agent.errors import DomainError

    repo, user = tasks
    tid = repo.submit(user, "decision-recovery", "digest", {}, "trace")["task_id"]
    lease = repo.claim(user, tid)
    challenge = repo.challenge(user, tid, lease, "node", "params", {})
    assert repo.decision(user, tid, "node", "params") is None
    waiting = repo.finish(user, tid, lease, "waiting_confirmation")
    repo.confirm(user, tid, waiting["version"], "node", "params", challenge["nonce"], decision)
    recovered = repo.decision(user, tid, "node", "params")
    assert recovered == {
        "decision": decision,
        "node_key": "node",
        "digest": "params",
        "expires_at": challenge["expires_at"],
    }
    assert repo.decision(user, tid, "node", "wrong-digest") is None
    assert repo.decision(user, tid, "other-node", "params") is None
    with pytest.raises(DomainError) as error:
        repo.decision("foreign-tenant", tid, "node", "params")
    assert error.value.status == 404


def test_concurrent_submit_and_claim_are_serialized(tasks):
    from banking_agent.errors import DomainError

    repo, user = tasks
    with ThreadPoolExecutor(max_workers=4) as executor:
        views = list(
            executor.map(lambda _: repo.submit(user, "concurrent", "digest", {}, "trace"), range(8))
        )
    tid = views[0]["task_id"]
    assert {view["task_id"] for view in views} == {tid}

    def claim(_):
        try:
            return repo.claim(user, tid)
        except DomainError as error:
            assert error.status == 409
            return None

    with ThreadPoolExecutor(max_workers=4) as executor:
        assert sum(token is not None for token in executor.map(claim, range(8))) == 1


def test_new_tables_force_tenant_rls_and_minimum_privileges(tasks):
    import psycopg

    repo, user = tasks
    tid = repo.submit(user, "rls", "digest", {"secret_input": "never_in_event"}, "trace")["task_id"]
    tables = [
        "agent_tasks",
        "agent_tool_calls",
        "agent_confirmations",
        "agent_task_events",
        "agent_checkpoints",
        "agent_checkpoint_writes",
    ]
    with repo.repository.pool.connection() as conn:
        for table in tables:
            assert conn.execute(f"SELECT * FROM public.{table}").fetchall() == []
            row = conn.execute(
                "SELECT relrowsecurity,relforcerowsecurity,has_table_privilege(current_user,oid,'DELETE') AS deletion,has_table_privilege(current_user,oid,'TRUNCATE') AS truncation FROM pg_class WHERE relname=%s",
                (table,),
            ).fetchone()
            assert row == {
                "relrowsecurity": True,
                "relforcerowsecurity": True,
                "deletion": False,
                "truncation": False,
            }
    with repo._transaction(user) as conn:
        for sql in (
            "DELETE FROM public.agent_tasks WHERE task_id=%s",
            "UPDATE public.agent_tasks SET owner_id='other' WHERE task_id=%s",
        ):
            with pytest.raises(psycopg.errors.InsufficientPrivilege):
                with conn.transaction():
                    conn.execute(sql, (tid,))
    assert "never_in_event" not in str(repo.events(user, tid))
