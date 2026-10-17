"""Repository tests.

Run real PostgreSQL checks with BANKING_AGENT_TEST_DATABASE_URL (the restricted
banking_agent_app login) and BANKING_AGENT_TEST_ADMIN_URL (migration owner).
Without both URLs integration tests skip; deterministic contract checks still run.
Use a disposable database: the integration fixture applies the migration and
creates synthetic tenant records, intentionally never deleting through the app.
"""

from __future__ import annotations

import importlib
import os
from concurrent.futures import ThreadPoolExecutor
from types import SimpleNamespace
from uuid import uuid4

import pytest


class Database(tuple):
    """Hide connection credentials from pytest's failure-context formatting."""

    def __repr__(self):
        return "<Database(runtime_role=banking_agent_app)>"


def projection(version=1, *, source_id="account-1", kind="account", amount=100):
    return SimpleNamespace(
        kind=kind,
        source_id=source_id,
        source_version=version,
        payload={"balance_minor": amount, "currency": "CNY"},
        evidence=[{"source": "banking-service", "verified": True}],
        audit_id="audit-1",
    )


def test_digest_ignores_object_key_order_but_tracks_evidence_and_audit():
    repository = importlib.import_module("banking_agent.repository")
    first = projection()
    second = projection()
    second.payload = {"currency": "CNY", "balance_minor": 100}
    assert repository.projection_digest(first) == repository.projection_digest(second)
    second.evidence = [{"source": "other", "verified": True}]
    assert repository.projection_digest(first) != repository.projection_digest(second)
    second.evidence = first.evidence
    second.audit_id = "audit-2"
    assert repository.projection_digest(first) != repository.projection_digest(second)


@pytest.mark.parametrize("version", [0, -1, True, 1.5, "1"])
def test_projection_rejects_nonpositive_or_noninteger_versions(version):
    repository = importlib.import_module("banking_agent.repository")
    from banking_agent.errors import DomainError

    with pytest.raises(DomainError) as error:
        repository.projection_digest(projection(version))
    assert error.value.code == "INVALID_PROJECTION"


def test_batch_selects_latest_unique_resource_and_detects_same_version_changes():
    repository = importlib.import_module("banking_agent.repository")
    from banking_agent.errors import DomainError

    batch, duplicates, stale = repository.prepare_batch(
        [projection(2), projection(1), projection(2)]
    )
    assert len(batch) == 1
    assert batch[0].source_version == 2
    assert (duplicates, stale) == (1, 1)
    with pytest.raises(DomainError) as error:
        repository.prepare_batch([projection(2), projection(2, amount=200)])
    assert error.value.code == "SOURCE_VERSION_CONFLICT"
    assert error.value.status == 409


def test_projection_contract_accepts_evidence_array_and_rejects_object():
    repository = importlib.import_module("banking_agent.repository")
    from banking_agent.errors import DomainError

    item = projection()
    item.evidence = []
    assert repository.projection_digest(item)
    for evidence in [{"source": "banking-service"}, ["source"]]:
        item.evidence = evidence
        with pytest.raises(DomainError) as error:
            repository.projection_digest(item)
        assert error.value.code == "INVALID_PROJECTION"


@pytest.fixture(scope="module")
def database():
    app_url = os.environ.get("BANKING_AGENT_TEST_DATABASE_URL")
    admin_url = os.environ.get("BANKING_AGENT_TEST_ADMIN_URL")
    if not app_url or not admin_url:
        pytest.skip("Set both BANKING_AGENT_TEST_DATABASE_URL and BANKING_AGENT_TEST_ADMIN_URL")
    from banking_agent.migrate import apply_migrations
    from banking_agent.repository import Repository

    apply_migrations(admin_url)
    repository = Repository(app_url)
    assert repository.ready(), "Integration URL must use the restricted nonowner app role"
    yield Database((repository, app_url, admin_url))
    repository.close()


@pytest.fixture
def tenant(database):
    repository, _, _ = database
    user_id = f"integration-{uuid4()}"
    repository.ensure_identity(user_id, f"coze-{uuid4()}", "test-identity")
    return repository, user_id


@pytest.mark.integration
def test_runtime_rls_denies_unset_context_foreign_insert_and_foreign_reads(database, tenant):
    import psycopg
    from psycopg.rows import dict_row

    repository, app_url, _ = database
    _, first = tenant
    second = f"integration-{uuid4()}"
    repository.ensure_identity(second, f"coze-{uuid4()}", "identity")
    repository.apply(first, "event-1", "digest-1", [projection()], "trace")
    repository.apply(second, "event-1", "digest-1", [projection()], "trace")
    with psycopg.connect(app_url, row_factory=dict_row) as connection:
        role = connection.execute(
            "SELECT rolsuper, rolbypassrls FROM pg_roles WHERE rolname=current_user"
        ).fetchone()
        assert role == {"rolsuper": False, "rolbypassrls": False}
        assert connection.execute("SELECT * FROM public.agent_projection_resources").fetchall() == []
        connection.execute("SELECT set_config('app.user_id', %s, true)", (first,))
        rows = connection.execute("SELECT owner_id FROM public.agent_projection_resources").fetchall()
        assert rows and all(row["owner_id"] == first for row in rows)
        with pytest.raises(psycopg.errors.InsufficientPrivilege):
            with connection.transaction():
                foreign_user_id = f"foreign-{uuid4()}"
                connection.execute(
                    "INSERT INTO public.agent_identities(owner_id, banking_user_id) VALUES (%s,%s)",
                    (foreign_user_id, foreign_user_id),
                )
        ownership = connection.execute(
            "SELECT count(*) AS n FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace "
            "WHERE n.nspname='public' AND c.relname LIKE 'agent_%' "
            "AND c.relowner=(SELECT oid FROM pg_roles WHERE rolname=current_user)"
        ).fetchone()
        assert ownership["n"] == 0


@pytest.mark.integration
def test_runtime_cannot_delete_truncate_create_or_change_identity_owner(database, tenant):
    import psycopg

    _, app_url, _ = database
    _, user_id = tenant
    with psycopg.connect(app_url) as connection:
        connection.execute("SELECT set_config('app.user_id', %s, true)", (user_id,))
        attempts = [
            ("DELETE FROM public.agent_identities WHERE owner_id=%s", (user_id,)),
            ("TRUNCATE public.agent_projection_resources", ()),
            ("CREATE TABLE public.agent_forbidden_runtime_ddl (id integer)", ()),
            ("UPDATE public.agent_identities SET banking_user_id=%s WHERE owner_id=%s", (user_id, user_id)),
        ]
        for statement, parameters in attempts:
            with pytest.raises(psycopg.errors.InsufficientPrivilege):
                with connection.transaction():
                    connection.execute(statement, parameters)


@pytest.mark.integration
def test_pool_clears_tenant_context_and_cross_owner_resource_is_missing(database, tenant):
    repository, _, _ = database
    _, first = tenant
    second = f"integration-{uuid4()}"
    repository.ensure_identity(second, f"coze-{uuid4()}", "identity")
    repository.apply(first, "event-1", "digest-1", [projection()], "trace")
    assert repository.query(first, "account", "account-1")
    assert repository.query(second, "account", "account-1") == []
    assert repository.receipt(second, "event-1", "digest-1") is None
    with repository.pool.connection() as connection:
        assert connection.execute("SELECT * FROM public.agent_projection_resources").fetchall() == []
        assert connection.execute("SELECT current_setting('app.user_id', true) AS user_id").fetchone()["user_id"] in (None, "")


@pytest.mark.integration
def test_concurrent_event_replays_once_and_rejects_changed_digest(tenant):
    from banking_agent.errors import DomainError

    repository, user_id = tenant
    with ThreadPoolExecutor(max_workers=6) as executor:
        results = list(executor.map(
            lambda _: repository.apply(user_id, "event-1", "digest-1", [projection()], "trace"),
            range(12),
        ))
    assert all(result == results[0] for result in results)
    assert results[0]["applied"] == 1
    assert len(repository.query(user_id, "account")) == 1
    assert repository.event(user_id, "event-1") == results[0]
    with pytest.raises(DomainError) as error:
        repository.apply(user_id, "event-1", "changed", [projection()], "trace")
    assert error.value.code == "IDEMPOTENCY_CONFLICT"


@pytest.mark.integration
def test_versions_ignore_stale_accept_equal_and_roll_back_entire_conflict(tenant):
    from banking_agent.errors import DomainError

    repository, user_id = tenant
    repository.apply(user_id, "initial", "initial", [projection(2)], "trace")
    stale = repository.apply(user_id, "older", "older", [projection(1)], "trace")
    assert (stale["applied"], stale["stale"]) == (0, 1)
    equal = repository.apply(user_id, "equal", "equal", [projection(2)], "trace")
    assert equal["duplicates"] == 1
    with pytest.raises(DomainError) as error:
        repository.apply(
            user_id, "conflict", "conflict",
            [projection(1, source_id="new-account"), projection(2, amount=999)], "trace",
        )
    assert error.value.code == "SOURCE_VERSION_CONFLICT"
    assert repository.query(user_id, "account", "new-account") == []
    assert repository.event(user_id, "conflict") is None
    newer = repository.apply(user_id, "newer", "newer", [projection(3, amount=200)], "trace")
    assert newer["applied"] == 1
    assert repository.query(user_id, "account", "account-1")[0]["payload"]["balance_minor"] == 200


@pytest.mark.integration
def test_receipts_report_retained_version_and_provenance_for_every_status(tenant):
    repository, user_id = tenant
    applied = repository.apply(user_id, "newest", "newest", [projection(3, amount=300)], "trace")
    retained = repository.query(user_id, "account", "account-1")[0]
    applied_resource = applied["resources"][0]
    assert applied_resource["source_version"] == 3
    assert applied_resource["incoming_source_version"] == 3
    assert applied_resource["observed_at"] == retained["observed_at"]
    assert applied_resource["source_digest"] == retained["source_digest"]

    stale = repository.apply(user_id, "stale", "stale", [projection(2, amount=200)], "trace")
    stale_resource = stale["resources"][0]
    assert stale_resource["status"] == "stale"
    assert stale_resource["source_version"] == 3
    assert stale_resource["incoming_source_version"] == 2
    assert stale_resource["observed_at"] == retained["observed_at"]
    assert stale_resource["source_digest"] == retained["source_digest"]

    duplicate = repository.apply(user_id, "duplicate", "duplicate", [projection(3, amount=300)], "trace")
    duplicate_resource = duplicate["resources"][0]
    assert duplicate_resource["status"] == "duplicate"
    assert duplicate_resource["source_version"] == 3
    assert duplicate_resource["incoming_source_version"] == 3
    assert duplicate_resource["observed_at"] == retained["observed_at"]
    assert duplicate_resource["source_digest"] == retained["source_digest"]

    repository.apply(user_id, "later", "later", [projection(4, amount=400)], "trace")
    assert repository.event(user_id, "stale") == stale
    assert repository.apply(user_id, "stale", "stale", [projection(4, amount=400)], "later-trace") == stale


@pytest.mark.integration
def test_concurrent_out_of_order_versions_never_regress_and_equal_version_conflicts(tenant):
    from banking_agent.errors import DomainError

    repository, user_id = tenant
    versions = [5, 1, 3, 2, 4, 5, 1, 3]
    with ThreadPoolExecutor(max_workers=6) as executor:
        results = list(executor.map(
            lambda indexed: repository.apply(
                user_id, f"event-{indexed[0]}", f"digest-{indexed[0]}",
                [projection(indexed[1], amount=indexed[1] * 100)], "trace",
            ), enumerate(versions),
        ))
    assert len(results) == len(versions)
    assert repository.query(user_id, "account")[0]["source_version"] == 5
    with ThreadPoolExecutor(max_workers=2) as executor:
        futures = [
            executor.submit(repository.apply, user_id, f"changed-{index}", f"changed-{index}", [projection(5, amount=999)], "trace")
            for index in range(2)
        ]
    for future in futures:
        with pytest.raises(DomainError) as error:
            future.result()
        assert error.value.code == "SOURCE_VERSION_CONFLICT"
    assert repository.query(user_id, "account")[0]["payload"]["balance_minor"] == 500


@pytest.mark.integration
def test_profile_has_local_defaults_and_optimistic_revision(tenant):
    from banking_agent.errors import DomainError

    repository, user_id = tenant
    profile = repository.profile(user_id)
    assert profile["revision"] == 0
    assert profile["currency"] == "CNY"
    assert profile["timezone"] == "Asia/Shanghai"
    saved = repository.save_profile(user_id, 0, "Asia/Shanghai", 12000, "trace")
    assert saved["revision"] == 1
    assert saved["safety_reserve_minor"] == 12000
    assert repository.profile(user_id) == saved
    with pytest.raises(DomainError) as error:
        repository.save_profile(user_id, 0, "UTC", 0, "trace")
    assert error.value.code == "PROFILE_VERSION_CONFLICT"
    assert repository.profile(user_id) == saved


@pytest.mark.integration
def test_concurrent_profile_updates_have_one_optimistic_winner(tenant):
    from banking_agent.errors import DomainError

    repository, user_id = tenant

    def save(amount):
        try:
            return repository.save_profile(user_id, 0, "Asia/Shanghai", amount, "trace")
        except DomainError as error:
            return error

    with ThreadPoolExecutor(max_workers=2) as executor:
        results = list(executor.map(save, [100, 200]))
    assert sum(isinstance(result, dict) for result in results) == 1
    assert [result.code for result in results if isinstance(result, DomainError)] == ["PROFILE_VERSION_CONFLICT"]
    assert repository.profile(user_id)["revision"] == 1


@pytest.mark.integration
def test_identity_mapping_cannot_be_rebound(tenant):
    from banking_agent.errors import DomainError

    repository, user_id = tenant
    with pytest.raises(DomainError) as error:
        repository.ensure_identity(user_id, "different-coze-user", "trace")
    assert error.value.code == "IDENTITY_CONFLICT"


@pytest.mark.integration
def test_identity_allows_missing_optional_coze_user_and_binds_only_once(database):
    repository, _, _ = database
    user_id = f"integration-{uuid4()}"
    assert repository.ensure_identity(user_id, None, "trace")["coze_user_id"] is None
    assert repository.ensure_identity(user_id, None, "trace")["banking_user_id"] == user_id
    coze_user_id = f"coze-{uuid4()}"
    assert repository.ensure_identity(user_id, coze_user_id, "trace")["coze_user_id"] == coze_user_id
    assert repository.ensure_identity(user_id, None, "trace")["coze_user_id"] == coze_user_id


@pytest.mark.integration
def test_migrations_are_idempotent_and_runtime_cannot_apply_them(database):
    from banking_agent.migrate import apply_migrations

    repository, app_url, admin_url = database
    apply_migrations(admin_url)
    apply_migrations(admin_url)
    assert repository.ready()
    with pytest.raises(RuntimeError, match="migration"):
        apply_migrations(app_url)


@pytest.mark.integration
@pytest.mark.parametrize("privilege", ["CREATEDB", "CREATEROLE", "REPLICATION"])
def test_readiness_rejects_membership_in_privileged_nologin_role(database, privilege):
    import psycopg
    from psycopg import sql

    repository, _, admin_url = database
    role_name = f"agent_test_privileged_{uuid4().hex}"
    role_identifier = sql.Identifier(role_name)
    with psycopg.connect(admin_url, autocommit=True) as admin:
        admin.execute(sql.SQL("CREATE ROLE {} NOLOGIN {}").format(role_identifier, sql.SQL(privilege)))
        try:
            admin.execute(sql.SQL("GRANT {} TO banking_agent_app").format(role_identifier))
            assert repository.ready() is False
            admin.execute(sql.SQL("REVOKE {} FROM banking_agent_app").format(role_identifier))
            assert repository.ready() is True
        finally:
            admin.execute(sql.SQL("REVOKE {} FROM banking_agent_app").format(role_identifier))
            admin.execute(sql.SQL("DROP ROLE {}").format(role_identifier))
