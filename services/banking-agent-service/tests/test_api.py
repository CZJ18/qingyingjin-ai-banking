"""HTTP boundary tests with explicitly injected fakes, never production fallbacks."""

import json

import psycopg
import pytest
from fastapi.testclient import TestClient

from banking_agent.banking import Principal
from banking_agent.config import Settings
from banking_agent.errors import DomainError
from banking_agent.main import create_app


class Bank:
    def __init__(self):
        self.calls = []
        self.fail = False

    async def authenticate(self, token, trace):
        if token == "agent":
            raise DomainError("FORBIDDEN", "user required", 403)
        if token not in ("user-a", "user-b"):
            raise DomainError("UNAUTHENTICATED", "invalid token", 401)
        return Principal("1" if token == "user-a" else "2", None)

    async def ready(self):
        return not self.fail

    async def fetch_resource(self, token, trace, kind, source_id, user_id):
        self.calls.append((kind, source_id, user_id))
        if self.fail or source_id == "999":
            raise DomainError("BANKING_UNAVAILABLE", "bank unavailable", 503)
        return [object()]


class Store:
    def __init__(self):
        self.applied = 0
        self.saved = {}
        self.queries = []

    def ready(self):
        return True

    def ensure_identity(self, user_id, coze_user_id, trace):
        pass

    def receipt(self, user_id, key, digest):
        if (user_id, key) in self.saved:
            old_digest, result = self.saved[user_id, key]
            if old_digest != digest:
                raise DomainError("IDEMPOTENCY_CONFLICT", "key conflict", 409)
            return result

    def apply(self, user_id, key, digest, projections, trace):
        self.applied += 1
        result = {
            "event_id": key,
            "applied": len(projections),
            "duplicates": 0,
            "stale": 0,
            "resources": [],
        }
        self.saved[user_id, key] = (digest, result)
        return result

    def profile(self, user_id):
        return {
            "user_id": user_id,
            "currency": "CNY",
            "timezone": "Asia/Shanghai",
            "safety_reserve_minor": 0,
            "revision": 0,
        }

    def save_profile(self, user_id, expected_version, timezone, amount, trace):
        if expected_version != 0:
            raise DomainError("VERSION_CONFLICT", "version conflict", 409)
        return {
            "user_id": user_id,
            "currency": "CNY",
            "timezone": timezone,
            "safety_reserve_minor": amount,
            "revision": 1,
        }

    def query(self, user_id, kind, source_id=None, limit=50, offset=0):
        self.queries.append((user_id, kind, source_id, limit, offset))
        return []

    def event(self, user_id, key):
        v = self.saved.get((user_id, key))
        return None if v is None else v[1]


class Cache:
    def __init__(self):
        self.values = []
        self.fail = False

    async def ping(self):
        return not self.fail

    async def set(self, key, value, ex):
        if self.fail:
            raise OSError("test-only cache unavailable")
        self.values.append((key, value, ex))


@pytest.fixture
def api():
    bank, store, cache = Bank(), Store(), Cache()
    settings = Settings(
        "postgresql://app:test-password@localhost/test",
        "redis://:test-password@localhost/0",
        "http://banking.invalid",
    )
    with TestClient(create_app(settings, banking=bank, repository=store, cache=cache)) as client:
        yield client, bank, store, cache


def headers(user="user-a", key="test-event"):
    return {"Authorization": "Bearer " + user, "Idempotency-Key": key, "X-Trace-ID": "test-api"}


def test_auth_is_required_and_no_agent_identity(api):
    c, _, s, _ = api
    for h, expected in [({}, 401), (headers("invalid"), 401), (headers("agent"), 403)]:
        assert c.get("/api/v1/profile", headers=h).status_code == expected
    assert s.applied == 0
    a = c.get("/api/v1/me", headers=headers()).json()
    assert a["data"]["user_id"] == "1" and a["data"]["financial_execution_enabled"] is False


def test_refresh_replay_and_owner_scope(api):
    c, b, s, cache = api
    body = {"resources": [{"kind": "account", "source_id": "1"}]}
    r = c.post("/api/v1/projections/refresh", json=body, headers=headers())
    assert r.status_code == 200
    assert (
        c.post("/api/v1/projections/refresh", json=body, headers=headers()).json()["data"]
        == r.json()["data"]
    )
    assert len(b.calls) == 1 and s.applied == 1
    assert (
        c.post(
            "/api/v1/projections/refresh",
            json={"resources": [{"kind": "account", "source_id": "2"}]},
            headers=headers(),
        ).status_code
        == 409
    )
    assert (
        c.get("/api/v1/projections/events/test-event", headers=headers("user-b")).status_code == 404
    )
    assert set(json.loads(cache.values[0][1])) == {"event_id", "applied", "duplicates", "stale"}
    c.get("/api/v1/projections/account", headers=headers("user-b"))
    assert s.queries[-1][0] == "2"


def test_source_failure_causes_no_partial_update(api):
    c, _, s, _ = api
    r = c.post(
        "/api/v1/projections/refresh",
        headers=headers(),
        json={
            "resources": [
                {"kind": "account", "source_id": "1"},
                {"kind": "transaction", "source_id": "999"},
            ]
        },
    )
    assert r.status_code == 503 and s.applied == 0


def test_idempotency_key_binds_original_resource_order(api):
    c, _, s, _ = api
    refs = [{"kind": "account", "source_id": "1"}, {"kind": "transaction", "source_id": "2"}]
    assert (
        c.post(
            "/api/v1/projections/refresh", headers=headers(), json={"resources": refs}
        ).status_code
        == 200
    )
    assert (
        c.post(
            "/api/v1/projections/refresh",
            headers=headers(),
            json={"resources": list(reversed(refs))},
        ).status_code
        == 409
    )
    assert s.applied == 1


@pytest.mark.parametrize(
    "body",
    [
        {"user_id": "2", "resources": [{"kind": "account", "source_id": "1"}]},
        {"resources": [{"kind": "transfer", "source_id": "1"}]},
        {"resources": [{"kind": "account", "source_id": 1}]},
        {"resources": [{"kind": "account", "source_id": "https://evil.invalid"}]},
        {
            "resources": [
                {"kind": "account", "source_id": "1"},
                {"kind": "account", "source_id": "1"},
            ]
        },
        {"resources": []},
    ],
)
def test_no_arbitrary_data_or_tools(api, body):
    c, b, s, _ = api
    assert c.post("/api/v1/projections/refresh", json=body, headers=headers()).status_code == 422
    assert not b.calls and not s.applied


def test_profile_integer_preference_and_versions(api):
    c, _, _, _ = api
    body = {"expected_version": 0, "timezone": "Asia/Shanghai", "safety_reserve_minor": 12345}
    r = c.put("/api/v1/profile", json=body, headers=headers())
    assert r.status_code == 200 and r.json()["data"]["safety_reserve_minor"] == 12345
    for bad in [True, 1.5, "12345", -1, 1_000_000_000_001]:
        assert (
            c.put(
                "/api/v1/profile", json=dict(body, safety_reserve_minor=bad), headers=headers()
            ).status_code
            == 422
        )
    assert (
        c.put(
            "/api/v1/profile", json=dict(body, timezone="made-up/timezone"), headers=headers()
        ).status_code
        == 422
    )
    assert (
        c.put("/api/v1/profile", json=dict(body, expected_version=1), headers=headers()).status_code
        == 409
    )


def test_cache_failure_does_not_reexecute_committed_projection(api):
    c, b, s, cache = api
    cache.fail = True
    body = {"resources": [{"kind": "account", "source_id": "1"}]}
    assert c.post("/api/v1/projections/refresh", json=body, headers=headers()).status_code == 200
    assert c.post("/api/v1/projections/refresh", json=body, headers=headers()).status_code == 200
    assert s.applied == 1 and len(b.calls) == 1


def test_health_openapi_and_boundary_logs(api, caplog):
    c, b, _, _ = api
    assert c.get("/healthz").status_code == 200
    assert c.get("/readyz").status_code == 200
    b.fail = True
    assert c.get("/readyz").status_code == 503
    doc = c.get("/openapi.json").json()
    assert doc["openapi"].startswith("3.") and "HTTPBearer" in doc["components"]["securitySchemes"]
    assert {"source_version", "source_digest", "observed_at", "evidence"} <= set(
        doc["components"]["schemas"]["ProjectionView"]["required"]
    )
    assert not any("execute" in p or "transfer" in p or "authorize" in p for p in doc["paths"])
    with caplog.at_level("INFO", logger="banking_agent"):
        r = c.post(
            "/api/v1/projections/refresh", json={"secret": "never-log-me"}, headers=headers()
        )
    assert (
        r.status_code == 422 and "never-log-me" not in caplog.text and "user-a" not in caplog.text
    )
    assert c.get("/healthz", headers={"X-Trace-ID": "invalid space"}).status_code == 400
    assert (
        c.post(
            "/api/v1/projections/refresh", content=b"x" * 1_048_577, headers=headers()
        ).status_code
        == 413
    )
    assert c.get("/api/v1/projections/account?limit=101", headers=headers()).status_code == 422


def test_financial_routes_are_absent(api):
    c, _, _, _ = api
    assert (
        c.post("/api/v1/transfers", json={"confirmed": True}, headers=headers()).status_code == 404
    )


def test_database_errors_are_sanitized(api):
    c, _, s, _ = api

    def unavailable(_user_id):
        raise psycopg.OperationalError("postgresql://user:never-return-password@localhost/db")

    s.profile = unavailable
    response = c.get("/api/v1/profile", headers=headers())
    assert response.status_code == 503
    assert response.json()["code"] == "DEPENDENCY_UNAVAILABLE"
    assert "never-return-password" not in response.text


def test_refresh_key_is_required_by_runtime_and_openapi(api):
    client, _, store, _ = api
    response = client.post(
        "/api/v1/projections/refresh",
        headers={"Authorization": "Bearer user-a"},
        json={"resources": [{"kind": "account", "source_id": "1"}]},
    )
    assert response.status_code == 422 and response.json()["code"] == "INVALID_ARGUMENT"
    assert store.applied == 0
    operation = client.get("/openapi.json").json()["paths"]["/api/v1/projections/refresh"]["post"]
    header = next(item for item in operation["parameters"] if item["name"] == "Idempotency-Key")
    assert header["in"] == "header" and header["required"] is True


def test_unrestricted_runtime_role_blocks_application_startup():
    bank, store, cache = Bank(), Store(), Cache()
    store.ready = lambda: False
    settings = Settings(
        "postgresql://app:test-password@localhost/test",
        "redis://:test-password@localhost/0",
        "http://banking.invalid",
    )
    with pytest.raises(RuntimeError, match="restricted runtime role"):
        with TestClient(create_app(settings, banking=bank, repository=store, cache=cache)):
            pass
