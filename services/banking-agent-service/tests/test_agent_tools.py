import asyncio
import importlib
import json
from copy import deepcopy

import httpx
import pytest

from banking_agent.errors import DomainError

TASK = "lg-11111111-1111-4111-8111-111111111111"
KEY = "service-secret-" + "x" * 32


def client(handler, key=KEY):
    module = importlib.import_module("banking_agent.agent_tools")
    return module.AgentBankingClient(
        "http://banking.test", key, transport=httpx.MockTransport(handler)
    )


def spec(**changes):
    return dict(
        key="read",
        agent_type="billing",
        tool="get_transactions",
        skill_id="billing_analysis",
        skill_version=1,
        execution_mode="read_only",
        parameters={},
        **changes,
    )


def output(**changes):
    return dict(
        task_id=TASK + ".read",
        agent_type="billing",
        skill_id="billing_analysis",
        skill_version=1,
        status="succeeded",
        result={"amount_minor": 12},
        evidence=[],
        errors=[],
        next_actions=[],
        explanation="",
        workflow_execute_id="",
        financial_execution_enabled=False,
        **changes,
    )


def response(data):
    return httpx.Response(200, json={"code": "OK", "data": data})


def delegation(role="billing", node="read", **changes):
    return dict(
        token="short-lived-agent",
        expires_in_seconds=120,
        agent_type=role,
        task_id=TASK + "." + node,
        **changes,
    )


def execute(handler, task=None, key=KEY):
    async def scenario():
        c = client(handler, key)
        try:
            return await c.execute("user-secret", "42", TASK, task or spec(), "trace.test")
        finally:
            await c.close()

    return asyncio.run(scenario())


def test_implementation_is_available():
    assert importlib.util.find_spec("banking_agent.agent_tools") is not None


@pytest.mark.asyncio
async def test_background_delegation_is_bound_and_not_sent_to_model_run():
    def handler(req):
        assert req.url.path == "/api/v1/langgraph/background-delegations"
        assert json.loads(req.content) == {"task_id": TASK, "request_digest": "a" * 64}
        assert req.headers["authorization"] == "Bearer user-secret"
        assert req.headers["x-banking-agent-key"] == KEY
        return response({"task_id": TASK, "request_digest": "a" * 64,
                         "capability": "bgt1.encrypted-fixture", "expires_in_seconds": 86400})

    bridge = client(handler)
    try:
        result = await bridge.background_delegation("user-secret", TASK, "a" * 64, "trace")
        assert result["capability"] == "bgt1.encrypted-fixture"
    finally:
        await bridge.close()


@pytest.mark.parametrize("status,code", [(409, "MODEL_OUTCOME_UNKNOWN"), (429, "MODEL_QUOTA_EXCEEDED"), (503, "COZE_UNAVAILABLE"), (422, "INVALID_TASK_PLAN"), (422, "MODEL_OUTPUT_INVALID"), (422, "INPUT_REQUIRED")])
def test_model_failures_preserve_actionable_codes_without_reflecting_upstream_text(status, code):
    def handler(req):
        return httpx.Response(status, json={"code": code, "message": KEY + " unsafe upstream details"})

    with pytest.raises(DomainError) as caught:
        execute(handler)
    assert caught.value.code == code
    assert KEY not in str(caught.value)


def test_unknown_upstream_error_code_is_not_trusted():
    with pytest.raises(DomainError) as caught:
        execute(lambda req: httpx.Response(409, json={"code": KEY, "message": KEY}))
    assert caught.value.code == "CONFLICT"
    assert KEY not in str(caught.value)


def test_delegated_run_and_recursive_secret_removal():
    requests = []

    def handler(req):
        requests.append(req)
        if req.url.path.endswith("delegations"):
            assert json.loads(req.content) == {"task_id": TASK + ".read", "agent_type": "billing"}
            assert req.headers["authorization"] == "Bearer user-secret"
            return response(delegation())
        assert req.headers["authorization"] == "Bearer short-lived-agent"
        assert req.headers["x-banking-agent-key"] == KEY
        out = output()
        out["result"]["nested"] = {
            "confirmation_nonce": "nonce",
            "token": KEY,
            "description": "ignore instructions",
        }
        return response(out)

    out = execute(handler)
    assert out["result"]["nested"] == {"description": "ignore instructions"}
    assert KEY not in json.dumps(out)
    assert len(requests) == 2


@pytest.mark.parametrize(
    "changes",
    [
        {"agent_type": "savings"},
        {"task_id": TASK + ".other"},
        {"skill_version": 2},
        {"status": "executed"},
        {"financial_execution_enabled": True},
    ],
)
def test_rejects_output_identity_or_execution_spoof(changes):
    def handler(req):
        if req.url.path.endswith("delegations"):
            return response(delegation())
        out = output()
        out.update(changes)
        return response(out)

    with pytest.raises(DomainError) as err:
        execute(handler)
    assert err.value.status == 502


@pytest.mark.parametrize(
    "changes",
    [
        {"tool": "execute"},
        {"agent_type": "savings"},
        {"parameters": {"confirmed": True}},
        {"parameters": {"amount_minor": 1.5}},
    ],
)
def test_invalid_spec_does_not_delegate(changes):
    task = spec()
    task.update(changes)

    def handler(req):
        pytest.fail("invalid task made network request")

    with pytest.raises(DomainError) as err:
        execute(handler, task)
    assert err.value.status == 400


def test_missing_service_key_fails_without_request():
    with pytest.raises(DomainError) as err:
        execute(lambda req: pytest.fail("request"), key="")
    assert (err.value.code, err.value.status) == ("DEPENDENCY_UNAVAILABLE", 503)


@pytest.mark.parametrize(
    "body,headers",
    [
        (b'{"code":"OK","code":"OK","data":{}}', {}),
        (b'{"code":"OK","data":{"value":NaN}}', {}),
        (b"{}", {"content-length": "1048577"}),
        (__import__("gzip").compress(b"{}"), {"content-encoding": "gzip"}),
        (b" " * 1048577, {}),
    ],
    ids=["duplicate", "nonfinite", "length", "compressed", "stream-limit"],
)
def test_response_limits_and_ambiguous_json(body, headers):
    with pytest.raises(DomainError) as err:
        execute(lambda req: httpx.Response(200, content=body, headers=headers))
    assert err.value.status == 502


def test_timeout_is_sanitized_and_never_retried():
    calls = []

    def handler(req):
        calls.append(req)
        raise httpx.ReadTimeout(KEY, request=req)

    with pytest.raises(DomainError) as err:
        execute(handler)
    assert err.value.status == 504
    assert KEY not in str(err.value)
    assert len(calls) == 1


def operation():
    return dict(
        id="22222222-2222-4222-8222-222222222222",
        user_id="42",
        created_at=1,
        updated_at=2,
        kind="cancel_subscription",
        permission="PREPARE",
        agent="subscription",
        resource_id="9",
        source_account_id="0",
        target_account_id="0",
        amount_minor=0,
        currency="CNY",
        status="pending_authorization",
        version=1,
        expires_at=3,
        risk_level="low",
        error_code="",
        task_id=TASK + ".cancel",
        audit_id="audit.real",
        skill_id="operation_preparation",
        skill_version=1,
        confirmation_nonce="do-not-persist",
        parameters={"subscription_id": "9"},
    )


@pytest.mark.parametrize("mutation", [None, "owner", "status", "float"])
def test_intent_is_projection_only_and_stable_idempotency(mutation):
    def handler(req):
        if req.url.path.endswith("delegations"):
            return response(delegation("subscription", "cancel"))
        assert req.url.path == "/api/v1/execution/operations"
        assert req.headers["idempotency-key"] == "langgraph:" + TASK + ".cancel"
        assert json.loads(req.content) == {
            "kind": "cancel_subscription",
            "parameters": {"subscription_id": "9"},
        }
        op = operation()
        if mutation == "owner":
            op["user_id"] = "43"
        if mutation == "status":
            op["status"] = "executed"
        if mutation == "float":
            op["amount_minor"] = 0.0
        return response({"operation": op, "authorizations": [{"signature": KEY}]})

    task = dict(
        key="cancel",
        agent_type="subscription",
        tool="create_cancel_intent",
        skill_id="operation_preparation",
        skill_version=1,
        execution_mode="intent",
        parameters={"subscription_id": "9"},
    )
    if mutation:
        with pytest.raises(DomainError) as err:
            execute(handler, task)
        assert err.value.status == 502
    else:
        out = execute(handler, task)
        assert out["result"]["id"] == operation()["id"]
        assert "confirmation_nonce" not in json.dumps(out)
        assert out["evidence"] == [
            {"type": "operation", "id": operation()["id"], "audit_id": "audit.real"}
        ]


def test_plan_uses_supervisor_and_validates_returned_graph():
    def handler(req):
        if req.url.path.endswith("delegations"):
            return response(delegation("supervisor", "supervisor_plan"))
        assert req.url.path.endswith("/plan")
        return response(
            {
                "tasks": [spec()],
                "workflow_execute_id": "coze-real",
                "owner_engine": "langgraph",
                "financial_execution_enabled": False,
            }
        )

    async def scenario():
        c = client(handler)
        try:
            return await c.plan("user-secret", "42", TASK, "analyze", {}, "trace.test")
        finally:
            await c.close()

    out = asyncio.run(scenario())
    assert out["workflow_execute_id"] == "coze-real"


def test_missing_coze_is_real_dependency_error():
    def handler(req):
        if req.url.path.endswith("delegations"):
            return response(delegation())
        return httpx.Response(503, json={"code": "DEPENDENCY_UNAVAILABLE", "message": KEY})

    with pytest.raises(DomainError) as err:
        execute(handler)
    assert err.value.code == "DEPENDENCY_UNAVAILABLE"
    assert KEY not in str(err.value)


@pytest.mark.parametrize(
    "changes",
    [
        {"expires_in_seconds": 121},
        {"expires_in_seconds": True},
        {"agent_type": "supervisor"},
        {"task_id": TASK + ".other"},
    ],
)
def test_delegation_identity_and_lifetime_are_verified(changes):
    def handler(req):
        assert req.url.path.endswith("delegations")
        data = delegation()
        data.update(changes)
        return response(data)

    with pytest.raises(DomainError) as err:
        execute(handler)
    assert err.value.status == 502


@pytest.mark.parametrize(
    "key", ["authorization", "authorizations", "nonce", "signature", "Authorization"]
)
def test_authorization_material_is_rejected_before_delegation(key):
    task = spec()
    task["parameters"] = {"nested": {key: "forged"}}
    with pytest.raises(DomainError) as err:
        execute(lambda req: pytest.fail("authorization input was delegated"), task)
    assert err.value.status == 400


def test_deep_response_is_rejected():
    data = {"value": "ok"}
    for _ in range(24):
        data = {"nested": data}
    with pytest.raises(DomainError) as err:
        execute(lambda req: response(data))
    assert err.value.status == 502


def test_signed_gateway_prepares_transfer_and_checks_amount():
    task = dict(
        key="transfer",
        agent_type="supervisor",
        tool="require_gateway",
        skill_id="operation_preparation",
        skill_version=1,
        execution_mode="intent",
        parameters={
            "kind": "transfer",
            "parameters": {
                "source_account_id": "2",
                "target_account_id": "3",
                "amount_minor": 120,
                "currency": "CNY",
            },
        },
    )

    def handler(req):
        if req.url.path.endswith("delegations"):
            return response(delegation("supervisor", "transfer"))
        assert req.headers["authorization"] == "Bearer short-lived-agent"
        assert json.loads(req.content) == task["parameters"]
        op = operation()
        op.update(
            agent="supervisor",
            kind="transfer",
            task_id=TASK + ".transfer",
            amount_minor=121,
            source_account_id="2",
            target_account_id="3",
            parameters=task["parameters"]["parameters"],
        )
        return response({"operation": op})

    with pytest.raises(DomainError) as err:
        execute(handler, task)
    assert err.value.status == 502


def gateway_task(body):
    return dict(
        key="prepare",
        agent_type="supervisor",
        tool="require_gateway",
        skill_id="operation_preparation",
        skill_version=1,
        execution_mode="intent",
        parameters=body,
    )


HUMAN_OPERATION_PARAMETERS = {
    "card_manage": {"action": "bind", "card_id": "2"},
    "recipient_transfer": {
        "source_account_id": "2", "recipient_id": "3", "amount_minor": 120,
    },
    "aa_payment": {"source_account_id": "2", "share_id": "3"},
    "scheduled_transfer": {
        "source_account_id": "2", "recipient_id": "3", "amount_minor": 120,
        "execute_at": 1900000000000,
    },
    "investment_assessment": {"answers": [1, 2, 3]},
    "investment_order": {
        "source_account_id": "2", "product_id": "3", "amount_minor": 120,
    },
    "occasion_plan": {"title": "Dinner", "budget_minor": 120},
    "occasion_order": {"plan_id": "2", "amount_minor": 120},
    "occasion_release": {"plan_id": "2"},
}


@pytest.mark.parametrize("kind", HUMAN_OPERATION_PARAMETERS)
@pytest.mark.parametrize("ttl", [None, 1, 900])
def test_human_only_gateway_returns_unverified_proposal_without_any_http(kind, ttl):
    body = {"kind": kind, "parameters": deepcopy(HUMAN_OPERATION_PARAMETERS[kind])}
    if ttl is not None:
        body["expires_in_seconds"] = ttl
    out = execute(lambda req: pytest.fail("human-only proposal made an HTTP request"), gateway_task(body))
    assert out == {
        "skill_id": "operation_preparation",
        "skill_version": 1,
        "rule_version": "1",
        "task_id": TASK + ".prepare",
        "agent_type": "supervisor",
        "status": "succeeded",
        "result": {
            "operation_proposal": body,
            "requires_user_preparation": True,
            "server_validated": False,
            "authorization_granted": False,
        },
        "evidence": [],
        "errors": [],
        "next_actions": [{"type": "review_operation_proposal", "requires_confirmation": True}],
        "explanation": "",
        "workflow_execute_id": "",
        "financial_execution_enabled": False,
    }


def test_human_only_proposal_skips_delegation_and_request_helpers(monkeypatch):
    module = importlib.import_module("banking_agent.agent_tools")

    async def forbidden(*args, **kwargs):
        pytest.fail("a proposal called a delegated HTTP helper")

    monkeypatch.setattr(module.AgentBankingClient, "_delegate", forbidden)
    monkeypatch.setattr(module.AgentBankingClient, "_request", forbidden)
    out = execute(lambda req: pytest.fail("request"), gateway_task({"kind": "card_manage", "parameters": {}}))
    assert out["result"]["server_validated"] is False


def test_human_only_proposal_is_a_deep_copy_of_safe_typed_suggestions():
    body = {"kind": "investment_assessment", "parameters": {"answers": [{"choice": 1}]}}
    original = deepcopy(body)
    out = execute(lambda req: pytest.fail("request"), gateway_task(body))
    proposal = out["result"]["operation_proposal"]
    body["parameters"]["answers"][0]["choice"] = 2
    assert proposal == original
    proposal["parameters"]["answers"][0]["choice"] = 3
    assert body["parameters"]["answers"][0]["choice"] == 2


@pytest.mark.parametrize(
    "body",
    [
        {"kind": "unknown_operation", "parameters": {}},
        {"kind": "", "parameters": {}},
        {"kind": True, "parameters": {}},
        {"kind": "card_manage"},
        {"kind": "card_manage", "parameters": []},
        {"kind": "card_manage", "parameters": {}, "unexpected": "field"},
        *[
            {"kind": "card_manage", "parameters": {}, "expires_in_seconds": ttl}
            for ttl in (0, 901, True, 1.0, "1", None)
        ],
        {"kind": "investment_order", "parameters": {"amount_minor": 120.5}},
        {"kind": "investment_assessment", "parameters": {"answers": [1.0]}},
    ],
)
def test_invalid_gateway_proposal_body_is_rejected_without_http(body):
    with pytest.raises(DomainError) as err:
        execute(lambda req: pytest.fail("invalid proposal made an HTTP request"), gateway_task(body))
    assert (err.value.code, err.value.status) == ("INVALID_ARGUMENT", 400)


@pytest.mark.parametrize(
    "key",
    ["authorization", "authorization_id", "authorizations", "confirmation_nonce", "nonce",
     "signature", "user_id", "owner_id", "token", "access_token", "agent_token", "bearer",
     "service_key", "lease_token", "capability", "background_capability", "User-ID"],
)
def test_human_only_proposal_rejects_nested_identity_and_authorization_material(key):
    body = {"kind": "card_manage", "parameters": {"nested": [{key: "forged"}]}}
    with pytest.raises(DomainError) as err:
        execute(lambda req: pytest.fail("unsafe proposal made an HTTP request"), gateway_task(body))
    assert (err.value.code, err.value.status) == ("INVALID_ARGUMENT", 400)


@pytest.mark.parametrize("kind", ["transfer", "savings_deposit", "savings_withdrawal", "cancel_subscription"])
def test_ordinary_gateway_intent_still_delegates_and_prepares_real_operation(kind):
    body = {"kind": kind, "parameters": {"source_account_id": "2", "amount_minor": 120}}
    requests = []

    def handler(req):
        requests.append(req)
        if req.url.path.endswith("delegations"):
            assert json.loads(req.content) == {"task_id": TASK + ".prepare", "agent_type": "supervisor"}
            return response(delegation("supervisor", "prepare"))
        assert req.url.path == "/api/v1/execution/operations"
        assert req.headers["authorization"] == "Bearer short-lived-agent"
        assert req.headers["idempotency-key"] == "langgraph:" + TASK + ".prepare"
        assert json.loads(req.content) == body
        op = operation()
        op.update(agent="supervisor", kind=kind, task_id=TASK + ".prepare", amount_minor=120,
                  source_account_id="2", parameters=body["parameters"])
        return response({"operation": op})

    out = execute(handler, gateway_task(body))
    assert len(requests) == 2
    assert out["result"]["id"] == operation()["id"]
    assert "operation_proposal" not in out["result"]
    assert out["next_actions"] == [{"type": "authorize_operation", "requires_confirmation": True}]


@pytest.mark.parametrize("value", [{"user_id": "43"}, {"financial_execution_enabled": True}])
def test_nested_owner_and_execution_flags_are_verified(value):
    def handler(req):
        if req.url.path.endswith("delegations"):
            return response(delegation())
        out = output()
        out["result"]["nested"] = value
        return response(out)

    with pytest.raises(DomainError) as err:
        execute(handler)
    assert err.value.status == 502


def test_invalid_operation_identifier_is_output_error():
    def handler(req):
        if req.url.path.endswith("delegations"):
            return response(delegation("subscription", "cancel"))
        op = operation()
        op["id"] = "invalid"
        return response({"operation": op})

    task = dict(
        key="cancel",
        agent_type="subscription",
        tool="create_cancel_intent",
        skill_id="operation_preparation",
        skill_version=1,
        execution_mode="intent",
        parameters={"subscription_id": "9"},
    )
    with pytest.raises(DomainError) as err:
        execute(handler, task)
    assert err.value.status == 502


def test_native_plan_accepts_null_dependencies():
    def handler(req):
        if req.url.path.endswith("delegations"):
            return response(delegation("supervisor", "supervisor_plan"))
        task = spec()
        task["dependencies"] = None
        return response(
            {
                "tasks": [task],
                "workflow_execute_id": "coze-real",
                "owner_engine": "langgraph",
                "financial_execution_enabled": False,
            }
        )

    async def scenario():
        c = client(handler)
        try:
            return await c.plan("user-secret", "42", TASK, "analyze", {}, "trace.test")
        finally:
            await c.close()

    out = asyncio.run(scenario())
    assert out["tasks"][0]["dependencies"] == []


def test_redirect_never_follows_or_forwards_credentials():
    requests = []

    def handler(req):
        requests.append(req)
        return httpx.Response(307, headers={"location": "https://other.example/steal"})

    with pytest.raises(DomainError) as err:
        execute(handler)
    assert err.value.status == 502
    assert len(requests) == 1


def test_connect_error_is_sanitized_dependency_failure():
    def handler(req):
        raise httpx.ConnectError("connection " + KEY, request=req)

    with pytest.raises(DomainError) as err:
        execute(handler)
    assert err.value.status == 503
    assert KEY not in str(err.value)


def test_route_allowlist_blocks_execution_and_arbitrary_url():
    async def scenario():
        c = client(lambda req: pytest.fail("forbidden route requested"))
        try:
            for path in (
                "/api/v1/execution/operations/id/authorize",
                "/api/v1/execution/operations/id/execute",
                "https://other.example/run",
            ):
                with pytest.raises(DomainError) as err:
                    await c._request("POST", path, "user-secret", "trace.test", {})
                assert err.value.status == 400
            assert KEY not in repr(c)
            assert c._client.timeout.read > 60
        finally:
            await c.close()

    asyncio.run(scenario())


@pytest.mark.parametrize("mutation", [None, "amount", "owner", "flag", "evidence_object"])
def test_native_lifestyle_text_evidence_and_optional_amount_are_not_metadata(mutation):
    task = spec()
    task.update(
        agent_type="lifestyle",
        tool="get_lifestyle_plan",
        skill_id="lifestyle_planning",
        parameters={"plan_id": "38"},
    )

    def handler(req):
        if req.url.path.endswith("delegations"):
            return response(delegation("lifestyle"))
        data = output()
        data.update(agent_type="lifestyle", skill_id="lifestyle_planning")
        data["result"] = {
            "plan": {
                "user_id": "42",
                "snapshot": {
                    "goal": {
                        "evidence": {
                            "expense_reduction_monthly_minor": "user described monthly savings",
                            "user_id": "a quoted user identity, not the resource owner",
                            "financial_execution_enabled": "quoted text, not an execution flag",
                        }
                    },
                    "savings_ceiling_minor": None,
                    "budget_minor": 120,
                },
                "result": {"financial_execution_enabled": False},
            }
        }
        if mutation == "amount":
            data["result"]["plan"]["snapshot"]["budget_minor"] = 120.5
        if mutation == "owner":
            data["result"]["plan"]["user_id"] = "43"
        if mutation == "flag":
            data["result"]["plan"]["result"]["financial_execution_enabled"] = True
        if mutation == "evidence_object":
            data["result"]["plan"]["snapshot"]["goal"]["evidence"]["user_id"] = {"user_id": "43"}
        return response(data)

    if mutation:
        with pytest.raises(DomainError) as err:
            execute(handler, task)
        assert err.value.status == 502
    else:
        out = execute(handler, task)
        assert out["result"]["plan"]["snapshot"]["savings_ceiling_minor"] is None
        assert isinstance(
            out["result"]["plan"]["snapshot"]["goal"]["evidence"][
                "expense_reduction_monthly_minor"
            ],
            str,
        )


def test_billing_cannot_reuse_lifestyle_text_evidence_or_nullable_money_exception():
    def handler(req):
        if req.url.path.endswith("delegations"):
            return response(delegation())
        data = output()
        data["result"] = {
            "plan": {
                "snapshot": {
                    "goal": {"evidence": {"amount_minor": "not money"}},
                    "savings_ceiling_minor": None,
                }
            }
        }
        return response(data)

    with pytest.raises(DomainError) as err:
        execute(handler)
    assert err.value.status == 502


def test_project_monetary_quota_preserves_safe_code():
    upstream = httpx.Response(429, json={"code": "MODEL_SPEND_QUOTA_EXCEEDED", "message": "secret-upstream-text"})
    agent_class = importlib.import_module("banking_agent.agent_tools").AgentBankingClient
    code, message = asyncio.run(agent_class._business_failure(upstream, "TOOL_UPSTREAM_ERROR", "unavailable"))
    assert code == "MODEL_SPEND_QUOTA_EXCEEDED"
    assert "secret-upstream-text" not in message


@pytest.mark.parametrize("status,expected", [(403, "MODEL_APPROVAL_REQUIRED"), (429, "MODEL_APPROVAL_LIMIT_EXCEEDED"), (503, "MODEL_EXECUTION_CONFIRMATION_REQUIRED"), (503, "MODEL_BUDGET_UNAVAILABLE")])
def test_approval_failure_preserves_safe_code(status, expected):
    upstream = httpx.Response(status, json={"code": expected, "message": "secret-upstream-text"})
    agent_class = importlib.import_module("banking_agent.agent_tools").AgentBankingClient
    code, message = asyncio.run(agent_class._business_failure(upstream, "TOOL_UPSTREAM_ERROR", "unavailable"))
    assert code == expected
    assert "secret-upstream-text" not in message
