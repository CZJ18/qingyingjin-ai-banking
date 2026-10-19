"""Native LangGraph orchestration; credentials live only in Runtime.context."""

import asyncio
import hashlib
import json
from dataclasses import dataclass, field
from typing import Annotated, Any, TypedDict

from langgraph.graph import END, START, StateGraph
from langgraph.runtime import Runtime
from langgraph.types import Command, Send, interrupt

from . import monthly_plan
from .agent_models import AgentSubmit, AgentTaskRequest, AgentTaskSpec, safe_parameters
from .checkpoint import TenantCheckpointSaver
from .errors import DomainError


def digest(value):
    return hashlib.sha256(
        json.dumps(
            value, sort_keys=True, separators=(",", ":"), ensure_ascii=False, allow_nan=False
        ).encode()
    ).hexdigest()


def merge_results(left, right):
    result = dict(left or {})
    for key, value in right.items():
        if key in result and result[key] != value:
            raise DomainError(
                "TOOL_OUTPUT_INVALID", "completed task output cannot be replaced", 502
            )
        result[key] = value
    return result


def resolve_parameters(value, dependencies):
    if isinstance(value, dict):
        if set(value) == {"$ref"}:
            ref = value["$ref"]
            if not isinstance(ref, str) or len(ref) > 256:
                raise DomainError("INVALID_TASK_PLAN", "bounded dependency reference required", 422)
            parts = ref.split(".")
            if parts[0] not in dependencies:
                raise DomainError(
                    "INVALID_TASK_PLAN", "reference must name an explicit dependency", 422
                )
            item = dependencies[parts[0]]
            try:
                for part in parts[1:]:
                    item = item[int(part)] if isinstance(item, list) else item[part]
            except (KeyError, IndexError, TypeError, ValueError):
                raise DomainError(
                    "INVALID_TASK_PLAN", "dependency evidence is missing", 422
                ) from None
            return item
        return {key: resolve_parameters(item, dependencies) for key, item in value.items()}
    if isinstance(value, list):
        return [resolve_parameters(item, dependencies) for item in value]
    return value


class GraphState(TypedDict, total=False):
    task_id: str
    request: dict[str, Any]
    specs: list[dict]
    outputs: Annotated[dict[str, dict], merge_results]
    supervisor_metadata: dict
    spec: dict
    dependency_outputs: dict


@dataclass(frozen=True)
class RunContext:
    user_id: str
    task_id: str
    lease: str = field(repr=False)
    bearer: str = field(repr=False)
    trace: str
    repository: Any = field(repr=False)
    tools: Any = field(repr=False)
    max_attempts: int
    monthly_goal: dict | None = None

    async def db(self, method, *args, **kwargs):
        return await asyncio.to_thread(
            getattr(self.repository, method), self.user_id, self.task_id, *args, **kwargs
        )

    async def active(self):
        if self.monthly_goal and self.monthly_goal["period"] != monthly_plan.current_period():
            raise DomainError(
                "MONTHLY_GOAL_EXPIRED", "monthly goal no longer belongs to the current month", 409
            )
        if not await self.db("check_active", self.lease):
            raise DomainError("TASK_INTERRUPTED", "task was cancelled or lease expired", 409)

    async def safe_finish(self, status, result=None, *, error_code=""):
        try:
            return await self.db("finish", self.lease, status, result, error_code=error_code)
        except DomainError as error:
            if error.code != "LEASE_CONFLICT":
                raise
            return await self.db("get")

    async def safe_cancel(self):
        # Read the CAS version before checking the lease: a new claim after
        # either read increments version and prevents this worker cancelling it.
        current = await self.db("get")
        if not await self.db("check_active", self.lease):
            return await self.db("get")
        try:
            return await self.db("cancel", current["version"])
        except DomainError as error:
            if error.code not in ("LEASE_CONFLICT", "TASK_VERSION_CONFLICT", "TASK_CONFLICT"):
                raise
            return await self.db("get")


async def journal_call(context, node, parameters, mode, operation, *, idempotent):
    await context.active()
    request_digest = digest(parameters)
    prior = await context.db("call_get", node)
    if prior and prior.get("request_digest") not in (None, request_digest):
        raise DomainError("TOOL_CALL_CONFLICT", "immutable tool parameters changed", 409)
    if prior and prior["state"] == "succeeded":
        return prior["result"]
    if prior and prior["state"] in ("running", "unknown") and not idempotent:
        raise DomainError(
            "TOOL_OUTCOME_UNKNOWN", "inspect the source plan before creating a new request", 409
        )
    await context.db("call_start", context.lease, node, request_digest, mode)
    for attempt in range(context.max_attempts if idempotent else 1):
        await context.active()
        try:
            result = await operation()
            await context.db("call_finish", node, request_digest, result, lease_token=context.lease)
            return result
        except DomainError as error:
            await context.db(
                "call_fail",
                node,
                request_digest,
                error.code,
                status="failed" if idempotent else "unknown",
                lease_token=context.lease,
            )
            if (
                not idempotent
                or error.code in ("COZE_UNAVAILABLE", "MODEL_OUTCOME_UNKNOWN", "MODEL_BUDGET_UNAVAILABLE")
                or error.status not in (503, 504)
                or attempt + 1 == context.max_attempts
            ):
                raise
        except asyncio.CancelledError:
            await asyncio.shield(
                context.db(
                    "call_fail",
                    node,
                    request_digest,
                    "TOOL_OUTCOME_UNKNOWN",
                    status="unknown",
                    lease_token=context.lease,
                )
            )
            raise
    raise DomainError("TOOL_OUTCOME_UNKNOWN", "tool did not produce a verified result", 409)


async def supervisor(state: GraphState, runtime: Runtime[RunContext]):
    context = runtime.context
    await context.active()
    specs = list(state.get("specs", []))
    metadata = state.get("supervisor_metadata", {})
    if not specs:
        request = state["request"]
        raw = await journal_call(
            context,
            "__internal:supervisor_plan",
            {"instruction": request["instruction"], "bindings": request["bindings"]},
            "read_only",
            lambda: context.tools.plan(
                context.bearer,
                context.user_id,
                context.task_id,
                request["instruction"],
                request["bindings"],
                context.trace,
            ),
            idempotent=True,
        )
        try:
            parsed = AgentSubmit.model_validate(
                {"tasks": raw["tasks"], "monthly_goal": request.get("monthly_goal")}
            )
        except (ValueError, KeyError):
            raise DomainError(
                "TOOL_OUTPUT_INVALID", "Supervisor returned an invalid task graph", 502
            ) from None
        specs = [task.model_dump() for task in parsed.tasks]
        metadata = {"workflow_execute_id": raw.get("workflow_execute_id", "")}
    # Only this fixed read task may be added from authoritative next_actions.
    # Advice and financial intent parameters are never taken from free text.
    outputs = state.get("outputs", {})
    if (
        monthly_plan.REVIEW_NODE not in outputs
        and len(specs) < 32
        and not any(task["agent_type"] == "subscription" for task in specs)
    ):
        for task in specs:
            output = outputs.get(task["key"], {})
            if task["agent_type"] == "billing" and any(
                action.get("type") == "verify_subscriptions"
                for action in output.get("next_actions", [])
            ):
                key = "verify_subscriptions"
                if any(item["key"] == key for item in specs):
                    break
                params = {
                    key: value
                    for key, value in task["parameters"].items()
                    if key in {"from", "to", "account_id"}
                }
                params = resolve_parameters(params, {k: outputs[k] for k in task["dependencies"]})
                addition = AgentTaskSpec(
                    key=key,
                    agent_type="subscription",
                    tool="detect_subscriptions",
                    skill_id="subscription_analysis",
                    skill_version=1,
                    execution_mode="read_only",
                    parameters=params,
                    dependencies=[task["key"]],
                )
                specs.append(addition.model_dump())
                break
    if context.monthly_goal:
        try:
            monthly_plan.validate_monthly_specs(specs)
        except ValueError:
            raise DomainError(
                "INVALID_TASK_PLAN", "monthly plan cannot be reviewed safely", 422
            ) from None
        accepted = outputs.get(monthly_plan.REVIEW_NODE)
        if accepted:
            view = monthly_review_view(state["request"], specs, outputs, context.task_id)
            if accepted.get("result", {}).get("plan_digest") != digest(view):
                raise DomainError("CONFIRMATION_CONFLICT", "accepted monthly plan changed", 409)
    return {"specs": specs, "supervisor_metadata": metadata}


def dispatch(state: GraphState):
    specs, outputs = state["specs"], state.get("outputs", {})
    remaining = [task for task in specs if task["key"] not in outputs]
    if state["request"].get("monthly_goal") and monthly_plan.REVIEW_NODE not in outputs:
        remaining = [
            task for task in remaining if task["execution_mode"] in monthly_plan.SOURCE_MODES
        ]
        if not remaining:
            return monthly_plan.REVIEW_NODE
    if not remaining:
        return END
    ready = [task for task in remaining if all(dep in outputs for dep in task["dependencies"])]
    if not ready:
        raise DomainError("INVALID_TASK_PLAN", "dependency graph cannot advance", 422)
    ready.sort(key=lambda task: (task["priority"], task["key"]))
    reads = [task for task in ready if task["execution_mode"] == "read_only"]
    selected = reads or ready[:1]
    return [
        Send(
            "specialist_" + task["agent_type"],
            {
                "spec": task,
                "dependency_outputs": {key: outputs[key] for key in task["dependencies"]},
            },
        )
        for task in selected
    ]


def monthly_review_view(request, specs, outputs, task_id):
    try:
        suggestions = monthly_plan.validate_suggestions(specs, outputs, task_id)
    except ValueError:
        raise DomainError(
            "TOOL_OUTPUT_INVALID", "monthly plan requires successful verified suggestions", 502
        ) from None
    if not monthly_plan.has_usable_plan(specs, suggestions, request["monthly_goal"]):
        raise DomainError(
            "PLAN_ADVICE_REQUIRED",
            "a feasible monthly plan requires complete source information",
            422,
        )
    return {
        "confirmation_scope": "PLAN_ACCEPTANCE",
        "monthly_goal": request["monthly_goal"],
        "goal_text": request["instruction"],
        "plan_tasks": specs,
        "suggestions": suggestions,
        "source_digest": digest(suggestions),
    }


async def monthly_review(state: GraphState, runtime: Runtime[RunContext]):
    """This node is server-owned and never comes from a model task specification."""
    context = runtime.context
    await context.active()
    view = monthly_review_view(
        state["request"], state["specs"], state.get("outputs", {}), context.task_id
    )
    request_digest, node = digest(view), monthly_plan.REVIEW_NODE
    challenge = await context.db("challenge", context.lease, node, request_digest, view)
    decision = interrupt(
        {
            "node_key": node,
            "digest": request_digest,
            "parameters_view": view,
            "expires_at": challenge.get("expires_at"),
            "confirmation_scope": "PLAN_ACCEPTANCE",
        }
    )
    if (
        not isinstance(decision, dict)
        or decision.get("node_key") != node
        or decision.get("digest") != request_digest
        or decision.get("decision") not in ("approve", "reject")
    ):
        raise DomainError("CONFIRMATION_CONFLICT", "bound plan acceptance required", 409)
    if decision["decision"] == "reject":
        raise DomainError("USER_REJECTED", "user declined the monthly plan", 409)
    if not await context.db("approved", node, request_digest):
        raise DomainError("CONFIRMATION_EXPIRED", "current persisted plan acceptance required", 409)
    await context.active()
    return {
        "outputs": {
            node: {
                "task_id": context.task_id + "." + node,
                "agent_type": "supervisor",
                "status": "succeeded",
                "result": {
                    "plan_accepted": True,
                    "authorization_granted": False,
                    "financial_execution_enabled": False,
                    "plan_digest": request_digest,
                    "source_digest": view["source_digest"],
                },
                "evidence": [],
                "errors": [],
                "next_actions": [],
                "financial_execution_enabled": False,
            }
        }
    }


async def specialist(state: GraphState, runtime: Runtime[RunContext]):
    context = runtime.context
    await context.active()
    item = dict(state["spec"])
    if item["blocked"]:
        raise DomainError("MISSING_INFORMATION", "task needs user-supplied source information", 422)
    item["parameters"] = resolve_parameters(item["parameters"], state["dependency_outputs"])
    try:
        item = AgentTaskSpec.model_validate(item).model_dump()
        safe_parameters(item["parameters"])
    except ValueError:
        raise DomainError(
            "INVALID_TASK_PLAN", "resolved parameters failed validation", 422
        ) from None
    mode, node = item["execution_mode"], item["key"]
    if mode in ("intent", "user_confirmation"):
        request_digest = digest(item)
        previous = await context.db("call_get", node)
        if previous and previous["state"] == "succeeded":
            return {"outputs": {node: previous["result"]}}
        challenge = await context.db("challenge", context.lease, node, request_digest, item)
        decision = interrupt(
            {
                "node_key": node,
                "digest": request_digest,
                "parameters_view": item,
                "expires_at": challenge.get("expires_at"),
                "confirmation_scope": "PREPARE_ONLY",
            }
        )
        if (
            not isinstance(decision, dict)
            or decision.get("node_key") != node
            or decision.get("digest") != request_digest
            or decision.get("decision") not in ("approve", "reject")
        ):
            raise DomainError("CONFIRMATION_CONFLICT", "bound user confirmation required", 409)
        if decision["decision"] == "reject":
            raise DomainError("USER_REJECTED", "user declined the preparation request", 409)
        if not await context.db("approved", node, request_digest):
            raise DomainError("CONFIRMATION_EXPIRED", "current persisted approval required", 409)
        await context.active()
        if mode == "user_confirmation":
            output = {
                "task_id": context.task_id + "." + node,
                "agent_type": "supervisor",
                "status": "succeeded",
                "result": {"advice_acknowledged": True, "authorization_granted": False},
                "evidence": [],
                "errors": [],
                "next_actions": [],
                "financial_execution_enabled": False,
            }
            return {"outputs": {node: output}}
    result = await journal_call(
        context,
        node,
        item,
        mode,
        lambda: context.tools.execute(
            context.bearer, context.user_id, context.task_id, item, context.trace
        ),
        idempotent=mode == "read_only"
        or mode == "intent"
        or item["tool"] == "compile_lifestyle_plan",
    )
    return {"outputs": {node: result}}


def build_graph(saver):
    builder = StateGraph(GraphState, context_schema=RunContext)
    builder.add_node("supervisor", supervisor)
    builder.add_node(monthly_plan.REVIEW_NODE, monthly_review)
    builder.add_edge(monthly_plan.REVIEW_NODE, "supervisor")
    for role in ("billing", "subscription", "lifestyle", "savings", "supervisor"):
        builder.add_node("specialist_" + role, specialist)
        builder.add_edge("specialist_" + role, "supervisor")
    builder.add_edge(START, "supervisor")
    builder.add_conditional_edges("supervisor", dispatch)
    return builder.compile(checkpointer=saver)


class LangGraphOrchestrator:
    def __init__(self, repository, tools, *, saver_factory=TenantCheckpointSaver):
        self.repository, self.tools, self.saver_factory = repository, tools, saver_factory

    async def run(self, user_id, task_id, bearer, trace, *, expected_version=None, resume=None):
        task = await asyncio.to_thread(self.repository.get, user_id, task_id)
        if task["status"] in ("succeeded", "cancelled"):
            return task
        request = task["request"]
        lease = await asyncio.to_thread(
            self.repository.claim,
            user_id,
            task_id,
            expected_version,
            (request["timeout_ms"] + 999) // 1000 + 30,
        )
        context = RunContext(
            user_id,
            task_id,
            lease,
            bearer,
            trace,
            self.repository,
            self.tools,
            request["max_attempts"],
            request.get("monthly_goal"),
        )
        saver = self.saver_factory(self.repository, user_id, task_id, lease_token=lease)
        graph = build_graph(saver)
        config = {"configurable": {"thread_id": task_id}, "recursion_limit": 100}
        try:
            try:
                AgentTaskRequest.model_validate(request)
            except ValueError:
                raise DomainError(
                    "INVALID_TASK_PLAN", "stored task request is invalid", 422
                ) from None
            await context.active()
            snapshot = await graph.aget_state(config)
            if snapshot.values and snapshot.values.get("request") != request:
                raise DomainError("CONFIRMATION_CONFLICT", "checkpoint request changed", 409)
            if resume is None and snapshot.interrupts:
                value = snapshot.interrupts[0].value
                resume = await context.db("decision", value["node_key"], value["digest"])
                if resume is None:
                    raise DomainError(
                        "CONFIRMATION_EXPIRED",
                        "existing preparation decision is unavailable or expired",
                        409,
                    )
            if resume is not None:
                if not snapshot.interrupts:
                    raise DomainError(
                        "CONFIRMATION_CONFLICT", "task has no pending graph interrupt", 409
                    )
                input_value = Command(resume=resume)
            elif snapshot.values:
                input_value = None
            else:
                input_value = {
                    "task_id": task_id,
                    "request": request,
                    "specs": request["tasks"],
                    "outputs": {},
                }
            async with asyncio.timeout(request["timeout_ms"] / 1000):
                result = await graph.ainvoke(
                    input_value, config, context=context, durability="sync"
                )
            snapshot = await graph.aget_state(config)
            if snapshot.interrupts:
                return await context.safe_finish("waiting_confirmation")
            outputs = result.get("outputs", {})
            supervisor_output = {
                "task_id": task_id + ".supervisor",
                "agent_type": "supervisor",
                "status": "succeeded",
                "skill_id": "task_planning",
                "skill_version": 1,
                "result": {"completed_tasks": sorted(outputs), "owner_engine": "langgraph"},
                "evidence": [
                    evidence
                    for output in outputs.values()
                    for evidence in output.get("evidence", [])
                ],
                "errors": [],
                "next_actions": [],
                "financial_execution_enabled": False,
                **result.get("supervisor_metadata", {}),
            }
            return await context.safe_finish(
                "succeeded",
                {
                    "outputs": [supervisor_output, *outputs.values()],
                    "financial_execution_enabled": False,
                },
            )
        except DomainError as error:
            if error.code == "LEASE_CONFLICT":
                return await context.db("get")
            if error.code == "USER_REJECTED":
                return await context.safe_cancel()
            status = (
                "interrupted"
                if error.code in ("TOOL_OUTCOME_UNKNOWN", "TASK_INTERRUPTED")
                else "failed"
            )
            return await context.safe_finish(status, error_code=error.code)
        except TimeoutError:
            return await context.safe_finish("interrupted", error_code="TASK_TIMEOUT")
        except asyncio.CancelledError:
            await asyncio.shield(context.safe_finish("interrupted", error_code="TASK_INTERRUPTED"))
            raise
        except Exception:
            # Serializer/DB/engine errors may contain SQL or credentials. Do not
            # persist exception strings or use an in-memory production fallback.
            return await context.safe_finish("failed", error_code="TASK_ENGINE_ERROR")
