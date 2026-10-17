import asyncio

import httpx
import pytest

from banking_agent.banking import BankingClient
from banking_agent.errors import DomainError


def envelope(data):
    return {"code": "OK", "message": "success", "data": data}


def context(**changes):
    return {
        "actor": {
            "kind": "user",
            "subject": "42",
            "user_id": "42",
            "coze_user_id": "101",
            **changes,
        }
    }


def run(coroutine):
    return asyncio.run(coroutine)


def test_authentication_verifies_user_context_then_active_registered_user():
    requests = []

    def handler(request):
        requests.append(request)
        data = (
            context() if request.url.path.endswith("context") else {"id": "42", "status": "active"}
        )
        return httpx.Response(200, json=envelope(data))

    async def scenario():
        client = BankingClient("http://banking.test", transport=httpx.MockTransport(handler))
        try:
            principal = await client.authenticate("opaque-bearer", "trace.test")
            assert (principal.user_id, principal.coze_user_id) == ("42", "101")
        finally:
            await client.close()

    run(scenario())
    assert [r.url.path for r in requests] == ["/api/v1/context", "/api/v1/users/me"]
    assert all(r.method == "GET" for r in requests)
    assert all(r.headers["authorization"] == "Bearer opaque-bearer" for r in requests)
    assert all(r.headers["x-trace-id"] == "trace.test" for r in requests)


@pytest.mark.parametrize(
    "changes",
    [
        {"subject": "43"},
        {"user_id": "042", "subject": "042"},
        {"user_id": 42},
        {"subject": True},
        {"coze_user_id": "0101"},
        {"user_id": "9223372036854775808", "subject": "9223372036854775808"},
    ],
)
def test_authentication_rejects_invalid_identity(changes):
    seen = []

    def handler(request):
        seen.append(request.url.path)
        return httpx.Response(200, json=envelope(context(**changes)))

    async def scenario():
        client = BankingClient("http://banking.test", transport=httpx.MockTransport(handler))
        try:
            with pytest.raises(DomainError) as error:
                await client.authenticate("opaque-bearer", "trace.test")
            assert (error.value.code, error.value.status) == ("UNAUTHENTICATED", 401)
        finally:
            await client.close()

    run(scenario())
    assert seen == ["/api/v1/context"]


@pytest.mark.parametrize("kind", ["agent", "tool"])
def test_authentication_rejects_agent_and_tool_actors(kind):
    async def scenario():
        client = BankingClient(
            "http://banking.test",
            transport=httpx.MockTransport(
                lambda request: httpx.Response(200, json=envelope(context(kind=kind)))
            ),
        )
        try:
            with pytest.raises(DomainError) as error:
                await client.authenticate("opaque-bearer", "trace.test")
            assert error.value.status == 403
        finally:
            await client.close()

    run(scenario())


@pytest.mark.parametrize(
    "user",
    [
        {"id": "43", "status": "active"},
        {"id": "42", "status": "inactive"},
        {"id": 42, "status": "active"},
    ],
)
def test_authentication_requires_same_active_user(user):
    async def scenario():
        client = BankingClient(
            "http://banking.test",
            transport=httpx.MockTransport(
                lambda request: httpx.Response(
                    200, json=envelope(context() if request.url.path.endswith("context") else user)
                )
            ),
        )
        try:
            with pytest.raises(DomainError) as error:
                await client.authenticate("opaque-bearer", "trace.test")
            assert error.value.code == "UNAUTHENTICATED"
        finally:
            await client.close()

    run(scenario())


def test_timeout_errors_are_sanitized():
    def handler(request):
        raise httpx.ReadTimeout("secret-token SQL internal address", request=request)

    async def scenario():
        client = BankingClient("http://banking.test", transport=httpx.MockTransport(handler))
        try:
            with pytest.raises(DomainError) as error:
                await client.authenticate("opaque-bearer", "trace.test")
            assert (error.value.code, error.value.status) == ("BANKING_UNAVAILABLE", 503)
            assert "secret" not in error.value.message
        finally:
            await client.close()

    run(scenario())


def test_redirects_never_forward_bearer_credentials():
    seen = []

    def handler(request):
        seen.append(str(request.url))
        return httpx.Response(302, headers={"Location": "https://evil.test/collect"})

    async def scenario():
        client = BankingClient("http://banking.test", transport=httpx.MockTransport(handler))
        try:
            with pytest.raises(DomainError) as error:
                await client.authenticate("opaque-bearer", "trace.test")
            assert error.value.code == "SOURCE_OUTPUT_INVALID"
        finally:
            await client.close()

    run(scenario())
    assert seen == ["http://banking.test/api/v1/context"]


def test_fetch_resource_only_uses_allowlisted_get_and_validates_ownership():
    seen = []
    raw = {
        "id": "12",
        "user_id": "43",
        "created_at": 100,
        "updated_at": 101,
        "display_name": "Account",
        "account_type": "checking",
        "masked_account_number": "****",
        "currency": "CNY",
        "balance_minor": 100,
        "status": "active",
        "version": 1,
        "is_simulated": True,
    }

    def handler(request):
        seen.append((request.method, request.url.path))
        return httpx.Response(200, json=envelope(raw))

    async def scenario():
        client = BankingClient("http://banking.test", transport=httpx.MockTransport(handler))
        try:
            with pytest.raises(DomainError) as error:
                await client.fetch_resource("opaque-bearer", "trace.test", "account", "12", "42")
            assert error.value.code == "SOURCE_OUTPUT_INVALID"
            with pytest.raises(DomainError):
                await client.fetch_resource("opaque-bearer", "trace.test", "account", "../12", "42")
        finally:
            await client.close()

    run(scenario())
    assert seen == [("GET", "/api/v1/accounts/12")]


@pytest.mark.parametrize(
    "response",
    [
        httpx.Response(200, json={"code": "OK", "data": []}),
        httpx.Response(200, json={"code": "DATABASE_ERROR", "message": "secret"}),
        httpx.Response(200, content=b'{"code":"OK","code":"OK","data":{}}'),
        httpx.Response(200, content=b"not JSON secret"),
        httpx.Response(
            200,
            content=b'{"code":"OK","data":{"nested":' + b"[" * 1200 + b"0" + b"]" * 1200 + b"}}",
        ),
        httpx.Response(200, headers={"Content-Length": "9" * 5000}, json=envelope({})),
        httpx.Response(500, content=b"database secret"),
    ],
)
def test_upstream_response_failures_do_not_expose_raw_content(response):
    async def scenario():
        client = BankingClient(
            "http://banking.test", transport=httpx.MockTransport(lambda request: response)
        )
        try:
            with pytest.raises(DomainError) as error:
                await client.authenticate("opaque-bearer", "trace.test")
            assert "secret" not in error.value.message
        finally:
            await client.close()

    run(scenario())


def test_readiness_probe_is_anonymous_and_checks_dependency_values():
    seen = []

    def handler(request):
        seen.append(request)
        return httpx.Response(
            200,
            json=envelope(
                {
                    "dependencies": {"mysql": "ready", "redis": "ready", "schema": "ready"},
                }
            ),
        )

    async def scenario():
        client = BankingClient("http://banking.test", transport=httpx.MockTransport(handler))
        try:
            assert await client.ready()
        finally:
            await client.close()

    run(scenario())
    assert seen[0].url.path == "/readyz"
    assert "authorization" not in seen[0].headers


@pytest.mark.parametrize(
    ("dependencies", "expected"),
    [
        pytest.param(
            {"postgres": "ready", "redis": "ready", "schema": "ready", "storage": "ready"},
            True,
            id="postgres-ready",
        ),
        pytest.param(
            {"mysql": "ready", "redis": "ready", "schema": "ready", "storage": "ready"},
            True,
            id="mysql-with-storage-ready",
        ),
        pytest.param(
            {
                "mysql": "ready",
                "postgres": "ready",
                "redis": "ready",
                "schema": "ready",
                "storage": "ready",
            },
            False,
            id="dual-drivers-rejected",
        ),
        pytest.param(
            {"redis": "ready", "schema": "ready", "storage": "ready"},
            False,
            id="driver-required",
        ),
        pytest.param(
            {"postgres": "ready", "redis": "ready", "schema": "ready"},
            False,
            id="postgres-storage-required",
        ),
        pytest.param(
            {"postgres": "ready", "redis": "ready", "schema": "ready", "storage": "unavailable"},
            False,
            id="postgres-frozen-rejected",
        ),
        pytest.param(
            {"postgres": "ready", "redis": "ready", "schema": "unavailable", "storage": "ready"},
            False,
            id="schema-required-ready",
        ),
        pytest.param(
            {"postgres": "ready", "schema": "ready", "storage": "ready"},
            False,
            id="redis-required",
        ),
        pytest.param(
            {"mysql": "ready", "redis": "ready", "schema": "ready", "extra": "unavailable"},
            False,
            id="all-reported-dependencies-required-ready",
        ),
    ],
)
def test_readiness_supports_exactly_one_database_driver(dependencies, expected):
    def handler(request):
        assert request.method == "GET"
        assert request.url.path == "/readyz"
        assert "authorization" not in request.headers
        return httpx.Response(200, json=envelope({"dependencies": dependencies}))

    async def scenario():
        client = BankingClient("http://banking.test", transport=httpx.MockTransport(handler))
        try:
            assert await client.ready() is expected
        finally:
            await client.close()

    run(scenario())


@pytest.mark.parametrize(
    "base_url",
    [
        "ftp://banking.test",
        "http://user:secret@banking.test",
        "http://banking.test/other",
        "http://banking.test/?redirect=evil",
    ],
)
def test_base_url_must_be_fixed_http_origin(base_url):
    with pytest.raises(ValueError):
        BankingClient(base_url)


def test_response_stream_stops_at_size_bound_and_closes_connection():
    class OversizedStream(httpx.AsyncByteStream):
        def __init__(self):
            self.chunks_read = 0
            self.closed = False

        async def __aiter__(self):
            for _ in range(20):
                self.chunks_read += 1
                yield b"x" * (256 * 1024)

        async def aclose(self):
            self.closed = True

    stream = OversizedStream()

    async def scenario():
        client = BankingClient(
            "http://banking.test",
            transport=httpx.MockTransport(lambda request: httpx.Response(200, stream=stream)),
        )
        try:
            with pytest.raises(DomainError) as error:
                await client.authenticate("opaque-bearer", "trace.test")
            assert error.value.code == "SOURCE_OUTPUT_INVALID"
        finally:
            await client.close()

    run(scenario())
    assert stream.chunks_read <= 5
    assert stream.closed


def test_invalid_private_route_is_rejected_before_request():
    seen = []

    def handler(request):
        seen.append(request)
        return httpx.Response(200, json=envelope({}))

    async def scenario():
        client = BankingClient("http://banking.test", transport=httpx.MockTransport(handler))
        try:
            with pytest.raises(DomainError):
                await client._get("https://evil.test", "opaque-bearer", "trace.test")
        finally:
            await client.close()

    run(scenario())
    assert seen == []


@pytest.mark.parametrize(
    "data",
    [
        {"dependencies": {"postgres": "unavailable"}},
        {"dependencies": ["ready"]},
        {"dependencies": {}},
        {"dependencies": {"mysql": "ready", "redis": "ready"}},
        {"dependencies": {"mysql": "ready", "redis": "ready", "schema": "unavailable"}},
        {},
    ],
)
def test_readiness_fails_closed_on_bad_dependency_state(data):
    async def scenario():
        client = BankingClient(
            "http://banking.test",
            transport=httpx.MockTransport(lambda request: httpx.Response(200, json=envelope(data))),
        )
        try:
            assert not await client.ready()
        finally:
            await client.close()

    run(scenario())
