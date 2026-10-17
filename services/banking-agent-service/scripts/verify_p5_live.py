"""Actual HTTP/SQL-backed P5 verification; operator secrets remain in memory.

Fixture setup creates a zero-funded savings goal through the existing user
gateway; P5 itself never calls authorization or financial execution routes.
"""

import argparse
import base64
import hashlib
import hmac
import json
import subprocess
import time
import urllib.error
import urllib.request
from datetime import UTC, datetime
from pathlib import Path
from uuid import uuid4

root = Path(__file__).resolve().parents[3]
out = root / "services/banking-agent-service/output/p5"
out.mkdir(parents=True, exist_ok=True)
parser = argparse.ArgumentParser()
parser.add_argument("--models", action="store_true")
parser.add_argument("--restart", action="store_true")
args = parser.parse_args()


def env(path):
    return dict(
        line.split("=", 1)
        for line in path.read_text(encoding="utf-8-sig").splitlines()
        if "=" in line and not line.startswith("#")
    )


private = env(root / "deploy/banking/.env.local")
report_path = out / (
    "live-models.json" if args.models else "live-restart.json" if args.restart else "live.json"
)
state_path = out / "live-state.json"
state = json.loads(
    (
        state_path
        if state_path.exists()
        else root / "services/banking-service/output/module11/live-state.json"
    ).read_text(encoding="utf8")
)
report = {
    "passed": False,
    "models": args.models,
    "restart": args.restart,
    "responses": {},
    "checks": {},
    "started_at": datetime.now(UTC).isoformat(),
}
if report_path.exists() and not json.loads(report_path.read_text(encoding="utf8")).get("passed"):
    report_path.rename(out / (report_path.stem + "-failed-" + str(time.time_ns()) + ".json"))


def save():
    report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf8")
    state_path.write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n", encoding="utf8")


def scrub(value):
    if isinstance(value, dict):
        return {
            key: scrub(item)
            for key, item in value.items()
            if key
            not in {"confirmation_nonce", "nonce", "token", "authorization_id", "authorizations"}
        }
    if isinstance(value, list):
        return [scrub(item) for item in value]
    return value


def token(user=None, role=None, task_id=None):
    now = int(time.time())
    claims = {
        "iss": private.get("BANKING_JWT_ISSUER", "qingyingjin"),
        "aud": [private.get("BANKING_JWT_AUDIENCE", "banking-service")],
        "sub": user or state["user_id"],
        "iat": now,
        "nbf": now,
        "exp": now + 120,
        "jti": str(uuid4()),
        "token_use": "agent" if role else "user",
    }
    if role:
        claims.update(agent_type=role, task_id=task_id or "lg-" + str(uuid4()) + ".probe")

    def encoded(value):
        return base64.urlsafe_b64encode(json.dumps(value, separators=(",", ":")).encode()).rstrip(
            b"="
        )

    raw = encoded({"alg": "HS256", "typ": "JWT"}) + b"." + encoded(claims)
    return (
        raw
        + b"."
        + base64.urlsafe_b64encode(
            hmac.new(private["BANKING_USER_JWT_SECRET"].encode(), raw, hashlib.sha256).digest()
        ).rstrip(b"=")
    ).decode()


def call(
    name, method, path, body=None, *, bank=False, expected=200, user=None, role=None, key=None
):
    origin = "http://127.0.0.1:8090" if bank else "http://127.0.0.1:8091"
    request = urllib.request.Request(
        origin + path,
        data=None if body is None else json.dumps(body, ensure_ascii=False).encode(),
        method=method,
        headers={
            "Authorization": "Bearer " + token(user, role),
            "X-Trace-ID": "p5-live-acceptance",
            "Content-Type": "application/json",
            "Idempotency-Key": key or str(uuid4()),
        },
    )
    try:
        response = urllib.request.urlopen(request, timeout=200)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        value = json.load(response)
        report["responses"][name] = {"status": response.status, "body": scrub(value)}
        save()
        assert response.status == expected, (name, response.status, value.get("code"))
        return value["data"]


def operator_setup(kind, parameters):
    value = call(
        "fixture_" + kind,
        "POST",
        "/api/v1/execution/operations",
        {"kind": kind, "parameters": parameters},
        bank=True,
        expected=201,
    )
    operation = value["operation"]
    authorized = call(
        "fixture_authorize_" + kind,
        "POST",
        f"/api/v1/execution/operations/{operation['id']}/authorize",
        {
            "decision": "approve",
            "intent_digest": operation["intent_digest"],
            "confirmation_nonce": operation["confirmation_nonce"],
        },
        bank=True,
    )
    current = authorized["operation"]
    executed = call(
        "fixture_execute_" + kind,
        "POST",
        f"/api/v1/execution/operations/{operation['id']}/execute",
        {
            "authorization_id": current["authorization_id"],
            "intent_digest": current["intent_digest"],
            "expected_version": current["version"],
        },
        bank=True,
    )
    assert executed["operation"]["status"] == "succeeded"
    return executed["operation"]["result"]


def spec(key, role, tool, skill, params, *, mode="read_only", dependencies=None, models=False):
    return {
        "key": key,
        "agent_type": role,
        "tool": tool,
        "skill_id": skill,
        "skill_version": 1,
        "execution_mode": mode,
        "parameters": params,
        "dependencies": dependencies or [],
        "use_coze": models,
    }


def submit_run(name, body, *, key=None):
    value = call(name + "_submit", "POST", "/api/v1/agent-tasks", body, expected=202, key=key)
    return call(
        name + "_run",
        "POST",
        f"/api/v1/agent-tasks/{value['task_id']}/run",
        {"expected_version": value["version"]},
    )


save()
for container in ("qingrongjin-agent-banking-agent-service-1", "qingyingjin-banking-service-1"):
    runtime = json.loads(
        subprocess.check_output(["docker", "inspect", container], encoding="utf8")
    )[0]
    report.setdefault("runtime", {})[container] = {
        "image_id": runtime["Image"],
        "started_at": runtime["State"]["StartedAt"],
    }
call("health", "GET", "/healthz")
call("ready", "GET", "/readyz")
account = call("account_before", "GET", "/api/v1/accounts/" + state["account_id"], bank=True)

if args.restart:
    result = call("persisted_result", "GET", "/api/v1/agent-tasks/" + state["completed_task_id"])
    assert result["status"] == "succeeded" and result["result"] == state["completed_result"]
    waiting = call("persisted_waiting", "GET", "/api/v1/agent-tasks/" + state["waiting_task_id"])
    assert (
        waiting["status"] == "waiting_confirmation"
        and waiting["confirmation"]["digest"] == state["waiting_digest"]
    )
    resumed = call(
        "resume_after_restart",
        "POST",
        f"/api/v1/agent-tasks/{waiting['task_id']}/resume",
        {
            "expected_version": waiting["version"],
            "node_key": waiting["confirmation"]["node_key"],
            "digest": waiting["confirmation"]["digest"],
            "nonce": waiting["confirmation"]["nonce"],
            "decision": "approve",
        },
    )
    assert resumed["status"] == "succeeded", resumed.get("error_code")
    state["prepared_operation_id"] = resumed["result"]["outputs"][-1]["result"]["id"]
    replay = call(
        "repeat_resume",
        "POST",
        f"/api/v1/agent-tasks/{waiting['task_id']}/resume",
        {
            "expected_version": waiting["version"],
            "node_key": waiting["confirmation"]["node_key"],
            "digest": waiting["confirmation"]["digest"],
            "nonce": waiting["confirmation"]["nonce"],
            "decision": "approve",
        },
    )
    assert replay["result"] == resumed["result"]
    operation = call(
        "operation_after_resume",
        "GET",
        "/api/v1/execution/operations/" + state["prepared_operation_id"],
        bank=True,
    )
    assert (
        operation["operation"]["status"] == "pending_authorization"
        and not operation["authorizations"]
    )
    report["checks"].update(
        persisted_graph_result=True,
        persisted_bound_interrupt=True,
        restart_resume_prepare_only=True,
        duplicate_resume_same_operation=True,
    )
elif args.models:
    items = [
        spec(
            "billing_model",
            "billing",
            "analyze_spending",
            "billing_analysis",
            state["billing_parameters"],
            models=True,
        ),
        spec(
            "subscription_model",
            "subscription",
            "analyze_subscription_cost",
            "subscription_analysis",
            state["subscription_parameters"],
            dependencies=["billing_model"],
            models=True,
        ),
        spec(
            "savings_model",
            "savings",
            "get_financial_plan",
            "savings_planning",
            {"plan_id": state["financial_plan_id"]},
            dependencies=["subscription_model"],
            models=True,
        ),
        spec(
            "lifestyle_model",
            "lifestyle",
            "get_lifestyle_plan",
            "lifestyle_planning",
            {"plan_id": state["plan_id"]},
            dependencies=["savings_model"],
            models=True,
        ),
    ]
    result = submit_run("models", {"tasks": items, "timeout_ms": 180_000, "max_attempts": 1})
    assert result["status"] == "succeeded", result.get("error_code")
    outputs = [item for item in result["result"]["outputs"] if item["agent_type"] != "supervisor"]
    assert len(outputs) == 4 and all(
        item.get("workflow_execute_id") and item.get("explanation") for item in outputs
    )
    natural = submit_run(
        "natural_supervisor",
        {
            "instruction": "请分析我的账单和订阅，再结合已有生活方案与储蓄计划提供说明，只查询和规划，不创建或执行金融操作。",
            "bindings": state["bindings"],
            "timeout_ms": 180_000,
            "max_attempts": 1,
        },
    )
    assert natural["status"] == "succeeded", natural.get("error_code")
    assert natural["result"]["outputs"][0].get("workflow_execute_id")
    report["checks"].update(
        four_specialist_real_model_execute_ids=True, natural_supervisor_real_model=True
    )
else:
    billing_report = call(
        "real_billing", "GET", "/api/v1/billing/reports/" + state["billing_report_id"], bank=True
    )
    period = billing_report["analysis_period"]
    state["billing_parameters"] = {
        "from": period["from"],
        "to": period["to"],
        "timezone": period["timezone"],
        "account_id": state["account_id"],
    }
    # Native analyze_subscription_cost accepts the empty DTO; account/period
    # filtering belongs to detect_subscriptions rather than this summary tool.
    state["subscription_parameters"] = {}
    goals = call("savings_goals", "GET", "/api/v1/savings/goals", bank=True)
    if not goals:
        now = int(time.time() * 1000)
        goal = operator_setup(
            "create_savings_goal",
            {
                "name": "P5 isolated simulated savings fixture",
                "target_minor": 60_000,
                "monthly_minor": 20_000,
                "currency": "CNY",
                "starts_at": now,
                "ends_at": now + 90 * 86400 * 1000,
            },
        )
    else:
        goal = goals[0]
    state["savings_goal_id"], state["savings_account_id"] = goal["id"], goal["account_id"]
    # Native transfers reject goal accounts: use a separate zero-funded checking
    # account, created by the test operator through the user execution gateway.
    if "transfer_target_account_id" not in state:
        target = operator_setup(
            "open_account",
            {"display_name": "P5 zero-funded transfer fixture", "account_type": "checking",
             "currency": "CNY", "initial_balance_minor": 0, "confirm_simulated_funds": False},
        )
        assert target["balance_minor"] == 0
        state["transfer_target_account_id"] = target["id"]
    state["target_account_id"] = state["transfer_target_account_id"]
    if "financial_plan_id" not in state:
        plan = call(
            "fixture_plan",
            "POST",
            "/api/v1/financial-plans",
            {
                "name": "P5 isolated deterministic plan",
                "settings": {
                    "period": datetime.now(UTC).strftime("%Y-%m"),
                    "account_ids": [int(state["account_id"])],
                    "savings_goal_ids": [int(goal["id"])],
                    "life_goal_ids": [],
                    "safety_reserve_minor": 100_000,
                    "cash_buffer_minor": 50_000,
                    "baseline_income_minor": 0,
                    "future_income_minor": 0,
                    "cashflows": [],
                },
            },
            bank=True,
            expected=201,
        )
        state["financial_plan_id"] = plan["plan"]["id"]
    state["bindings"] = {
        "from": period["from"],
        "to": period["to"],
        "account_id": state["account_id"],
        "financial_plan_id": state["financial_plan_id"],
        "lifestyle_goal_id": state["goal_id"],
        "lifestyle_plan_id": state["plan_id"],
        "lifestyle_goal_version": 1,
        "budget_id": state["budget_id"],
        "billing_report_id": state["billing_report_id"],
    }
    items = [
        spec(
            "billing",
            "billing",
            "analyze_spending",
            "billing_analysis",
            state["billing_parameters"],
        ),
        spec(
            "subscription",
            "subscription",
            "analyze_subscription_cost",
            "subscription_analysis",
            state["subscription_parameters"],
        ),
        spec(
            "savings",
            "savings",
            "get_financial_plan",
            "savings_planning",
            {"plan_id": state["financial_plan_id"]},
            dependencies=["billing", "subscription"],
        ),
        spec(
            "lifestyle",
            "lifestyle",
            "get_lifestyle_plan",
            "lifestyle_planning",
            {"plan_id": state["plan_id"]},
            dependencies=["savings"],
        ),
    ]
    key = "p5-live-" + str(uuid4())
    result = submit_run("five_roles", {"tasks": items}, key=key)
    assert result["status"] == "succeeded", result.get("error_code")
    assert {item["agent_type"] for item in result["result"]["outputs"]} == {
        "supervisor",
        "billing",
        "subscription",
        "savings",
        "lifestyle",
    }
    replay = call(
        "submission_replay", "POST", "/api/v1/agent-tasks", {"tasks": items}, expected=202, key=key
    )
    assert replay["task_id"] == result["task_id"]
    call(
        "submission_conflict",
        "POST",
        "/api/v1/agent-tasks",
        {"tasks": list(reversed(items))},
        expected=409,
        key=key,
    )
    state["completed_task_id"], state["completed_result"] = result["task_id"], result["result"]
    events = call("events", "GET", f"/api/v1/agent-tasks/{result['task_id']}/events")
    assert events and all("nonce" not in item for item in events)
    sse_request = urllib.request.Request(
        "http://127.0.0.1:8091/api/v1/agent-tasks/" + result["task_id"] + "/events/stream",
        headers={"Authorization": "Bearer " + token(), "X-Trace-ID": "p5-live-sse"},
    )
    with urllib.request.urlopen(sse_request, timeout=40) as response:
        text = response.read().decode()
        streamed = [json.loads(line[6:]) for line in text.splitlines() if line.startswith("data: ")]
        assert response.status == 200 and response.headers.get_content_type() == "text/event-stream"
        assert streamed == events
    report["checks"]["actual_owner_scoped_sse"] = True
    other = str(int(time.time() * 1000) + 3_000_000_000_000)
    call(
        "other_user",
        "POST",
        "/api/v1/users/me",
        {"display_name": "P5 owner isolation fixture"},
        user=other,
        bank=True,
        expected=201,
    )
    call(
        "foreign_task", "GET", "/api/v1/agent-tasks/" + result["task_id"], user=other, expected=404
    )
    call(
        "agent_denied",
        "GET",
        "/api/v1/agent-tasks/" + result["task_id"],
        role="supervisor",
        expected=403,
    )
    prepare = spec(
        "prepare_transfer",
        "supervisor",
        "require_gateway",
        "operation_preparation",
        {
            "kind": "transfer",
            "parameters": {
                "source_account_id": state["account_id"],
                "target_account_id": state["target_account_id"],
                "amount_minor": 100,
                "currency": "CNY",
                "description": "P5 preparation only",
            },
        },
        mode="intent",
    )
    waiting = submit_run("prepare", {"tasks": [prepare]})
    assert waiting["status"] == "waiting_confirmation", waiting.get("error_code")
    challenge = waiting["confirmation"]
    call(
        "wrong_nonce",
        "POST",
        f"/api/v1/agent-tasks/{waiting['task_id']}/resume",
        {
            "expected_version": waiting["version"],
            "node_key": challenge["node_key"],
            "digest": challenge["digest"],
            "nonce": "incorrect-confirmation-nonce",
            "decision": "approve",
        },
        expected=409,
    )
    state["waiting_task_id"], state["waiting_digest"] = waiting["task_id"], challenge["digest"]
    cancelled = call("cancel_submit", "POST", "/api/v1/agent-tasks", {"tasks": items}, expected=202)
    call(
        "cancel",
        "POST",
        f"/api/v1/agent-tasks/{cancelled['task_id']}/cancel",
        {"expected_version": cancelled["version"]},
    )
    cancelled = call(
        "cancelled_run",
        "POST",
        f"/api/v1/agent-tasks/{cancelled['task_id']}/run",
        {"expected_version": cancelled["version"]},
    )
    assert cancelled["status"] == "cancelled" and cancelled["result"] is None
    report["checks"].update(
        real_native_financial_tools=True,
        five_roles_and_dependencies=True,
        durable_idempotency=True,
        conflict_409=True,
        owner_and_agent_isolation=True,
        cancelled_task_not_executed=True,
        bound_prepare_interrupt=True,
        invalid_nonce_denied=True,
    )

after = call("account_after", "GET", "/api/v1/accounts/" + state["account_id"], bank=True)
assert after["balance_minor"] == account["balance_minor"] and after["version"] == account["version"]
report["checks"].update(bank_balance_and_version_unchanged=True, financial_execution_enabled=False)
report["passed"] = True
save()
print(json.dumps({"passed": True, "checks": report["checks"]}, ensure_ascii=False))
