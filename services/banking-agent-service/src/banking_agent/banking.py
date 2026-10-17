"""Bearer authentication and fixed-path GET-only access to the native Go service."""

import json
import re
from dataclasses import dataclass
from typing import Any

import httpx

from .errors import DomainError
from .source import Projection, canonical_numeric_id, normalize_resource, validate_source_id

_RESOURCE_PATHS = {
    "account": "/api/v1/accounts/",
    "transaction": "/api/v1/transactions/",
    "subscription": "/api/v1/subscriptions/",
    "budget": "/api/v1/budgets/",
    "lifestyle_goal": "/api/v2/lifestyle-goals/",
    "lifestyle_plan": "/api/v2/lifestyle-plans/",
    "operation": "/api/v1/execution/operations/",
}
_TRACE = re.compile(r"[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}\Z", re.ASCII)
_TOKEN = re.compile(r"[!-~]{1,8192}\Z", re.ASCII)
_MAX_RESPONSE_BYTES = 1024 * 1024


@dataclass(frozen=True)
class Principal:
    user_id: str
    coze_user_id: str | None = None


def _unique_object(pairs: list[tuple[str, Any]]) -> dict:
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError("duplicate JSON property")
        result[key] = value
    return result


def _invalid_constant(value: str) -> None:
    raise ValueError("non-finite JSON number")


class BankingClient:
    def __init__(
        self, base_url: str, timeout: float = 5, transport: httpx.AsyncBaseTransport | None = None
    ):
        try:
            origin = httpx.URL(base_url)
            if (
                origin.scheme not in ("http", "https")
                or not origin.host
                or origin.userinfo
                or origin.path not in ("", "/")
                or origin.query
                or origin.fragment
            ):
                raise ValueError(
                    "Banking URL must be an HTTP origin without credentials, path or query"
                )
        except (httpx.InvalidURL, TypeError):
            raise ValueError("Banking URL must be a valid HTTP origin") from None
        self._client = httpx.AsyncClient(
            base_url=origin,
            timeout=timeout,
            transport=transport,
            trust_env=False,
            follow_redirects=False,
            headers={"Accept": "application/json", "Accept-Encoding": "identity"},
        )

    async def close(self) -> None:
        await self._client.aclose()

    async def _get(self, path: str, token: str | None, trace_id: str) -> dict:
        allowed = path in {"/api/v1/context", "/api/v1/users/me", "/readyz"}
        if not allowed and type(path) is str:
            for kind, prefix in _RESOURCE_PATHS.items():
                if path.startswith(prefix):
                    validate_source_id(kind, path[len(prefix) :])
                    allowed = True
                    break
        if not allowed:
            raise DomainError("INVALID_ARGUMENT", "Banking read route is not allowed", 400)
        if type(trace_id) is not str or not _TRACE.fullmatch(trace_id):
            raise DomainError("INVALID_ARGUMENT", "A valid trace ID is required", 400)
        headers = {"X-Trace-ID": trace_id}
        if token is not None:
            if type(token) is not str or not _TOKEN.fullmatch(token):
                raise DomainError("UNAUTHENTICATED", "A valid user bearer token is required", 401)
            headers["Authorization"] = "Bearer " + token
        try:
            async with self._client.stream("GET", path, headers=headers) as response:
                if response.status_code == 401:
                    raise DomainError(
                        "UNAUTHENTICATED", "A valid user bearer token is required", 401
                    )
                if response.status_code == 403:
                    raise DomainError("FORBIDDEN", "Banking resource access is forbidden", 403)
                if response.status_code == 404:
                    raise DomainError("SOURCE_NOT_FOUND", "Banking resource was not found", 404)
                if response.status_code >= 500:
                    raise DomainError(
                        "BANKING_UNAVAILABLE", "Banking service is temporarily unavailable", 503
                    )
                if (
                    response.status_code != 200
                    or response.headers.get("content-encoding", "identity").lower() != "identity"
                ):
                    raise DomainError(
                        "SOURCE_OUTPUT_INVALID", "Banking source output failed validation", 502
                    )
                content_length = response.headers.get("content-length")
                if content_length is not None and (
                    len(content_length) > 10
                    or not content_length.isascii()
                    or not content_length.isdecimal()
                    or int(content_length) > _MAX_RESPONSE_BYTES
                ):
                    raise DomainError(
                        "SOURCE_OUTPUT_INVALID", "Banking source output failed validation", 502
                    )
                body = bytearray()
                async for chunk in response.aiter_bytes():
                    if len(body) + len(chunk) > _MAX_RESPONSE_BYTES:
                        raise DomainError(
                            "SOURCE_OUTPUT_INVALID", "Banking source output failed validation", 502
                        )
                    body.extend(chunk)
        except httpx.RequestError:
            raise DomainError(
                "BANKING_UNAVAILABLE", "Banking service is temporarily unavailable", 503
            ) from None
        try:
            envelope = json.loads(
                body, object_pairs_hook=_unique_object, parse_constant=_invalid_constant
            )
            if (
                type(envelope) is not dict
                or envelope.get("code") != "OK"
                or type(envelope.get("data")) is not dict
            ):
                raise ValueError("invalid envelope")
            return envelope["data"]
        except (ValueError, TypeError, UnicodeDecodeError, RecursionError):
            raise DomainError(
                "SOURCE_OUTPUT_INVALID", "Banking source output failed validation", 502
            ) from None

    async def authenticate(self, token: str, trace_id: str) -> Principal:
        context = await self._get("/api/v1/context", token, trace_id)
        actor = context.get("actor")
        if type(actor) is not dict:
            raise DomainError("UNAUTHENTICATED", "A valid user identity is required", 401)
        if actor.get("kind") != "user":
            raise DomainError("FORBIDDEN", "Only user bearer identities are accepted", 403)
        try:
            user_id = canonical_numeric_id(actor.get("user_id"))
            if canonical_numeric_id(actor.get("subject")) != user_id:
                raise ValueError("identity mismatch")
            coze_user_id = actor.get("coze_user_id")
            if coze_user_id in (None, ""):
                coze_user_id = None
            else:
                coze_user_id = canonical_numeric_id(coze_user_id)
        except ValueError:
            raise DomainError("UNAUTHENTICATED", "A valid user identity is required", 401) from None
        try:
            user = await self._get("/api/v1/users/me", token, trace_id)
        except DomainError as error:
            if error.status == 404:
                raise DomainError(
                    "UNAUTHENTICATED", "An active Banking user is required", 401
                ) from None
            raise
        try:
            if canonical_numeric_id(user.get("id")) != user_id or user.get("status") != "active":
                raise ValueError("inactive or mismatched user")
        except ValueError:
            raise DomainError(
                "UNAUTHENTICATED", "An active Banking user is required", 401
            ) from None
        return Principal(user_id=user_id, coze_user_id=coze_user_id)

    async def fetch_resource(
        self, token: str, trace_id: str, kind: str, source_id: str, user_id: str
    ) -> list[Projection]:
        source_id = validate_source_id(kind, source_id)
        try:
            canonical_numeric_id(user_id)
        except ValueError:
            raise DomainError("INVALID_ARGUMENT", "A canonical user ID is required", 400) from None
        data = await self._get(_RESOURCE_PATHS[kind] + source_id, token, trace_id)
        return normalize_resource(kind, source_id, user_id, data)

    async def ready(self) -> bool:
        """Probe the anonymous native dependency health without user credentials."""
        try:
            data = await self._get("/readyz", None, "banking-agent-readiness")
            dependencies = data.get("dependencies")
            if type(dependencies) is not dict:
                return False
            drivers = {"mysql", "postgres"} & dependencies.keys()
            if len(drivers) != 1:
                return False
            required = {"redis", "schema"} | drivers
            if "postgres" in drivers:
                required.add("storage")
            return required <= dependencies.keys() and all(
                value == "ready" for value in dependencies.values()
            )
        except DomainError:
            return False
