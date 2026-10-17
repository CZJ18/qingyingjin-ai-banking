import pytest
from fastapi.testclient import TestClient
from test_agent_models import spec
from test_api import Bank, Cache, Store, headers

from banking_agent.config import Settings
from banking_agent.main import create_app


def test_agent_endpoints_reject_agent_identity_and_document_user_confirmation():
    settings = Settings(
        "postgresql://app:test@localhost/test", "redis://:test@localhost/0", "http://bank.invalid"
    )
    with TestClient(
        create_app(settings, banking=Bank(), repository=Store(), cache=Cache())
    ) as client:
        response = client.post(
            "/api/v1/agent-tasks", headers=headers("agent"), json={"tasks": [spec().model_dump()]}
        )
        assert response.status_code == 403
        response = client.post(
            "/api/v1/agent-tasks",
            headers=headers(),
            json={"tasks": [spec().model_dump()], "owner_engine": "go"},
        )
        assert response.status_code == 422
        schema = client.get("/openapi.json").json()
        assert "/api/v1/agent-tasks/{task_id}/resume" in schema["paths"]
        properties = schema["components"]["schemas"]["AgentResume"]["properties"]
        assert {"expected_version", "node_key", "digest", "nonce", "decision"} <= set(properties)
        assert "confirmed" not in properties


@pytest.mark.parametrize("carrier", ["trace", "idempotency"])
@pytest.mark.parametrize("secret_kind", ["service", "bearer"])
def test_credentials_in_correlation_headers_never_echo_or_persist(carrier, secret_kind, caplog):
    service_key = "f" * 64
    secret = service_key if secret_kind == "service" else "user-a"
    settings = Settings(
        "postgresql://app:test@localhost/test",
        "redis://:test@localhost/0",
        "http://bank.invalid",
        agent_service_key=service_key,
    )
    store = Store()
    request_headers = headers()
    request_headers["X-Trace-ID" if carrier == "trace" else "Idempotency-Key"] = "prefix-" + secret
    with TestClient(
        create_app(settings, banking=Bank(), repository=store, cache=Cache())
    ) as client:
        response = client.post(
            "/api/v1/agent-tasks", headers=request_headers, json={"tasks": [spec().model_dump()]}
        )
        assert response.status_code == 400
        assert secret not in response.text
        assert secret not in response.headers["X-Trace-ID"]
        assert secret not in caplog.text
        assert store.applied == 0


def test_event_cursor_cannot_overflow_postgres_bigint():
    settings = Settings(
        "postgresql://app:test@localhost/test", "redis://:test@localhost/0", "http://bank.invalid"
    )
    with TestClient(
        create_app(settings, banking=Bank(), repository=Store(), cache=Cache())
    ) as client:
        task_id = "lg-00000000-0000-0000-0000-000000000000"
        for suffix in ("events", "events/stream"):
            response = client.get(
                f"/api/v1/agent-tasks/{task_id}/{suffix}?after=9223372036854775808",
                headers=headers(),
            )
            assert response.status_code == 422
