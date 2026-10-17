import hashlib
import json
import logging
import re
import time
from contextlib import asynccontextmanager
from typing import Annotated
from uuid import uuid4

import anyio
import psycopg
from fastapi import Depends, FastAPI, Header, Query, Request
from fastapi.exceptions import RequestValidationError
from fastapi.responses import JSONResponse
from fastapi.security import HTTPAuthorizationCredentials, HTTPBearer
from redis.asyncio import Redis
from starlette.exceptions import HTTPException as StarletteHTTPException

from .agent_graph import LangGraphOrchestrator
from .agent_routes import register_agent_routes
from .agent_tools import AgentBankingClient
from .banking import BankingClient, Principal
from .config import Settings
from .errors import DomainError
from .models import (
    Envelope,
    EvidenceEnvelope,
    ProfileEnvelope,
    ProfileInput,
    ProjectionEnvelope,
    ProjectionListEnvelope,
    RefreshEnvelope,
    RefreshInput,
)
from .repository import Repository
from .task_repository import TaskRepository

log = logging.getLogger("banking_agent")
correlation = re.compile(r"^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$")
kinds = {
    "account",
    "transaction",
    "subscription",
    "budget",
    "lifestyle_goal",
    "lifestyle_plan",
    "recommendation",
    "operation",
}
bearer = HTTPBearer(auto_error=False)


class SafeLogFormatter(logging.Formatter):
    def format(self, record):
        return json.dumps(
            {
                "level": record.levelname,
                "service": "banking-agent-service",
                "message": record.getMessage(),
            },
            ensure_ascii=False,
        )


def configure_logging():
    log.setLevel(logging.INFO)
    if not any(getattr(handler, "banking_agent_safe", False) for handler in log.handlers):
        handler = logging.StreamHandler()
        handler.setFormatter(SafeLogFormatter())
        handler.banking_agent_safe = True
        log.addHandler(handler)


def envelope(request: Request, data=None, code="OK", message="success"):
    return {
        "code": code,
        "message": message,
        "request_id": request.state.request_id,
        "trace_id": request.state.trace_id,
        "data": data,
    }


async def in_thread(fn, *args):
    return await anyio.to_thread.run_sync(fn, *args)


def create_app(
    settings: Settings | None = None,
    *,
    banking=None,
    repository=None,
    cache=None,
    task_repository=None,
    agent_tools=None,
):
    """Injected dependencies are explicit test fixtures; production has no fallback."""

    @asynccontextmanager
    async def lifespan(app: FastAPI):
        configure_logging()
        config = settings or Settings.from_env()
        app.state.repository = repository or Repository(config.database_url)
        app.state.banking = banking or BankingClient(config.banking_url, config.banking_timeout)
        app.state.cache = cache or Redis.from_url(
            config.redis_url, decode_responses=True, socket_timeout=3, socket_connect_timeout=3
        )
        app.state.tasks = task_repository or TaskRepository(app.state.repository)
        app.state.agent_service_key = config.agent_service_key
        app.state.agent_tools = agent_tools or AgentBankingClient(
            config.banking_url, config.agent_service_key
        )
        app.state.orchestrator = LangGraphOrchestrator(app.state.tasks, app.state.agent_tools)
        try:
            if not await in_thread(app.state.repository.ready):
                raise RuntimeError("projection schema or restricted runtime role is not ready")
            yield
        finally:
            if repository is None:
                await in_thread(app.state.repository.close)
            if banking is None:
                await app.state.banking.close()
            if cache is None:
                await app.state.cache.aclose()
            if agent_tools is None:
                await app.state.agent_tools.close()

    app = FastAPI(
        title="轻融金产品 API",
        version="0.5.0",
        lifespan=lifespan,
        docs_url=None,
        redoc_url=None,
        openapi_url="/openapi.json",
        description="Authenticated product preferences and Go Banking point-in-time projections. No financial execution endpoints.",
        responses={
            status: {"model": Envelope, "description": description}
            for status, description in {
                400: "INVALID_ARGUMENT / INVALID_INPUT",
                401: "UNAUTHENTICATED",
                403: "FORBIDDEN",
                404: "NOT_FOUND / SOURCE_NOT_FOUND",
                409: "IDEMPOTENCY_CONFLICT / SOURCE_VERSION_CONFLICT / PROFILE_VERSION_CONFLICT / IDENTITY_CONFLICT",
                413: "INVALID_ARGUMENT: body too large",
                422: "INVALID_ARGUMENT: schema validation",
                500: "INTERNAL_ERROR",
                502: "SOURCE_OUTPUT_INVALID",
                503: "BANKING_UNAVAILABLE / DEPENDENCY_UNAVAILABLE",
            }.items()
        },
    )

    @app.middleware("http")
    async def boundaries(request: Request, call_next):
        started = time.monotonic()
        request.state.request_id = str(uuid4())
        trace = request.headers.get("X-Trace-ID") or request.state.request_id
        authentication = request.headers.get("Authorization", "").split(" ", 1)
        request_secrets = (
            request.app.state.agent_service_key,
            authentication[1] if len(authentication) == 2 else "",
        )
        safe_trace = correlation.fullmatch(trace) and not any(
            secret and secret in trace for secret in request_secrets
        )
        request.state.trace_id = trace if safe_trace else request.state.request_id
        if not safe_trace:
            response = JSONResponse(
                envelope(request, code="INVALID_ARGUMENT", message="invalid trace header"),
                status_code=400,
            )
        else:
            # Bound actual streamed bytes, including requests without Content-Length.
            chunks, size = [], 0
            async for chunk in request.stream():
                size += len(chunk)
                if size > 1_048_576:
                    break
                chunks.append(chunk)
            if size > 1_048_576:
                response = JSONResponse(
                    envelope(request, code="INVALID_ARGUMENT", message="request body too large"),
                    status_code=413,
                )
            else:
                request._body = b"".join(chunks)
                try:
                    response = await call_next(request)
                except Exception:
                    # Never log exceptions carrying DSNs, SQL values or upstream bodies.
                    log.error("request_failed request_id=%s", request.state.request_id)
                    response = JSONResponse(
                        envelope(request, code="INTERNAL_ERROR", message="internal server error"),
                        status_code=500,
                    )
        response.headers.update(
            {
                "X-Request-ID": request.state.request_id,
                "X-Trace-ID": request.state.trace_id,
                "Cache-Control": "no-store",
            }
        )
        route = getattr(request.scope.get("route"), "path", "unmatched")
        log.info(
            "request method=%s route=%s status=%s duration_ms=%s request_id=%s trace_id=%s",
            request.method,
            route,
            response.status_code,
            round((time.monotonic() - started) * 1000),
            request.state.request_id,
            request.state.trace_id,
        )
        return response

    @app.exception_handler(DomainError)
    async def domain_error(request, exc):
        return JSONResponse(
            envelope(request, code=exc.code, message=exc.message), status_code=exc.status
        )

    @app.exception_handler(RequestValidationError)
    async def validation_error(request, _exc):
        # Pydantic errors can echo the submitted token or financial payload.
        return JSONResponse(
            envelope(
                request,
                code="INVALID_ARGUMENT",
                message="invalid input, unknown fields or non-integer amounts",
            ),
            status_code=422,
        )

    @app.exception_handler(psycopg.Error)
    async def database_error(request, _exc):
        return JSONResponse(
            envelope(
                request, code="DEPENDENCY_UNAVAILABLE", message="projection database unavailable"
            ),
            status_code=503,
        )

    @app.exception_handler(StarletteHTTPException)
    async def http_error(request, exc):
        return JSONResponse(
            envelope(
                request,
                code="NOT_FOUND" if exc.status_code == 404 else "INVALID_ARGUMENT",
                message="route not found" if exc.status_code == 404 else "request not allowed",
            ),
            status_code=exc.status_code,
        )

    async def authenticated(
        request: Request,
        credentials: Annotated[HTTPAuthorizationCredentials | None, Depends(bearer)],
    ):
        if (
            credentials is None
            or credentials.scheme.lower() != "bearer"
            or len(credentials.credentials) > 8192
        ):
            raise DomainError("UNAUTHENTICATED", "valid user bearer token required", 401)
        principal = await request.app.state.banking.authenticate(
            credentials.credentials, request.state.trace_id
        )
        await in_thread(
            request.app.state.repository.ensure_identity,
            principal.user_id,
            principal.coze_user_id,
            request.state.trace_id,
        )
        return principal, credentials.credentials

    Actor = Annotated[tuple[Principal, str], Depends(authenticated)]
    register_agent_routes(app, authenticated, envelope)

    @app.get("/healthz", response_model=Envelope, tags=["Health"])
    async def health(request: Request):
        return envelope(
            request, {"service": "banking-agent-service", "financial_execution_enabled": False}
        )

    @app.get("/readyz", response_model=Envelope, tags=["Health"])
    async def ready(request: Request):
        states = {}
        try:
            states["postgresql"] = (
                "ready" if await in_thread(request.app.state.repository.ready) else "unavailable"
            )
        except Exception:
            states["postgresql"] = "unavailable"
        try:
            states["redis"] = "ready" if await request.app.state.cache.ping() else "unavailable"
        except Exception:
            states["redis"] = "unavailable"
        try:
            states["banking"] = (
                "ready" if await request.app.state.banking.ready() else "unavailable"
            )
        except Exception:
            states["banking"] = "unavailable"
        ok = all(v == "ready" for v in states.values())
        return JSONResponse(
            envelope(
                request,
                {"dependencies": states},
                code="OK" if ok else "DEPENDENCY_UNAVAILABLE",
                message="ready" if ok else "dependencies unavailable",
            ),
            status_code=200 if ok else 503,
        )

    @app.get("/api/v1/me", response_model=Envelope, tags=["Identity"])
    async def me(request: Request, actor: Actor):
        user, _ = actor
        return envelope(
            request,
            {
                "user_id": user.user_id,
                "coze_user_id": user.coze_user_id,
                "identity_provider": "banking-service",
                "financial_execution_enabled": False,
            },
        )

    @app.get("/api/v1/profile", response_model=ProfileEnvelope, tags=["Profile"])
    async def profile(request: Request, actor: Actor):
        return envelope(
            request, await in_thread(request.app.state.repository.profile, actor[0].user_id)
        )

    @app.put("/api/v1/profile", response_model=ProfileEnvelope, tags=["Profile"])
    async def update_profile(request: Request, body: ProfileInput, actor: Actor):
        value = await in_thread(
            request.app.state.repository.save_profile,
            actor[0].user_id,
            body.expected_version,
            body.timezone,
            body.safety_reserve_minor,
            request.state.trace_id,
        )
        return envelope(request, value)

    @app.post("/api/v1/projections/refresh", response_model=RefreshEnvelope, tags=["Projections"])
    async def refresh(
        request: Request,
        body: RefreshInput,
        actor: Actor,
        idempotency_key: Annotated[str, Header(alias="Idempotency-Key")],
    ):
        if not correlation.fullmatch(idempotency_key):
            raise DomainError("INVALID_ARGUMENT", "valid Idempotency-Key required", 400)
        refs = [r.model_dump() for r in body.resources]
        digest = hashlib.sha256(
            json.dumps(refs, sort_keys=True, separators=(",", ":")).encode()
        ).hexdigest()
        user, token = actor
        saved = await in_thread(
            request.app.state.repository.receipt, user.user_id, idempotency_key, digest
        )
        if saved is not None:
            return envelope(request, saved)
        projections = []
        # Complete all source reads first. Any failed read prevents partial DB updates.
        for ref in sorted(refs, key=lambda r: (r["kind"], r["source_id"])):
            projections.extend(
                await request.app.state.banking.fetch_resource(
                    token, request.state.trace_id, ref["kind"], ref["source_id"], user.user_id
                )
            )
        result = await in_thread(
            request.app.state.repository.apply,
            user.user_id,
            idempotency_key,
            digest,
            projections,
            request.state.trace_id,
        )
        try:
            await request.app.state.cache.set(
                f"projection:{user.user_id}:{idempotency_key}",
                json.dumps({k: result[k] for k in ("event_id", "applied", "duplicates", "stale")}),
                ex=600,
            )
        except Exception:
            # Durable PostgreSQL receipt is authoritative; cache cannot undo a commit.
            log.warning("projection_cache_unavailable request_id=%s", request.state.request_id)
        return envelope(request, result)

    @app.get(
        "/api/v1/projections/events/{event_id}",
        response_model=RefreshEnvelope,
        tags=["Projections"],
    )
    async def event(request: Request, event_id: str, actor: Actor):
        if not correlation.fullmatch(event_id):
            raise DomainError("INVALID_ARGUMENT", "invalid event ID", 400)
        value = await in_thread(request.app.state.repository.event, actor[0].user_id, event_id)
        if value is None:
            raise DomainError("NOT_FOUND", "projection event not found", 404)
        return envelope(request, value)

    @app.get(
        "/api/v1/recommendations", response_model=ProjectionListEnvelope, tags=["Recommendations"]
    )
    async def recommendations(
        request: Request,
        actor: Actor,
        limit: Annotated[int, Query(ge=1, le=100)] = 50,
        offset: Annotated[int, Query(ge=0, le=1_000_000)] = 0,
    ):
        return envelope(
            request,
            await in_thread(
                request.app.state.repository.query,
                actor[0].user_id,
                "recommendation",
                None,
                limit,
                offset,
            ),
        )

    @app.get(
        "/api/v1/recommendations/{source_id}/evidence",
        response_model=EvidenceEnvelope,
        tags=["Recommendations"],
    )
    async def evidence(request: Request, source_id: str, actor: Actor):
        rows = await in_thread(
            request.app.state.repository.query, actor[0].user_id, "recommendation", source_id
        )
        if not rows:
            raise DomainError("NOT_FOUND", "recommendation not found", 404)
        return envelope(
            request,
            {
                "source_id": source_id,
                "source_version": rows[0]["source_version"],
                "observed_at": rows[0]["observed_at"],
                "source_digest": rows[0]["source_digest"],
                "evidence": rows[0]["evidence"],
            },
        )

    @app.get(
        "/api/v1/projections/{kind}", response_model=ProjectionListEnvelope, tags=["Projections"]
    )
    async def projections(
        request: Request,
        kind: str,
        actor: Actor,
        limit: Annotated[int, Query(ge=1, le=100)] = 50,
        offset: Annotated[int, Query(ge=0, le=1_000_000)] = 0,
    ):
        if kind not in kinds:
            raise DomainError("INVALID_ARGUMENT", "unknown projection kind", 400)
        return envelope(
            request,
            await in_thread(
                request.app.state.repository.query, actor[0].user_id, kind, None, limit, offset
            ),
        )

    @app.get(
        "/api/v1/projections/{kind}/{source_id}",
        response_model=ProjectionEnvelope,
        tags=["Projections"],
    )
    async def projection(request: Request, kind: str, source_id: str, actor: Actor):
        if kind not in kinds:
            raise DomainError("INVALID_ARGUMENT", "unknown projection kind", 400)
        rows = await in_thread(
            request.app.state.repository.query, actor[0].user_id, kind, source_id
        )
        if not rows:
            raise DomainError("NOT_FOUND", "projection not found", 404)
        return envelope(request, rows[0])

    return app


app = create_app()
