"""User-owned graph API; bound acceptance and preparation never execute funds."""

import asyncio
import hmac
import json
from typing import Annotated, Any, Literal
from uuid import UUID

from fastapi import Depends, Header, Query, Request
from fastapi.responses import StreamingResponse
from pydantic import BaseModel, Field

from .agent_graph import digest
from .agent_models import AgentResume, AgentSubmit, AgentTaskRequest, TaskVersionInput
from .errors import DomainError
from .models import Envelope, StrictInput


class InternalModelTaskInput(StrictInput):
    owner_id: str = Field(
        min_length=1, max_length=128, pattern=r"^[A-Za-z0-9][A-Za-z0-9:_-]{0,127}$"
    )
    task_id: str = Field(
        pattern=r"^lg-[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.[A-Za-z0-9][A-Za-z0-9_-]{0,63}$"
    )


class PreparationChallenge(BaseModel):
    node_key: str
    digest: str
    nonce: str
    expires_at: str
    status: Literal["pending"]
    parameters_view: dict[str, Any]
    confirmation_scope: Literal["PREPARE_ONLY", "PLAN_ACCEPTANCE"] = "PREPARE_ONLY"


class AgentTaskView(BaseModel):
    task_id: str = Field(pattern=r"^lg-[0-9a-f-]{36}$")
    owner_engine: Literal["langgraph"]
    status: Literal[
        "queued",
        "running",
        "interrupted",
        "waiting_confirmation",
        "succeeded",
        "failed",
        "cancelled",
    ]
    version: int = Field(ge=1)
    request: AgentTaskRequest
    result: dict[str, Any] | None
    error_code: str
    confirmation: PreparationChallenge | None
    created_at: str
    updated_at: str
    financial_execution_enabled: Literal[False] = False


class AgentTaskEnvelope(Envelope):
    data: AgentTaskView


class AgentTaskSummary(BaseModel):
    task_id: str = Field(pattern=r"^lg-[0-9a-f-]{36}$")
    status: Literal["queued", "running", "interrupted", "waiting_confirmation", "succeeded", "failed", "cancelled"]
    version: int = Field(ge=1)
    instruction: str
    display_title: str = Field(default="", max_length=512)
    error_code: str
    created_at: str
    updated_at: str


class AgentTaskPage(BaseModel):
    items: list[AgentTaskSummary]
    total: int = Field(ge=0)
    limit: int = Field(ge=1, le=100)
    offset: int = Field(ge=0, le=1000000)


class AgentTaskPageEnvelope(Envelope):
    data: AgentTaskPage


def validate_task_id(raw):
    try:
        if not raw.startswith("lg-") or str(UUID(raw[3:])) != raw[3:]:
            raise ValueError
    except (ValueError, AttributeError):
        raise DomainError("INVALID_ARGUMENT", "canonical LangGraph task ID required", 400) from None


def register_agent_routes(app, authenticate, response):
    Actor = Annotated[tuple, Depends(authenticate)]

    def tasks(request):
        return request.app.state.tasks

    async def db(request, actor, method, *args):
        return await asyncio.to_thread(getattr(tasks(request), method), actor[0].user_id, *args)

    async def internal_service(request: Request):
        supplied = request.headers.get("X-Banking-Agent-Key", "")
        expected = request.app.state.agent_service_key
        if not expected or not hmac.compare_digest(supplied.encode(), expected.encode()):
            raise DomainError(
                "UNAUTHENTICATED", "Valid internal service authentication required", 401
            )

    InternalService = Annotated[None, Depends(internal_service)]

    @app.post("/internal/model-task-context", response_model=Envelope, include_in_schema=False)
    async def internal_model_context(
        request: Request, body: InternalModelTaskInput, _service: InternalService
    ):
        context = await asyncio.to_thread(tasks(request).model_context, body.owner_id, body.task_id)
        return response(request, context)

    @app.post(
        "/api/v1/agent-tasks",
        response_model=AgentTaskEnvelope,
        tags=["Agent Tasks"],
        status_code=202,
    )
    async def submit(
        request: Request,
        body: AgentSubmit,
        actor: Actor,
        idempotency_key: Annotated[str, Header(alias="Idempotency-Key")],
    ):
        from .main import correlation

        if not correlation.fullmatch(idempotency_key):
            raise DomainError("INVALID_ARGUMENT", "valid idempotency key required", 400)
        # Keep existing ordinary requests stable for persisted idempotency keys.
        value = body.model_dump(exclude={"monthly_goal"} if body.monthly_goal is None else None)
        serialized = json.dumps(value, ensure_ascii=False)
        secrets = (actor[1], request.app.state.agent_service_key)
        if any(
            secret and (secret in serialized or secret in idempotency_key) for secret in secrets
        ):
            raise DomainError(
                "INVALID_ARGUMENT", "credentials cannot be included in task content", 400
            )
        out = await db(
            request, actor, "submit", idempotency_key, digest(value), value, request.state.trace_id
        )
        if request.app.state.scheduler is not None:
            delegation = await request.app.state.agent_tools.background_delegation(
                actor[1], out["task_id"], digest(value), request.state.trace_id
            )
            await db(
                request,
                actor,
                "enroll",
                out["task_id"],
                digest(value),
                delegation["capability"],
                delegation["expires_in_seconds"],
            )
        return response(request, out)

    @app.get("/api/v1/agent-tasks", response_model=AgentTaskPageEnvelope, tags=["Agent Tasks"])
    async def history(
        request: Request,
        actor: Actor,
        limit: Annotated[int, Query(ge=1, le=100)] = 20,
        offset: Annotated[int, Query(ge=0, le=1000000)] = 0,
    ):
        return response(request, await db(request, actor, "list_recent", limit, offset))

    @app.get(
        "/api/v1/agent-tasks/{task_id}", response_model=AgentTaskEnvelope, tags=["Agent Tasks"]
    )
    async def status(request: Request, task_id: str, actor: Actor):
        validate_task_id(task_id)
        return response(request, await db(request, actor, "get", task_id))

    @app.post(
        "/api/v1/agent-tasks/{task_id}/run", response_model=AgentTaskEnvelope, tags=["Agent Tasks"]
    )
    async def run(request: Request, task_id: str, body: TaskVersionInput, actor: Actor):
        validate_task_id(task_id)
        if not request.app.state.agent_service_key:
            raise DomainError(
                "DEPENDENCY_UNAVAILABLE", "Agent service delegation is not configured", 503
            )
        if request.app.state.scheduler is not None:
            current = await db(request, actor, "get", task_id)
            if current["status"] == "queued":
                if current["version"] != body.expected_version:
                    return response(request, current)
                request_digest = digest(current["request"])
                delegation = await request.app.state.agent_tools.background_delegation(
                    actor[1], task_id, request_digest, request.state.trace_id
                )
                await db(
                    request,
                    actor,
                    "enroll",
                    task_id,
                    request_digest,
                    delegation["capability"],
                    delegation["expires_in_seconds"],
                )
                return response(request, await db(request, actor, "get", task_id))
            if current["status"] in ("running", "succeeded", "cancelled", "waiting_confirmation"):
                return response(request, current)
        out = await request.app.state.orchestrator.run(
            actor[0].user_id,
            task_id,
            actor[1],
            request.state.trace_id,
            expected_version=body.expected_version,
        )
        return response(request, out)

    @app.post(
        "/api/v1/agent-tasks/{task_id}/resume",
        response_model=AgentTaskEnvelope,
        tags=["Agent Tasks"],
    )
    async def resume(request: Request, task_id: str, body: AgentResume, actor: Actor):
        validate_task_id(task_id)
        if not request.app.state.agent_service_key:
            raise DomainError(
                "DEPENDENCY_UNAVAILABLE", "Agent service delegation is not configured", 503
            )
        if request.app.state.scheduler is not None:
            # Renew only using the authenticated owner's persisted immutable request.
            # Do this while waiting_confirmation so the queue cannot race expiry.
            current = await db(request, actor, "get", task_id)
            request_digest = digest(current["request"])
            delegation = await request.app.state.agent_tools.background_delegation(
                actor[1], task_id, request_digest, request.state.trace_id
            )
            await db(
                request,
                actor,
                "enroll",
                task_id,
                request_digest,
                delegation["capability"],
                delegation["expires_in_seconds"],
            )
        await db(
            request,
            actor,
            "confirm",
            task_id,
            body.expected_version,
            body.node_key,
            body.digest,
            body.nonce,
            body.decision,
        )
        if request.app.state.scheduler is not None:
            return response(request, await db(request, actor, "get", task_id))
        # The graph restores only the database-confirmed decision. Arbitrary
        # Command.update, checkpoint_id, confirmed=true or new parameters are absent.
        out = await request.app.state.orchestrator.run(
            actor[0].user_id, task_id, actor[1], request.state.trace_id
        )
        return response(request, out)

    @app.post(
        "/api/v1/agent-tasks/{task_id}/cancel",
        response_model=AgentTaskEnvelope,
        tags=["Agent Tasks"],
    )
    async def cancel(request: Request, task_id: str, body: TaskVersionInput, actor: Actor):
        validate_task_id(task_id)
        return response(request, await db(request, actor, "cancel", task_id, body.expected_version))

    @app.get("/api/v1/agent-tasks/{task_id}/events", response_model=Envelope, tags=["Agent Tasks"])
    async def events(
        request: Request,
        task_id: str,
        actor: Actor,
        after: Annotated[int, Query(ge=0, le=9_223_372_036_854_775_807)] = 0,
        limit: Annotated[int, Query(ge=1, le=200)] = 100,
    ):
        validate_task_id(task_id)
        return response(request, await db(request, actor, "events", task_id, after, limit))

    @app.get(
        "/api/v1/agent-tasks/{task_id}/events/stream",
        tags=["Agent Tasks"],
        responses={
            200: {
                "content": {"text/event-stream": {}},
                "description": "Owner-scoped events; after is the durable event sequence",
            }
        },
    )
    async def stream(
        request: Request,
        task_id: str,
        actor: Actor,
        after: Annotated[int, Query(ge=0, le=9_223_372_036_854_775_807)] = 0,
    ):
        validate_task_id(task_id)
        await db(request, actor, "get", task_id)

        async def chunks():
            cursor = after
            for _ in range(30):
                if await request.is_disconnected():
                    break
                # Re-check expired/revoked identity throughout the connection.
                try:
                    await request.app.state.banking.authenticate(actor[1], request.state.trace_id)
                except DomainError:
                    yield "event: auth_expired\ndata: {}\n\n"
                    break
                batch = await db(request, actor, "events", task_id, cursor, 200)
                for event in batch:
                    cursor = event["seq"]
                    yield f"id: {cursor}\nevent: task\ndata: {json.dumps(event, ensure_ascii=False)}\n\n"
                current = await db(request, actor, "get", task_id)
                if current["status"] in (
                    "succeeded",
                    "failed",
                    "interrupted",
                    "waiting_confirmation",
                    "cancelled",
                ):
                    break
                yield ": heartbeat\n\n"
                await asyncio.sleep(1)

        return StreamingResponse(
            chunks(),
            media_type="text/event-stream",
            headers={"Cache-Control": "no-store", "X-Accel-Buffering": "no"},
        )
