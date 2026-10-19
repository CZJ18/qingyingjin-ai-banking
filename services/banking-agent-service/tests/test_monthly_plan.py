"""Monthly plan acceptance must be a server-enforced, immutable final review."""

import copy
from datetime import datetime
from zoneinfo import ZoneInfo

import pytest
from pydantic import ValidationError
from test_agent_graph import system as graph_system
from test_agent_models import spec

from banking_agent.agent_models import AgentSubmit


def month():
    return datetime.now(ZoneInfo("Asia/Shanghai")).strftime("%Y-%m")


def metadata(**changes):
    return {"period": month(), "currency": "CNY", "timezone": "Asia/Shanghai"} | changes


def plan_spec(**changes):
    return spec(
        **(
            {
                "key": "plan",
                "agent_type": "lifestyle",
                "tool": "get_lifestyle_plan",
                "skill_id": "lifestyle_planning",
                "execution_mode": "read_only",
                "parameters": {"plan_id": "1"},
            }
            | changes
        )
    ).model_dump()


def financial_result(**changes):
    return {
        "result": {
            "currency": "CNY",
            "period": month(),
            "blockers": [],
            "options": [
                {
                    "name": "balanced",
                    "feasible": True,
                    "savings_minor": 20000,
                    "retained_minor": 80000,
                    "allocations": [],
                    "explanation": "Keep reserves.",
                }
            ],
            "financial_execution_enabled": False,
        }
        | changes,
        "financial_execution_enabled": False,
    }


def lifestyle_result(**changes):
    return {
        "plan": {
            "id": "1",
            "expires_at": int(datetime.now(ZoneInfo("Asia/Shanghai")).timestamp() * 1000) + 600_000,
            "result": {
                "status": "ready",
                "missing_information": [],
                "conflicts": [],
                "options": [
                    {
                        "name": "balanced",
                        "budget_minor": 100000,
                        "expense_reduction_minor": 20000,
                        "savings_monthly_minor": 0,
                        "categories": [],
                        "explanation": "Reduce optional costs.",
                    }
                ],
                "financial_execution_enabled": False,
            }
            | changes,
        }
    }


def monthly_request(tasks=None, **changes):
    return AgentSubmit.model_validate(
        {"tasks": tasks or [plan_spec()], "monthly_goal": metadata()} | changes
    ).model_dump()


def system(request):
    engine, store, tools, memory = graph_system(request)
    execute = tools.execute

    async def typed_plans(*args):
        output = await execute(*args)
        item = args[3]
        if item["agent_type"] == "lifestyle" and item["tool"] in {
            "get_lifestyle_plan",
            "compile_lifestyle_plan",
        }:
            output["result"] = lifestyle_result()
        elif item["agent_type"] == "savings" and item["tool"] in {
            "get_financial_plan",
            "compare_financial_plans",
            "replan_financial_plan",
        }:
            output["result"] = financial_result()
        return output

    tools.execute = typed_plans
    return engine, store, tools, memory


def intent(**changes):
    return spec(
        key="prepare",
        agent_type="supervisor",
        tool="require_gateway",
        skill_id="operation_preparation",
        execution_mode="intent",
        parameters={"kind": "transfer", "parameters": {"amount_minor": 100}},
        **changes,
    ).model_dump()


def confirm(store, decision="approve"):
    challenge = copy.deepcopy(store.task["confirmation"])
    store.task["status"], store.approval = "queued", decision
    return {"decision": decision, "node_key": challenge["node_key"], "digest": challenge["digest"]}


def test_monthly_goal_contract_accepts_only_current_shanghai_cny_month():
    assert monthly_request()["monthly_goal"] == metadata()
    for changes in (
        {"period": "2000-01"},
        {"period": "2026-1"},
        {"currency": "USD"},
        {"timezone": "UTC"},
        {"owner_id": "forged"},
    ):
        with pytest.raises(ValidationError):
            monthly_request(monthly_goal=metadata(**changes))


def test_reserved_review_key_and_side_effect_dependent_suggestions_are_rejected():
    with pytest.raises(ValidationError):
        monthly_request([spec(key="monthly_plan_review").model_dump()])
    with pytest.raises(ValidationError):
        monthly_request([intent(), spec(dependencies=["prepare"]).model_dump()])
    with pytest.raises(ValidationError):
        monthly_request([intent()])


@pytest.mark.asyncio
async def test_monthly_read_advice_complete_before_server_final_acceptance_and_prepare():
    advice = spec(
        key="advice",
        agent_type="lifestyle",
        tool="compile_lifestyle_plan",
        skill_id="lifestyle_planning",
        execution_mode="advice",
        parameters={"report": {"$ref": "billing.result.report_id"}},
        dependencies=["billing"],
    ).model_dump()
    early = spec(
        key="early",
        agent_type="supervisor",
        tool="request_confirmation",
        skill_id="task_planning",
        execution_mode="user_confirmation",
        parameters={},
        priority=0,
    ).model_dump()
    request = monthly_request([intent(priority=0), early, spec().model_dump(), advice])
    engine, store, tools, memory = system(request)
    waiting = await engine.run("42", store.task["task_id"], "secret-user-bearer", "trace")
    assert waiting["status"] == "waiting_confirmation"
    assert tools.called == ["billing", "advice"]
    challenge = waiting["confirmation"]
    assert challenge["node_key"] == "monthly_plan_review"
    view = challenge["parameters_view"]
    assert view["confirmation_scope"] == "PLAN_ACCEPTANCE"
    assert view["monthly_goal"] == metadata() and view["goal_text"] == ""
    assert view["plan_tasks"] == request["tasks"]
    assert set(view["suggestions"]) == {"billing", "advice"}
    from banking_agent.agent_graph import digest

    assert view["source_digest"] == digest(view["suggestions"])
    assert challenge["digest"] == digest(view)
    assert "secret-user-bearer" not in repr(memory.storage)
    result = await engine.run(
        "42", store.task["task_id"], "fresh-bearer", "trace", resume=confirm(store)
    )
    assert result["status"] == "waiting_confirmation"
    assert tools.called == ["billing", "advice"]
    assert result["confirmation"]["node_key"] in {"prepare", "early"}


@pytest.mark.asyncio
async def test_monthly_acceptance_persists_across_engine_reconstruction_without_reexecuting_sources():
    from banking_agent.agent_graph import LangGraphOrchestrator

    engine, store, tools, memory = system(monthly_request())
    await engine.run("42", store.task["task_id"], "bearer", "trace")
    confirm(store)
    restarted = LangGraphOrchestrator(store, tools, saver_factory=lambda *_a, **_kw: memory)
    result = await restarted.run("42", store.task["task_id"], "fresh-bearer", "trace")
    assert result["status"] == "succeeded" and tools.called == ["plan"]
    output = next(
        row
        for row in result["result"]["outputs"]
        if row["task_id"].endswith(".monthly_plan_review")
    )
    assert output["result"]["plan_accepted"] is True
    assert output["result"]["authorization_granted"] is False
    assert output["financial_execution_enabled"] is False


@pytest.mark.asyncio
async def test_monthly_reject_cancels_without_intent():
    engine, store, tools, _ = system(monthly_request([plan_spec(), intent()]))
    await engine.run("42", store.task["task_id"], "bearer", "trace")
    result = await engine.run(
        "42", store.task["task_id"], "bearer", "trace", resume=confirm(store, "reject")
    )
    assert result["status"] == "cancelled" and tools.called == ["plan"]


@pytest.mark.asyncio
async def test_monthly_instruction_cannot_omit_final_review_or_forge_reserved_node():
    for forged in (False, True):
        request = AgentSubmit(instruction="本月节省200元", monthly_goal=metadata()).model_dump()
        engine, store, tools, _ = system(request)

        async def plan(*_args, forged=forged):
            return {"tasks": [plan_spec() | {"key": "monthly_plan_review" if forged else "plan"}]}

        tools.plan = plan
        result = await engine.run("42", store.task["task_id"], "bearer", "trace")
        assert result["status"] == ("failed" if forged else "waiting_confirmation")
        if not forged:
            assert result["confirmation"]["parameters_view"]["goal_text"] == request["instruction"]
            assert result["confirmation"]["node_key"] == "monthly_plan_review"
        else:
            assert not tools.called


@pytest.mark.asyncio
async def test_monthly_tool_claimed_confirmation_does_not_accept_plan():
    engine, store, tools, _ = system(monthly_request())
    execute = tools.execute

    async def claims_approval(*args):
        result = await execute(*args)
        result["result"]["confirmed"] = True
        result["result"]["plan_accepted"] = True
        return result

    tools.execute = claims_approval
    result = await engine.run("42", store.task["task_id"], "bearer", "trace")
    assert result["status"] == "waiting_confirmation" and store.approval is None


@pytest.mark.asyncio
async def test_monthly_unusable_suggestion_fails_closed_before_review():
    engine, store, tools, _ = system(monthly_request([plan_spec(), intent()]))
    execute = tools.execute

    async def failed_output(*args):
        result = await execute(*args)
        result["status"] = "failed"
        return result

    tools.execute = failed_output
    result = await engine.run("42", store.task["task_id"], "bearer", "trace")
    assert result["status"] == "failed" and result["error_code"] == "TOOL_OUTPUT_INVALID"
    assert tools.called == ["plan"] and result["confirmation"] is None


@pytest.mark.asyncio
async def test_monthly_runtime_month_rollover_blocks_previously_valid_request(monkeypatch):
    engine, store, tools, _ = system(monthly_request())
    from banking_agent import monthly_plan

    monkeypatch.setattr(monthly_plan, "current_period", lambda: "2099-01")
    result = await engine.run("42", store.task["task_id"], "bearer", "trace")
    assert result["status"] == "failed" and result["error_code"] == "MONTHLY_GOAL_EXPIRED"
    assert not tools.called


@pytest.mark.asyncio
async def test_monthly_limit_has_room_for_server_review():
    request = monthly_request([plan_spec(key=f"read{index}") for index in range(32)])
    engine, store, tools, _ = system(request)
    waiting = await engine.run("42", store.task["task_id"], "bearer", "trace")
    assert waiting["status"] == "waiting_confirmation" and len(tools.called) == 32
    assert len(waiting["confirmation"]["parameters_view"]["plan_tasks"]) == 32
    result = await engine.run("42", store.task["task_id"], "bearer", "trace", resume=confirm(store))
    assert result["status"] == "succeeded" and len(tools.called) == 32


@pytest.mark.asyncio
async def test_monthly_wrong_digest_resume_does_not_record_acceptance():
    engine, store, tools, _ = system(monthly_request([plan_spec(), intent()]))
    await engine.run("42", store.task["task_id"], "bearer", "trace")
    decision = confirm(store)
    decision["digest"] = "0" * 64
    result = await engine.run("42", store.task["task_id"], "bearer", "trace", resume=decision)
    assert result["status"] == "failed" and result["error_code"] == "CONFIRMATION_CONFLICT"
    assert tools.called == ["plan"]


@pytest.mark.asyncio
async def test_monthly_approval_must_exist_in_repository_and_not_just_graph_command():
    engine, store, tools, _ = system(monthly_request())
    waiting = await engine.run("42", store.task["task_id"], "bearer", "trace")
    challenge = waiting["confirmation"]
    store.task["status"] = "queued"
    result = await engine.run(
        "42",
        store.task["task_id"],
        "bearer",
        "trace",
        resume={
            "node_key": challenge["node_key"],
            "digest": challenge["digest"],
            "decision": "approve",
        },
    )
    assert result["status"] == "failed" and result["error_code"] == "CONFIRMATION_EXPIRED"
    assert tools.called == ["plan"]


@pytest.mark.asyncio
async def test_monthly_request_changed_after_checkpoint_fails_instead_of_regrouping():
    engine, store, tools, _ = system(monthly_request())
    await engine.run("42", store.task["task_id"], "bearer", "trace")
    confirm(store)
    store.task["request"]["tasks"][0]["parameters"]["account_id"] = "other-source"
    result = await engine.run("42", store.task["task_id"], "bearer", "trace")
    assert result["status"] == "failed" and result["error_code"] == "CONFIRMATION_CONFLICT"
    assert tools.called == ["plan"]


@pytest.mark.asyncio
async def test_monthly_rollover_after_review_rejects_resume_before_any_intent(monkeypatch):
    engine, store, tools, _ = system(monthly_request([plan_spec(), intent()]))
    await engine.run("42", store.task["task_id"], "bearer", "trace")
    decision = confirm(store)
    from banking_agent import monthly_plan

    monkeypatch.setattr(monthly_plan, "current_period", lambda: "2099-01")
    result = await engine.run("42", store.task["task_id"], "bearer", "trace", resume=decision)
    assert result["status"] == "failed" and result["error_code"] == "MONTHLY_GOAL_EXPIRED"
    assert tools.called == ["plan"]


@pytest.mark.asyncio
async def test_monthly_dynamic_subscription_evidence_is_in_review_and_stays_frozen():
    engine, store, tools, _ = system(
        monthly_request([spec().model_dump(), plan_spec(dependencies=["billing"]), intent()])
    )
    execute = tools.execute

    async def subscriptions(*args):
        output = await execute(*args)
        if args[3]["key"] == "billing":
            output["next_actions"] = [{"type": "verify_subscriptions"}]
        return output

    tools.execute = subscriptions
    waiting = await engine.run("42", store.task["task_id"], "bearer", "trace")
    view = copy.deepcopy(waiting["confirmation"]["parameters_view"])
    assert sorted(tools.called) == ["billing", "plan", "verify_subscriptions"]
    assert tools.called[0] == "billing"
    assert {task["key"] for task in view["plan_tasks"]} == {
        "billing",
        "plan",
        "verify_subscriptions",
        "prepare",
    }
    assert set(view["suggestions"]) == {"billing", "plan", "verify_subscriptions"}
    prepared = await engine.run(
        "42", store.task["task_id"], "bearer", "trace", resume=confirm(store)
    )
    assert (
        prepared["status"] == "waiting_confirmation"
        and prepared["confirmation"]["node_key"] == "prepare"
    )
    complete = await engine.run(
        "42", store.task["task_id"], "bearer", "trace", resume=confirm(store)
    )
    assert complete["status"] == "succeeded"
    assert sorted(tools.called) == ["billing", "plan", "prepare", "verify_subscriptions"]
    assert tools.called[0] == "billing" and tools.called[-1] == "prepare"
    accepted = next(
        row
        for row in complete["result"]["outputs"]
        if row["task_id"].endswith(".monthly_plan_review")
    )
    from banking_agent.agent_graph import digest

    assert accepted["result"]["plan_digest"] == digest(view)


@pytest.mark.asyncio
async def test_monthly_planner_cannot_emit_suggestions_that_require_intent():
    request = AgentSubmit(
        instruction="This month save 200 CNY", monthly_goal=metadata()
    ).model_dump()
    engine, store, tools, _ = system(request)

    async def plan(*_):
        return {"tasks": [intent(), spec(dependencies=["prepare"]).model_dump()]}

    tools.plan = plan
    result = await engine.run("42", store.task["task_id"], "bearer", "trace")
    assert result["status"] == "failed" and result["error_code"] == "TOOL_OUTPUT_INVALID"
    assert not tools.called


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "change", [{"skill_id": "forged"}, {"skill_version": 2}, {"skill_version": True}]
)
async def test_monthly_suggestions_must_match_bound_skill(change):
    engine, store, tools, _ = system(monthly_request())
    execute = tools.execute

    async def wrong_skill(*args):
        return await execute(*args) | change

    tools.execute = wrong_skill
    result = await engine.run("42", store.task["task_id"], "bearer", "trace")
    assert result["status"] == "failed" and result["error_code"] == "TOOL_OUTPUT_INVALID"
    assert result["confirmation"] is None


@pytest.mark.asyncio
@pytest.mark.parametrize("tool", ["get_account_summary", "get_transactions", "analyze_spending"])
async def test_plain_billing_sources_cannot_be_accepted_as_monthly_plan(tool):
    engine, store, tools, _ = system(monthly_request([spec(tool=tool).model_dump(), intent()]))
    result = await engine.run("42", store.task["task_id"], "bearer", "trace")
    assert result["status"] == "failed" and result["error_code"] == "PLAN_ADVICE_REQUIRED"
    assert result["confirmation"] is None and tools.called == ["billing"]


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "payload",
    [
        lifestyle_result(status="needs_information"),
        lifestyle_result(missing_information=["budget"]),
        lifestyle_result(conflicts=["target"]),
        lifestyle_result(options=[]),
        lifestyle_result(options=[{}]),
        lifestyle_result(financial_execution_enabled=True),
    ],
)
async def test_monthly_nonready_lifestyle_plan_requires_more_information(payload):
    engine, store, tools, _ = system(monthly_request([plan_spec(), intent()]))
    execute = tools.execute

    async def unavailable(*args):
        output = await execute(*args)
        output["result"] = payload
        return output

    tools.execute = unavailable
    result = await engine.run("42", store.task["task_id"], "bearer", "trace")
    assert result["status"] == "failed" and result["error_code"] == "PLAN_ADVICE_REQUIRED"
    assert result["confirmation"] is None and tools.called == ["plan"]


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "changes",
    [
        {"blockers": ["necessary expenses unknown"]},
        {"options": []},
        {"options": [{"name": "balanced", "feasible": False}]},
        {"options": [{"name": "balanced", "feasible": 1}]},
        {"period": "2000-01"},
        {"currency": "USD"},
        {"financial_execution_enabled": True},
    ],
)
async def test_monthly_unusable_financial_plan_requires_more_information(changes):
    task = spec(
        key="plan",
        agent_type="savings",
        tool="get_financial_plan",
        skill_id="savings_planning",
        execution_mode="read_only",
        parameters={"plan_id": "1"},
    ).model_dump()
    engine, store, tools, _ = system(monthly_request([task, intent()]))
    execute = tools.execute

    async def unavailable(*args):
        output = await execute(*args)
        output["result"] = financial_result(**changes)
        return output

    tools.execute = unavailable
    result = await engine.run("42", store.task["task_id"], "bearer", "trace")
    assert result["status"] == "failed" and result["error_code"] == "PLAN_ADVICE_REQUIRED"
    assert result["confirmation"] is None and tools.called == ["plan"]


@pytest.mark.asyncio
@pytest.mark.parametrize(
    "role,tool,mode,payload",
    [
        ("savings", "get_financial_plan", "read_only", financial_result()),
        ("savings", "compare_financial_plans", "read_only", financial_result()),
        ("savings", "replan_financial_plan", "advice", financial_result()),
        ("lifestyle", "get_lifestyle_plan", "read_only", lifestyle_result()),
        ("lifestyle", "compile_lifestyle_plan", "advice", lifestyle_result()),
    ],
)
async def test_monthly_accepts_ready_registered_financial_or_lifestyle_plans(
    role, tool, mode, payload
):
    task = spec(
        key="plan",
        agent_type=role,
        tool=tool,
        skill_id=role + "_planning",
        execution_mode=mode,
        parameters={"plan_id": "1"},
    ).model_dump()
    engine, store, tools, _ = system(monthly_request([task]))
    execute = tools.execute

    async def ready_plan(*args):
        output = await execute(*args)
        output["result"] = payload
        return output

    tools.execute = ready_plan
    waiting = await engine.run("42", store.task["task_id"], "bearer", "trace")
    assert waiting["status"] == "waiting_confirmation"
    assert waiting["confirmation"]["node_key"] == "monthly_plan_review"
    accepted = await engine.run(
        "42", store.task["task_id"], "bearer", "trace", resume=confirm(store)
    )
    assert accepted["status"] == "succeeded" and tools.called == ["plan"]


@pytest.mark.asyncio
@pytest.mark.parametrize("expiry", [None, 0, 1, True, "9999999999", 9999999999.0])
async def test_monthly_lifestyle_plan_requires_explicit_future_integer_expiry(expiry):
    engine, store, tools, _ = system(monthly_request([plan_spec(), intent()]))
    execute = tools.execute

    async def expired(*args):
        output = await execute(*args)
        if expiry is None:
            output["result"]["plan"].pop("expires_at", None)
        else:
            output["result"]["plan"]["expires_at"] = expiry
        return output

    tools.execute = expired
    result = await engine.run("42", store.task["task_id"], "bearer", "trace")
    assert result["status"] == "failed" and result["error_code"] == "PLAN_ADVICE_REQUIRED"
    assert result["confirmation"] is None and tools.called == ["plan"]


@pytest.mark.asyncio
async def test_monthly_billing_cannot_forge_options_to_become_a_plan():
    engine, store, tools, _ = system(monthly_request([spec().model_dump(), intent()]))
    execute = tools.execute

    async def forged(*args):
        output = await execute(*args)
        output["result"] = financial_result()
        return output

    tools.execute = forged
    result = await engine.run("42", store.task["task_id"], "bearer", "trace")
    assert result["status"] == "failed" and result["error_code"] == "PLAN_ADVICE_REQUIRED"
    assert result["confirmation"] is None and tools.called == ["billing"]


@pytest.mark.asyncio
async def test_monthly_one_ready_plan_does_not_hide_another_conflicting_plan():
    tasks = [plan_spec(), plan_spec(key="conflicting")]
    engine, store, tools, _ = system(monthly_request(tasks))
    execute = tools.execute

    async def conflicting(*args):
        output = await execute(*args)
        if args[3]["key"] == "conflicting":
            output["result"] = lifestyle_result(conflicts=["target not feasible"])
        return output

    tools.execute = conflicting
    result = await engine.run("42", store.task["task_id"], "bearer", "trace")
    assert result["status"] == "failed" and result["error_code"] == "PLAN_ADVICE_REQUIRED"
    assert result["confirmation"] is None


@pytest.mark.asyncio
@pytest.mark.parametrize("offset_ms", [-86400000, -1, 0, 1, 86400000])
async def test_monthly_lifestyle_native_millisecond_expiry_boundary(monkeypatch, offset_ms):
    from banking_agent import monthly_plan

    fixed_now = datetime.now(ZoneInfo("Asia/Shanghai"))
    now_ms = int(fixed_now.timestamp() * 1000)

    class FixedClock:
        @classmethod
        def now(cls, timezone):
            return fixed_now.astimezone(timezone)

    monkeypatch.setattr(monthly_plan, "datetime", FixedClock)
    engine, store, tools, _ = system(monthly_request([plan_spec(), intent()]))
    execute = tools.execute

    async def native_expiry(*args):
        output = await execute(*args)
        output["result"]["plan"]["expires_at"] = now_ms + offset_ms
        return output

    tools.execute = native_expiry
    result = await engine.run("42", store.task["task_id"], "bearer", "trace")
    if offset_ms > 0:
        assert result["status"] == "waiting_confirmation"
        assert result["confirmation"]["node_key"] == "monthly_plan_review"
    else:
        assert result["status"] == "failed" and result["error_code"] == "PLAN_ADVICE_REQUIRED"
        assert result["confirmation"] is None
    assert tools.called == ["plan"]
