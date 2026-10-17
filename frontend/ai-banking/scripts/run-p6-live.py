"""Operator-only live browser acceptance.

Uses an explicitly isolated simulated user. Fixture writes use the existing
trusted execution gateway. Never installs browser credentials into application
source, localStorage or build variables. Only the two isolated P6 fixtures may
receive phone credentials, kept in an ignored, owner-readable local file.
"""
import base64
import csv
import hashlib
import hmac
import json
import os
import re
import secrets
import subprocess
import time
import urllib.request
import urllib.error
from pathlib import Path
from uuid import uuid4

root = Path(__file__).resolve().parents[3]
frontend = root / "frontend/ai-banking"
private = dict(line.split("=", 1) for line in (root / "deploy/banking/.env.local").read_text(encoding="utf-8-sig").splitlines() if "=" in line and not line.startswith("#"))
user = "1960014006001"
foreign = "1960014006002"
fixture_users = frozenset((user, foreign))
credential_file = root / "output/phone-auth/p6-browser-credentials.local.json"


class FixtureAPIError(RuntimeError):
    def __init__(self, path, status, code):
        super().__init__(f"Fixture request failed: {path} HTTP {status} {code}")
        self.status = status
        self.code = code


def token_subject(value):
    part = value.split(".")[1]
    return json.loads(base64.urlsafe_b64decode(part + "=" * (-len(part) % 4)))["sub"]


def lock_private_file(path):
    if os.name == "nt":
        identity = subprocess.check_output(
            ["whoami", "/user", "/fo", "csv", "/nh"], text=True
        ).strip()
        sid = next(csv.reader([identity]))[1]
        if not sid.startswith("S-1-"):
            raise RuntimeError("Cannot determine the current Windows user SID")
        subprocess.run(
            ["icacls", str(path), "/inheritance:r", "/grant:r", f"*{sid}:(F)"],
            stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL, check=True,
        )
    else:
        path.chmod(0o600)


def save_phone_credentials(data):
    credential_file.parent.mkdir(parents=True, exist_ok=True)
    temporary = credential_file.with_name(credential_file.name + "." + uuid4().hex)
    descriptor = os.open(temporary, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    try:
        lock_private_file(temporary)
        with os.fdopen(descriptor, "w", encoding="utf-8") as stream:
            descriptor = -1
            json.dump(data, stream, ensure_ascii=False, indent=2)
            stream.write("\n")
        os.replace(temporary, credential_file)
    finally:
        if descriptor != -1:
            os.close(descriptor)
        if temporary.exists():
            temporary.unlink()


def load_phone_credentials():
    if credential_file.exists():
        if credential_file.resolve().parent != credential_file.parent.resolve():
            raise RuntimeError("P6 credential file must remain inside its ignored directory")
        lock_private_file(credential_file)
        data = json.loads(credential_file.read_text(encoding="utf-8"))
        if data.get("version") != 1 or set(data.get("users", {})) != fixture_users:
            raise RuntimeError("P6 credential file has an invalid isolated-user whitelist")
        for entry in data["users"].values():
            if not re.fullmatch(r"1[3-9][0-9]{9}", entry.get("phone", "")) or not 8 <= len(entry.get("password", "")) <= 128:
                raise RuntimeError("P6 private phone credentials have an invalid format")
        return data
    data = {"version": 1, "users": {
        owner: {"phone": "199" + f"{secrets.randbelow(100000000):08d}",
                "password": secrets.token_urlsafe(32), "bound": False}
        for owner in fixture_users
    }}
    # Save before binding so an interrupted run retains the exact credentials.
    save_phone_credentials(data)
    return data

def token(owner):
    now = int(time.time())
    def encode(value):
        return base64.urlsafe_b64encode(json.dumps(value, separators=(",", ":")).encode()).rstrip(b"=")
    raw = encode({"alg": "HS256", "typ": "JWT"}) + b"." + encode({"iss": private.get("BANKING_JWT_ISSUER", "qingyingjin"), "aud": [private.get("BANKING_JWT_AUDIENCE", "banking-service")], "sub": owner, "iat": now, "nbf": now, "exp": now + 1800, "jti": str(uuid4()), "token_use": "user"})
    return (raw + b"." + base64.urlsafe_b64encode(hmac.new(private["BANKING_USER_JWT_SECRET"].encode(), raw, hashlib.sha256).digest()).rstrip(b"=")).decode()

def call(path, body=None, key=None, owner=user):
    if owner not in fixture_users:
        raise RuntimeError("P6 fixture requests require an explicitly isolated user")
    signed = token(owner)
    if token_subject(signed) != owner:
        raise RuntimeError("P6 operator JWT subject does not match its isolated owner")
    req = urllib.request.Request("http://127.0.0.1:8090" + path, data=None if body is None else json.dumps(body).encode(), headers={"Authorization": "Bearer " + signed, "Content-Type": "application/json", "Idempotency-Key": key or str(uuid4())}, method="GET" if body is None else "POST")
    try:
        with urllib.request.urlopen(req, timeout=60) as response:
            envelope = json.load(response)
    except urllib.error.HTTPError as error:
        try:
            code = json.loads(error.read(4096)).get("code", "UNKNOWN")
        except (ValueError, AttributeError):
            code = "INVALID_RESPONSE"
        raise FixtureAPIError(path, error.code, code) from None
    if envelope.get("code") != "OK":
        raise RuntimeError("Fixture request failed: " + path + " " + envelope.get("code", ""))
    return envelope["data"]


def prepare_phone_identity(owner, data):
    if owner not in fixture_users:
        raise RuntimeError("Phone binding is restricted to the two P6 fixture users")
    entry = data["users"][owner]
    body = {"phone": entry["phone"], "password": entry["password"]}
    if entry.get("bound"):
        # The real browser login below revalidates both user ID and JWT subject.
        # Do not spend another password-login rate-limit attempt on every rerun.
        return body
    try:
        session = call("/api/v1/auth/bind", body, owner=owner)
    except FixtureAPIError as error:
        if error.status != 409 or error.code != "AUTH_PHONE_UNAVAILABLE":
            raise
        # A prior interrupted bind may have succeeded. Verify the retained
        # credentials; never replace an unknown existing binding/password.
        session = call("/api/v1/auth/login", body, owner=owner)
    if session["user"]["id"] != owner or token_subject(session["access_token"]) != owner:
        raise RuntimeError("P6 phone identity does not match its isolated fixture owner")
    if not entry.get("bound"):
        entry["bound"] = True
        save_phone_credentials(data)
    return body

def fixture(kind, parameters, key):
    prepared = call("/api/v1/execution/operations", {"kind": kind, "parameters": parameters}, key)
    op = prepared["operation"]
    if op["status"] == "succeeded":
        return op["result"]
    authorized = call(f"/api/v1/execution/operations/{op['id']}/authorize", {"decision": "approve", "intent_digest": op["intent_digest"], "confirmation_nonce": op["confirmation_nonce"]})["operation"]
    result = call(f"/api/v1/execution/operations/{op['id']}/execute", {"authorization_id": authorized["authorization_id"], "intent_digest": authorized["intent_digest"], "expected_version": authorized["version"]}, key + "-execute")["operation"]
    if result["status"] != "succeeded":
        raise RuntimeError("Fixture operation failed: " + kind + " " + result["status"])
    return result["result"]

accounts = call("/api/v1/accounts?limit=100")
source = next(a for a in accounts if a["display_name"] == "模拟日常账户")
target = next((a for a in accounts if a["display_name"] == "P6验收收款账户"), None)
if not target:
    fixture("open_account", {"display_name": "P6验收收款账户", "account_type": "checking", "currency": "CNY", "initial_balance_minor": 0, "confirm_simulated_funds": True}, "p6-fixture-target-v1")
    target = next(a for a in call("/api/v1/accounts?limit=100") if a["display_name"] == "P6验收收款账户")
budget = next((b for b in call("/api/v1/budgets?limit=100") if b["month"] == "2026-09"), None)
if not budget:
    fixture("create_budget", {"month": "2026-09", "total_minor": 160000, "currency": "CNY", "categories": [{"category": "food", "allocated_minor": 60000, "essential_reserve_minor": 50000}, {"category": "housing", "allocated_minor": 60000, "essential_reserve_minor": 60000}, {"category": "shopping", "allocated_minor": 30000, "essential_reserve_minor": 0}, {"category": "exercise", "allocated_minor": 10000, "essential_reserve_minor": 5000}], "enforce_reserve": True}, "p6-fixture-budget-v1")
    budget = next(b for b in call("/api/v1/budgets?limit=100") if b["month"] == "2026-09")
for index, (category, amount, merchant) in enumerate([("food", 18000, "P6模拟正常餐食"), ("shopping", 20000, "P6模拟生活用品"), ("housing", 30000, "P6模拟住房开支")]):
    fixture("import_transaction", {"bank_account_id": source["id"], "external_transaction_id": f"p6-september-{index}", "direction": "expense", "amount_minor": amount, "currency": "CNY", "category": category, "merchant_name": merchant, "description": "Explicit isolated P6 test fixture", "occurred_at": 1788480000000 + index * 86400000, "effect_mode": "apply_simulated_effect", "confirm_simulated_effect": True}, f"p6-fixture-transaction-{index}-v1")
# Earlier failed runs deliberately left an active higher-reserve plan in place.
# Record actual simulated income through the gateway; preserve every reserve.
# Stable keys prevent reruns from crediting this fixture twice.
posted = next((t for t in call("/api/v1/transactions?category=salary&limit=100")["items"] if t["external_transaction_id"] == "p6-posted-income-v1"), None)
if posted:
    assert posted["amount_minor"] == 100000 and posted["effect_mode"] == "apply_simulated_effect"
else:
    fixture("import_transaction", {"bank_account_id": source["id"], "external_transaction_id": "p6-posted-income-v1", "direction": "income", "amount_minor": 100000, "currency": "CNY", "category": "salary", "merchant_name": "P6模拟已到账收入", "description": "Posted simulated income for isolated P6 authorization acceptance", "occurred_at": 1792022400000, "effect_mode": "apply_simulated_effect", "confirm_simulated_effect": True}, "p6-fixture-posted-income-v1")
call("/api/v1/users/me", {"display_name": "P6越权验收用户"}, owner=foreign)
phone_credentials = load_phone_credentials()
owner_login = prepare_phone_identity(user, phone_credentials)
foreign_login = prepare_phone_identity(foreign, phone_credentials)
state = {"user_id": user, "account_id": source["id"], "target_account_id": target["id"], "budget_id": budget["id"], "fixture_only": "persisted simulated Banking API data; no frontend response interception"}
out = frontend / "test-results/p6"
out.mkdir(parents=True, exist_ok=True)
(out / "fixture.json").write_text(json.dumps(state, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
environment = os.environ.copy()
environment.update(BANKING_P6_JWT=token(user), BANKING_P6_FOREIGN_JWT=token(foreign), BANKING_P6_FIXTURE=json.dumps(state), BANKING_TEST_BROWSER="C:/Program Files (x86)/Microsoft/Edge/Application/msedge.exe",
                   BANKING_P6_PHONE=owner_login["phone"], BANKING_P6_PASSWORD=owner_login["password"],
                   BANKING_P6_FOREIGN_PHONE=foreign_login["phone"], BANKING_P6_FOREIGN_PASSWORD=foreign_login["password"])
result = subprocess.run(["node", "scripts/verify-p6-live.mjs"], cwd=frontend, env=environment, check=False)
raise SystemExit(result.returncode)
