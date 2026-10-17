"""Real Go -> FastAPI -> PostgreSQL/Redis projection and restart acceptance.

Signed tokens are test/operator facilities loaded in memory; application image
contains no JWT secret. Financial data are persisted simulated Go fixtures.
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
from pathlib import Path
from uuid import uuid4

root = Path(__file__).resolve().parents[3]
out = root / "services/banking-agent-service/output/p4"
out.mkdir(parents=True, exist_ok=True)
parser = argparse.ArgumentParser()
parser.add_argument("--restart", action="store_true")
args = parser.parse_args()


def env_file(path):
    return dict(
        line.split("=", 1)
        for line in path.read_text(encoding="utf8").splitlines()
        if "=" in line and not line.startswith("#")
    )


bank_env = env_file(root / "deploy/banking/.env.local")
config = env_file(root / "deploy/banking-agent/.env.local")
state = json.loads(
    (
        out / "live-state.json"
        if args.restart
        else root / "services/banking-service/output/module11/live-state.json"
    ).read_text(encoding="utf8")
)
report = {
    "passed": False,
    "restart": args.restart,
    "checks": {},
    "responses": {},
    "started_at": time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime()),
}
runtime = json.loads(
    subprocess.check_output(
        ["docker", "inspect", "qingrongjin-agent-banking-agent-service-1"], encoding="utf8"
    )
)[0]
report["runtime"] = {
    "image_id": runtime["Image"],
    "container_id": runtime["Id"],
    "started_at": runtime["State"]["StartedAt"],
}
report_path = out / ("live-restart.json" if args.restart else "live.json")
if report_path.exists() and not json.loads(report_path.read_text(encoding="utf8")).get("passed"):
    (out / (report_path.stem + "-failed-" + str(time.time_ns()) + ".json")).write_bytes(
        report_path.read_bytes()
    )


def persist():
    report_path.write_text(json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf8")


def token(user_id=None, role=None):
    now = int(time.time())
    claims = {
        "iss": bank_env.get("BANKING_JWT_ISSUER", "qingyingjin"),
        "aud": [bank_env.get("BANKING_JWT_AUDIENCE", "banking-service")],
        "sub": user_id or state["user_id"],
        "iat": now,
        "nbf": now,
        "exp": now + 90,
        "jti": str(uuid4()),
        "token_use": "agent" if role else "user",
    }
    if role:
        claims.update(agent_type=role, task_id="p4-acceptance")

    def encode(value):
        return base64.urlsafe_b64encode(json.dumps(value, separators=(",", ":")).encode()).rstrip(
            b"="
        )

    raw = encode({"alg": "HS256", "typ": "JWT"}) + b"." + encode(claims)
    return (
        raw
        + b"."
        + base64.urlsafe_b64encode(
            hmac.new(bank_env["BANKING_USER_JWT_SECRET"].encode(), raw, hashlib.sha256).digest()
        ).rstrip(b"=")
    ).decode()


def call(
    name, method, path, body=None, expected=200, *, banking=False, user_id=None, role=None, key=None
):
    origin = config["BANKING_AGENT_BANKING_URL" if banking else "BANKING_AGENT_API_URL"]
    request = urllib.request.Request(
        origin + path,
        method=method,
        data=None if body is None else json.dumps(body, ensure_ascii=False).encode(),
        headers={
            "Authorization": "Bearer " + token(user_id, role),
            "Content-Type": "application/json",
            "X-Trace-ID": "p4-live-acceptance",
            "Idempotency-Key": key or str(uuid4()),
        },
    )
    try:
        response = urllib.request.urlopen(request, timeout=30)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        value = json.load(response)
        report["responses"][name] = {"http_status": response.status, "body": value}
        persist()
        assert response.status == expected, (name, response.status, value.get("code"))
        return value.get("data")


persist()
# Compose reports the process started before ASGI lifespan has completed. Poll
# only anonymous readiness; business POST requests below are never retried.
deadline = time.monotonic() + 30
while True:
    try:
        with urllib.request.urlopen(
            config["BANKING_AGENT_API_URL"] + "/readyz", timeout=3
        ) as probe:
            if probe.status == 200:
                break
    except (OSError, urllib.error.URLError):
        pass
    if time.monotonic() >= deadline:
        raise RuntimeError("Banking agent did not become ready within 30 seconds")
    time.sleep(0.25)
call("health", "GET", "/healthz")
ready = call("ready", "GET", "/readyz")
assert ready["dependencies"] == {"postgresql": "ready", "redis": "ready", "banking": "ready"}
identity = call("identity", "GET", "/api/v1/me")
assert identity["user_id"] == state["user_id"] and not identity["financial_execution_enabled"]

if not args.restart:
    before = call(
        "bank_account_before", "GET", "/api/v1/accounts/" + state["account_id"], banking=True
    )
    plan = call("bank_plan", "GET", "/api/v2/lifestyle-plans/" + state["plan_id"], banking=True)
    evidence = plan["plan"]["snapshot"]["evidence_transaction_ids"]
    assert evidence
    refs = [
        {"kind": kind, "source_id": state[field]}
        for kind, field in [
            ("account", "account_id"),
            ("budget", "budget_id"),
            ("lifestyle_goal", "goal_id"),
            ("lifestyle_plan", "plan_id"),
        ]
    ]
    refs += [{"kind": "transaction", "source_id": evidence[0]}]
    event_id = "p4-live-" + str(uuid4())
    result = call(
        "refresh", "POST", "/api/v1/projections/refresh", {"resources": refs}, key=event_id
    )
    replay = call(
        "replay", "POST", "/api/v1/projections/refresh", {"resources": refs}, key=event_id
    )
    assert result == replay
    call(
        "key_conflict",
        "POST",
        "/api/v1/projections/refresh",
        {"resources": list(reversed(refs))},
        key=event_id,
        expected=409,
    )
    call("agent_denied", "GET", "/api/v1/profile", role="supervisor", expected=403)
    profile = call("profile_before", "GET", "/api/v1/profile")
    updated = call(
        "profile_update",
        "PUT",
        "/api/v1/profile",
        {
            "expected_version": profile["revision"],
            "timezone": "Asia/Shanghai",
            "safety_reserve_minor": 12345,
        },
    )
    call(
        "profile_version_conflict",
        "PUT",
        "/api/v1/profile",
        {
            "expected_version": profile["revision"],
            "timezone": "Asia/Shanghai",
            "safety_reserve_minor": 12345,
        },
        expected=409,
    )
    other = str(int(time.time() * 1000) + 1_000_000_000_000)
    # Initialize an independent local simulation identity only; no account/funds write.
    call(
        "second_user_registered",
        "POST",
        "/api/v1/users/me",
        {"display_name": "P4 isolated identity fixture"},
        user_id=other,
        banking=True,
        expected=201,
    )
    call(
        "cross_user_projection",
        "GET",
        "/api/v1/projections/account/" + state["account_id"],
        user_id=other,
        expected=404,
    )
    call(
        "cross_user_source",
        "POST",
        "/api/v1/projections/refresh",
        {"resources": [refs[0]]},
        user_id=other,
        expected=404,
    )
    state.update(
        event_id=event_id,
        projection_balance=before["balance_minor"],
        profile_revision=updated["revision"],
        transaction_id=evidence[0],
        recommendation_id=plan["recommendations"][0]["id"],
        evidence_ids=evidence,
    )
    (out / "live-state.json").write_text(
        json.dumps(state, ensure_ascii=False, indent=2) + "\n", encoding="utf8"
    )
    report["checks"].update(
        real_go_source_reads=True,
        refresh_replay_identical=True,
        idempotency_conflict_409=True,
        agent_denied_403=True,
        cross_user_isolated=True,
        profile_optimistic_revision=True,
    )

account = call("projected_account", "GET", "/api/v1/projections/account/" + state["account_id"])
assert account["payload"]["balance_minor"] == state["projection_balance"]
assert (
    account["source_version"] == account["payload"]["version"]
    and account["source_digest"]
    and account["observed_at"]
)
profile = call("profile", "GET", "/api/v1/profile")
assert profile["revision"] == state["profile_revision"] and profile["safety_reserve_minor"] == 12345
receipt = call("receipt", "GET", "/api/v1/projections/events/" + state["event_id"])
assert receipt["event_id"] == state["event_id"]
recommendation = call(
    "recommendation", "GET", "/api/v1/projections/recommendation/" + state["recommendation_id"]
)
ev = call("evidence", "GET", "/api/v1/recommendations/" + state["recommendation_id"] + "/evidence")
assert {x["source_id"] for x in ev["evidence"]} == set(state["evidence_ids"])
assert ev["source_digest"] == recommendation["source_digest"]
after = call("bank_account_after", "GET", "/api/v1/accounts/" + state["account_id"], banking=True)
assert after["balance_minor"] == state["projection_balance"]
report["checks"].update(
    actual_integer_amount_preserved=True,
    source_version_digest_and_time=True,
    evidence_preserved=True,
    profile_and_receipt_persisted=True,
    banking_balance_unchanged=True,
)
report["passed"] = True
persist()
print(json.dumps(report["checks"], ensure_ascii=False))
