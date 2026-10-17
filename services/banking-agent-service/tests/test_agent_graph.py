import asyncio
import copy

import pytest
from langgraph.checkpoint.memory import InMemorySaver
from test_agent_models import spec

from banking_agent.agent_models import AgentSubmit
from banking_agent.errors import DomainError


class TaskStore:
    def __init__(self, request):
        self.task = {
            "task_id": "lg-00000000-0000-0000-0000-000000000001",
            "status": "queued",
            "version": 1,
            "request": request,
            "result": None,
            "confirmation": None,
        }
        self.calls = {}
        self.approval = None

    def get(self, _user, _task):
        return copy.deepcopy(self.task)

    def claim(self, _user, _task, expected_version=None, lease_seconds=120):
        self.task.update(status="running", version=self.task["version"] + 1)
        return "lease"

    def finish(self, _user, _task, _lease, status, result=None, error_code=""):
        self.task.update(
            status=status, result=result, version=self.task["version"] + 1, error_code=error_code
        )
        return self.get(_user, _task)

    def check_active(self, *_):
        return self.task["status"] == "running"

    def call_get(self, _user, _task, node):
        return self.calls.get(node)

    def call_start(self, _user, _task, _lease, node, digest, mode):
        return self.calls.setdefault(
            node, {"state": "running", "request_digest": digest, "mode": mode}
        )

    def call_finish(self, _user, _task, node, _digest, result, **_kw):
        self.calls[node].update(state="succeeded", result=result)

    def call_fail(self, _user, _task, node, _digest, error_code, status="failed", **_kw):
        self.calls[node].update(state=status, error_code=error_code)

    def challenge(self, _user, _task, _lease, node, digest, parameters):
        self.task["confirmation"] = {
            "node_key": node,
            "digest": digest,
            "nonce": "test-challenge-nonce",
            "parameters_view": parameters,
        }
        return self.task["confirmation"]

    def approved(self, *_):
        return self.approval == "approve"

    def decision(self, _user, _task, node, request_digest):
        if self.approval:
            return {"decision": self.approval, "node_key": node, "digest": request_digest}

    def cancel(self, user, task, expected_version):
        self.task.update(status="cancelled", version=expected_version + 1)
        return self.get(user, task)


class Tools:
    def __init__(self):
        self.called = []
        self.active = self.peak = 0
        self.fail = 0

    async def execute(self, token, user, task, item, trace):
        self.called.append(item["key"])
        if self.fail:
            self.fail -= 1
            raise DomainError("BANKING_UNAVAILABLE", "unavailable", 503)
        self.active += 1
        self.peak = max(self.peak, self.active)
        await asyncio.sleep(0.02)
        self.active -= 1
        return {
            "task_id": task + "." + item["key"],
            "agent_type": item["agent_type"],
            "status": "succeeded",
            "result": {"report_id": "real-report"},
            "evidence": [],
            "errors": [],
            "next_actions": [],
            "financial_execution_enabled": False,
        }


def system(request):
    from banking_agent.agent_graph import LangGraphOrchestrator

    store, tools, memory = TaskStore(request), Tools(), InMemorySaver()
    return (
        LangGraphOrchestrator(store, tools, saver_factory=lambda *_args, **_kw: memory),
        store,
        tools,
        memory,
    )


@pytest.mark.asyncio
async def test_parallel_read_tasks_and_dependency_wait():
    request = AgentSubmit.model_validate(
        {
            "tasks": [
                spec(key="a").model_dump(),
                spec(key="b").model_dump(),
                spec(key="c", dependencies=["a", "b"]).model_dump(),
            ]
        }
    ).model_dump()
    engine, store, tools, _ = system(request)
    result = await engine.run("42", store.task["task_id"], "never-persist-bearer", "trace")
    assert result["status"] == "succeeded" and tools.peak == 2
    assert tools.called[-1] == "c" and result["result"]["financial_execution_enabled"] is False


@pytest.mark.asyncio
async def test_completed_tools_not_recalled_after_checkpoint_replay():
    engine, store, tools, _ = system(
        AgentSubmit.model_validate({"tasks": [spec().model_dump()]}).model_dump()
    )
    await engine.run("42", store.task["task_id"], "never-persist-bearer", "trace")
    store.task["status"] = "interrupted"
    result = await engine.run("42", store.task["task_id"], "fresh-bearer", "trace")
    assert result["status"] == "succeeded" and tools.called == ["billing"]


@pytest.mark.asyncio
async def test_prepare_interrupt_requires_bound_resume_and_never_executes_funds():
    item = spec(
        key="prepare",
        agent_type="supervisor",
        tool="require_gateway",
        skill_id="operation_preparation",
        execution_mode="intent",
        parameters={"kind": "transfer", "parameters": {"amount_minor": 100}},
    ).model_dump()
    engine, store, tools, memory = system(
        AgentSubmit.model_validate({"tasks": [item]}).model_dump()
    )
    waiting = await engine.run("42", store.task["task_id"], "secret-user-bearer", "trace")
    assert waiting["status"] == "waiting_confirmation" and not tools.called
    assert "secret-user-bearer" not in repr(memory.storage)
    store.task["status"] = "queued"
    store.approval = "approve"
    ch = store.task["confirmation"]
    result = await engine.run(
        "42",
        store.task["task_id"],
        "fresh-bearer",
        "trace",
        resume={"decision": "approve", "node_key": "prepare", "digest": ch["digest"]},
    )
    assert result["status"] == "succeeded" and tools.called == ["prepare"]


@pytest.mark.asyncio
async def test_unknown_nonidempotent_advice_stops_instead_of_replaying():
    item = spec(
        key="replan",
        agent_type="savings",
        tool="replan_financial_plan",
        skill_id="savings_planning",
        execution_mode="advice",
        parameters={"plan_id": "1"},
    ).model_dump()
    engine, store, tools, _ = system(AgentSubmit.model_validate({"tasks": [item]}).model_dump())
    store.calls["replan"] = {"state": "unknown"}
    result = await engine.run("42", store.task["task_id"], "bearer", "trace")
    assert (
        result["status"] == "interrupted"
        and result["error_code"] == "TOOL_OUTCOME_UNKNOWN"
        and not tools.called
    )


@pytest.mark.asyncio
async def test_dynamic_subscription_verification_uses_native_detect_dto():
    item = spec(
        parameters={
            "from": "2026-09-01",
            "to": "2026-10-01",
            "timezone": "Asia/Shanghai",
            "account_id": "1",
        }
    ).model_dump()
    engine, store, tools, _ = system(AgentSubmit.model_validate({"tasks": [item]}).model_dump())
    original = tools.execute
    seen = []

    async def duplicate(*args):
        seen.append(args[3])
        value = await original(*args)
        if args[3]["agent_type"] == "billing":
            value["next_actions"] = [{"type": "verify_subscriptions"}]
        return value

    tools.execute = duplicate
    result = await engine.run("42", store.task["task_id"], "bearer", "trace")
    assert result["status"] == "succeeded" and len(seen) == 2
    assert seen[-1]["tool"] == "detect_subscriptions"
    assert set(seen[-1]["parameters"]) == {"from", "to", "account_id"}


@pytest.mark.asyncio
async def test_retry_read_failure_is_finite_and_recovers():
    engine, store, tools, _ = system(
        AgentSubmit.model_validate({"tasks": [spec().model_dump()]}).model_dump()
    )
    tools.fail = 1
    result = await engine.run("42", store.task["task_id"], "bearer", "trace")
    assert result["status"] == "succeeded" and tools.called == ["billing", "billing"]


@pytest.mark.asyncio
async def test_confirmation_commit_before_process_loss_recovers_existing_decision():
    item = spec(
        key="prepare",
        agent_type="supervisor",
        tool="require_gateway",
        skill_id="operation_preparation",
        execution_mode="intent",
        parameters={"kind": "transfer", "parameters": {"amount_minor": 100}},
    ).model_dump()
    engine, store, tools, _ = system(AgentSubmit.model_validate({"tasks": [item]}).model_dump())
    await engine.run("42", store.task["task_id"], "bearer", "trace")
    store.task["status"], store.approval = "queued", "approve"
    result = await engine.run("42", store.task["task_id"], "fresh-bearer", "trace")
    assert result["status"] == "succeeded" and tools.called == ["prepare"]


@pytest.mark.asyncio
async def test_rejected_prepare_cancels_without_tool_call():
    item = spec(
        key="prepare",
        agent_type="supervisor",
        tool="require_gateway",
        skill_id="operation_preparation",
        execution_mode="intent",
        parameters={"kind": "transfer", "parameters": {"amount_minor": 100}},
    ).model_dump()
    engine, store, tools, _ = system(AgentSubmit.model_validate({"tasks": [item]}).model_dump())
    await engine.run("42", store.task["task_id"], "bearer", "trace")
    store.task["status"], store.approval = "queued", "reject"
    result = await engine.run("42", store.task["task_id"], "fresh-bearer", "trace")
    assert result["status"] == "cancelled" and not tools.called


@pytest.mark.asyncio
async def test_timeout_saves_interrupt_and_unknown_call_outcome():
    engine, store, tools, _ = system(
        AgentSubmit.model_validate({"tasks": [spec().model_dump()], "timeout_ms": 100}).model_dump()
    )

    async def slow(*_):
        await asyncio.sleep(1)

    tools.execute = slow
    result = await engine.run("42", store.task["task_id"], "bearer", "trace")
    assert result["status"] == "interrupted" and result["error_code"] == "TASK_TIMEOUT"
    assert store.calls["billing"]["state"] == "unknown"


def test_references_only_read_explicit_dependency_results():
    from banking_agent.agent_graph import resolve_parameters

    result = resolve_parameters(
        {"billing_report_id": {"$ref": "billing.result.report_id"}},
        {"billing": {"result": {"report_id": "actual-report"}}},
    )
    assert result == {"billing_report_id": "actual-report"}
    with pytest.raises(DomainError):
        resolve_parameters({"value": {"$ref": "unrelated.result.amount_minor"}}, {})


@pytest.mark.asyncio
async def test_planned_task_key_cannot_collide_with_internal_planner_journal():
    engine,store,tools,_ = system(AgentSubmit(instruction='plan billing').model_dump())
    async def plan(*_args):
        assert _args[2] == store.task['task_id']
        return {'tasks':[spec(key='supervisor_plan').model_dump()], 'workflow_execute_id':'workflow'}
    tools.plan = plan
    result = await engine.run('42',store.task['task_id'],'bearer','trace')
    assert result['status'] == 'succeeded'
    assert tools.called == ['supervisor_plan']
    assert len(store.calls) == 2
    assert '__internal:supervisor_plan' in store.calls


@pytest.mark.asyncio
async def test_expired_lease_during_error_finish_returns_current_task():
    engine,store,tools,_ = system(AgentSubmit(tasks=[spec()]).model_dump())
    checks = 0
    def active(*_):
        nonlocal checks
        checks += 1
        return checks < 2
    def finish(*_args,**_kwargs):
        raise DomainError('LEASE_CONFLICT','worker lease expired',409)
    store.check_active,store.finish = active,finish
    result = await engine.run('42',store.task['task_id'],'bearer','trace')
    assert result == store.get('42',store.task['task_id'])
    assert not tools.called


@pytest.mark.asyncio
async def test_rejection_cancel_lost_lease_does_not_cancel_new_worker():
    item = spec(key='prepare',agent_type='supervisor',tool='require_gateway',skill_id='operation_preparation',execution_mode='intent',parameters={'kind':'transfer','parameters':{'amount_minor':100}}).model_dump()
    engine,store,tools,_ = system(AgentSubmit(tasks=[item]).model_dump())
    await engine.run('42',store.task['task_id'],'bearer','trace')
    store.task['status'],store.approval = 'queued','reject'
    def cancel(*_args):
        raise DomainError('TASK_VERSION_CONFLICT','new worker claimed task',409)
    store.cancel = cancel
    result = await engine.run('42',store.task['task_id'],'bearer','trace')
    assert result == store.get('42',store.task['task_id'])
    assert not tools.called


@pytest.mark.asyncio
@pytest.mark.parametrize('failure',[TimeoutError,RuntimeError])
async def test_other_error_finish_lost_lease_returns_current_task(failure):
    engine,store,tools,_ = system(AgentSubmit(tasks=[spec()]).model_dump())
    async def execute(*_args):
        raise failure('synthetic failure')
    def finish(*_args,**_kwargs):
        raise DomainError('LEASE_CONFLICT','worker lease expired',409)
    tools.execute,store.finish = execute,finish
    result = await engine.run('42',store.task['task_id'],'bearer','trace')
    assert result == store.get('42',store.task['task_id'])


@pytest.mark.asyncio
async def test_rejection_from_expired_lease_does_not_attempt_cancel():
    from banking_agent.agent_graph import RunContext
    store = TaskStore(AgentSubmit(tasks=[spec()]).model_dump())
    store.task['status'] = 'running'
    store.check_active = lambda *_args: False
    def cancel(*_args):
        raise AssertionError('expired worker must not attempt cancellation')
    store.cancel = cancel
    context = RunContext('42',store.task['task_id'],'expired-lease','bearer','trace',store,Tools(),2)
    assert await context.safe_cancel() == store.get('42',store.task['task_id'])
