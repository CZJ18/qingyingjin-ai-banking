"""Read-only P7 runtime/restart acceptance; signing keys stay in memory.

Capture after the browser has finished its explicitly authorized simulations:
  python scripts/verify-p7-runtime.py --mode capture --env-file PRIVATE_ENV
After an operator restarts Go and the agent service, run with --mode verify.
Only IDs, hashes, counts and check outcomes are persisted. No restart or
financial write endpoint is implemented. --self-test needs no credentials.
"""
import argparse
import base64
import hashlib
import hmac
import json
from pathlib import Path
import sys
import time
import unittest
import urllib.error
import urllib.parse
import urllib.request
import uuid


ROOT = Path(__file__).resolve().parents[3]


class CheckFailed(Exception):
    """Contains only a fixed diagnostic label, never server response data."""


def require(condition, label):
    if not condition:
        raise CheckFailed(label)


def fingerprint(value):
    raw = json.dumps(value, ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False)
    return hashlib.sha256(raw.encode()).hexdigest()


def integer(value):
    require(type(value) is int, "non_integer_financial_value")
    return value


def local_url(raw):
    parsed = urllib.parse.urlsplit(raw)
    require(parsed.scheme in {"http", "https"} and parsed.hostname in {"localhost", "127.0.0.1", "::1"}
            and not parsed.username and not parsed.password and not parsed.query and not parsed.fragment
            and parsed.path in {"", "/"}, "loopback_base_url_required")
    return raw.rstrip("/")


def read_signing_config(path):
    wanted = {"BANKING_USER_JWT_SECRET", "BANKING_JWT_ISSUER", "BANKING_JWT_AUDIENCE"}
    values = {}
    for line in path.read_text(encoding="utf-8-sig").splitlines():
        line = line.strip()
        if line.startswith("export "):
            line = line[7:]
        if not line or line.startswith("#") or "=" not in line:
            continue
        key, value = line.split("=", 1)
        if key.strip() in wanted:
            value = value.strip()
            if len(value) >= 2 and value[0] == value[-1] and value[0] in {"'", '"'}:
                value = value[1:-1]
            values[key.strip()] = value
    require(len(values.get("BANKING_USER_JWT_SECRET", "")) >= 32, "private_signing_configuration_unavailable")
    return values


def user_token(config, user):
    now = int(time.time())
    claims = {"iss": config.get("BANKING_JWT_ISSUER", "qingyingjin"),
              "aud": [config.get("BANKING_JWT_AUDIENCE", "banking-service")],
              "sub": user, "iat": now, "nbf": now, "exp": now + 300,
              "jti": str(uuid.uuid4()), "token_use": "user"}
    def encode(value):
        return base64.urlsafe_b64encode(json.dumps(value, separators=(",", ":")).encode()).rstrip(b"=")
    unsigned = encode({"alg": "HS256", "typ": "JWT"}) + b"." + encode(claims)
    signature = hmac.new(config["BANKING_USER_JWT_SECRET"].encode(), unsigned, hashlib.sha256).digest()
    return (unsigned + b"." + base64.urlsafe_b64encode(signature).rstrip(b"=")).decode()


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, request, file, code, message, headers, newurl):
        return None


class Reader:
    def __init__(self, base, signing):
        self.base = local_url(base)
        self.signing = signing
        self.opener = urllib.request.build_opener(NoRedirect())
        self.calls = 0

    def get(self, path, owner=None, expected=200):
        require(path.startswith("/") and not path.startswith("//"), "relative_read_route_required")
        headers = {"Accept": "application/json", "X-Trace-ID": "p7-read-only-runtime"}
        if owner:
            headers["Authorization"] = "Bearer " + user_token(self.signing, owner)
        request = urllib.request.Request(self.base + path, method="GET", headers=headers)
        self.calls += 1
        try:
            response = self.opener.open(request, timeout=25)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            require(response.status == expected, "unexpected_read_http_status")
            raw = response.read(8 * 1024 * 1024 + 1)
            require(len(raw) <= 8 * 1024 * 1024, "read_response_too_large")
            envelope = json.loads(raw)
        require(type(envelope) is dict, "invalid_read_envelope")
        if expected == 200:
            require(envelope.get("code") == "OK", "read_business_error")
        return envelope

    def pages(self, path, owner, wrapped=False):
        rows = []
        for page in range(10000):
            suffix = "&" if "?" in path else "?"
            data = self.get(f"{path}{suffix}limit=100&offset={page * 100}", owner)["data"]
            items = data["items"] if wrapped else data
            require(type(items) is list, "invalid_read_page")
            rows.extend(items)
            if len(items) < 100:
                if wrapped:
                    require(integer(data["total"]) == len(rows), "transaction_page_count_changed")
                return rows
        raise CheckFailed("read_page_limit_exceeded")


def sorted_rows(rows):
    require(len({str(row["id"]) for row in rows}) == len(rows), "duplicate_read_row_id")
    return sorted(rows, key=lambda row: str(row["id"]))


def make_witness(user, accounts, balances, ledger, transactions, operations, view, audits):
    accounts, ledger, transactions, operations = map(sorted_rows, (accounts, ledger, transactions, operations))
    ids = [str(row["id"]) for row in accounts]
    require(len(accounts) >= 2, "persisted_simulated_accounts_missing")
    require(all(str(row["user_id"]) == user and row["is_simulated"] is True for row in accounts), "non_owned_simulation_account")
    require(all(str(row["user_id"]) == user and str(row["account_id"]) in ids for row in ledger), "non_owned_ledger_row")
    require(all(str(row["user_id"]) == user for row in transactions + operations), "non_owned_business_row")
    for account in accounts:
        account_id = str(account["id"])
        balance = balances[account_id]
        entries = [row for row in ledger if str(row["account_id"]) == account_id]
        total = sum(integer(row["delta_minor"]) for row in entries)
        require(balance["reconciled"] is True and str(balance["account_id"]) == account_id
                and total == integer(balance["balance_minor"]) == integer(balance["ledger_balance_minor"])
                == integer(account["balance_minor"]), "account_ledger_not_reconciled")
        require(all(row["currency"] == account["currency"] for row in entries), "ledger_currency_mismatch")
    internal = {}
    for transaction in transactions:
        if transaction["kind"] == "internal_transfer":
            internal.setdefault(transaction["journal_id"], []).append(transaction)
    require(bool(internal), "persisted_internal_transfer_missing")
    for journal, pair in internal.items():
        entries = [row for row in ledger if row["journal_id"] == journal]
        require(len(pair) == 2 and len(entries) == 2 and sum(integer(row["delta_minor"]) for row in entries) == 0
                and {row["side"] for row in entries} == {"debit", "credit"}
                and len({str(row["account_id"]) for row in entries}) == 2
                and {str(row["transaction_id"]) for row in entries} == {str(row["id"]) for row in pair}, "internal_transfer_journal_unbalanced")
        for transaction in pair:
            entry = next(row for row in entries if str(row["transaction_id"]) == str(transaction["id"]))
            amount = integer(transaction["amount_minor"])
            delta = amount if transaction["direction"] == "income" else -amount
            require(amount > 0 and transaction["direction"] in {"income", "expense"}
                    and integer(entry["delta_minor"]) == delta and transaction["currency"] == entry["currency"]
                    and str(transaction["bank_account_id"]) == str(entry["account_id"]), "transaction_ledger_mismatch")
    operation = view["operation"]
    require(operation["status"] == "succeeded" and operation["kind"] == "transfer" and str(operation["user_id"]) == user,
            "succeeded_owned_transfer_missing")
    authorizations = view["authorizations"]
    consumed = [row for row in authorizations if integer(row["consumed_at"]) > 0]
    require(len(consumed) == 1 and consumed[0]["decision"] == "approve"
            and consumed[0]["id"] == operation["authorization_id"]
            and consumed[0]["operation_id"] == operation["id"] and str(consumed[0]["user_id"]) == user
            and consumed[0]["intent_digest"] == operation["intent_digest"], "bound_authorization_not_consumed_once")
    transfer = operation["result"]
    require(type(transfer) is dict and transfer["status"] == "succeeded"
            and str(transfer["id"]) == str(operation["resource_id"])
            and transfer["journal_id"] in internal, "executed_transfer_receipt_invalid")
    require(integer(transfer["amount_minor"]) == integer(operation["amount_minor"]) > 0
            and str(transfer["source_account_id"]) == str(operation["source_account_id"])
            and str(transfer["target_account_id"]) == str(operation["target_account_id"]), "transfer_intent_receipt_mismatch")
    entries = [row for row in ledger if row["journal_id"] == transfer["journal_id"]]
    require({str(row["account_id"]): integer(row["delta_minor"]) for row in entries}
            == {str(transfer["source_account_id"]): -transfer["amount_minor"], str(transfer["target_account_id"]): transfer["amount_minor"]}, "executed_transfer_posting_mismatch")
    require(any(row["state"] == "succeeded" and row["operation_id"] == operation["id"]
                and row["authorization_id"] == operation["authorization_id"] for row in audits), "execution_audit_missing")
    return {"version": 1, "user_id": user, "account_ids": ids, "operation_id": operation["id"],
            "authorization_id": consumed[0]["id"], "journal_id": transfer["journal_id"],
            "counts": {"accounts": len(accounts), "ledger_entries": len(ledger), "transactions": len(transactions),
                       "internal_transfer_journals": len(internal), "operation_window": len(operations),
                       "authorizations": len(authorizations), "consumed_authorizations": len(consumed), "execution_audits": len(audits)},
            "hashes": {"accounts": fingerprint(accounts), "balances": fingerprint(balances), "ledger": fingerprint(ledger),
                       "transactions": fingerprint(transactions), "operations": fingerprint(operations),
                       "operation_view": fingerprint(view), "audits": fingerprint(sorted_rows(audits))}}


def collect(args, signing):
    reader = Reader(args.base_url, signing)
    agent = Reader(args.agent_base_url, signing)
    ready = reader.get("/readyz")["data"]["dependencies"]
    require({"postgres", "redis", "storage", "schema"} <= ready.keys() and "mysql" not in ready
            and all(value == "ready" for value in ready.values()), "postgres_runtime_not_ready")
    agent_ready = agent.get("/readyz")["data"]["dependencies"]
    require({"postgresql", "redis", "banking"} <= agent_ready.keys()
            and all(value == "ready" for value in agent_ready.values()), "agent_runtime_not_ready")
    accounts = reader.pages("/api/v1/accounts", args.user_id)
    balances, ledger = {}, []
    for account in accounts:
        account_id = str(account["id"])
        balances[account_id] = reader.get(f"/api/v1/accounts/{account_id}/balance", args.user_id)["data"]
        ledger.extend(reader.pages(f"/api/v1/accounts/{account_id}/ledger", args.user_id))
    transactions = reader.pages("/api/v1/transactions", args.user_id, wrapped=True)
    operations = reader.get("/api/v1/execution/operations", args.user_id)["data"]
    require(type(operations) is list and len(operations) < 100, "operation_window_may_be_truncated")
    completed = [row for row in operations if row["kind"] == "transfer" and row["status"] == "succeeded"]
    require(bool(completed), "succeeded_transfer_fixture_missing")
    selected = max(completed, key=lambda row: (row["created_at"], row["id"]))
    view = reader.get(f"/api/v1/execution/operations/{selected['id']}", args.user_id)["data"]
    audits = reader.get(f"/api/v1/execution/operations/{selected['id']}/audits", args.user_id)["data"]
    witness = make_witness(args.user_id, accounts, balances, ledger, transactions, operations, view, audits)
    foreign = reader.get(f"/api/v1/accounts/{witness['account_ids'][0]}", args.foreign_user_id, expected=404)
    require(foreign.get("code") == "NOT_FOUND" and not foreign.get("data"), "cross_user_account_exposed")
    return witness, reader.calls + agent.calls


def write_json(path, value):
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(value, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")


class RuntimeProofTests(unittest.TestCase):
    @staticmethod
    def fixture():
        accounts = [{"id": "1", "user_id": "7", "is_simulated": True, "currency": "CNY", "balance_minor": 100},
                    {"id": "2", "user_id": "7", "is_simulated": True, "currency": "CNY", "balance_minor": 29}]
        balances = {row["id"]: {"account_id": row["id"], "balance_minor": row["balance_minor"],
                    "ledger_balance_minor": row["balance_minor"], "reconciled": True} for row in accounts}
        ledger = [{"id": "1", "user_id": "7", "account_id": "1", "journal_id": "opening", "transaction_id": "10", "currency": "CNY", "delta_minor": 129, "side": "debit"},
                  {"id": "2", "user_id": "7", "account_id": "1", "journal_id": "transfer", "transaction_id": "11", "currency": "CNY", "delta_minor": -29, "side": "credit"},
                  {"id": "3", "user_id": "7", "account_id": "2", "journal_id": "transfer", "transaction_id": "12", "currency": "CNY", "delta_minor": 29, "side": "debit"}]
        transactions = [{"id": "11", "user_id": "7", "bank_account_id": "1", "kind": "internal_transfer", "journal_id": "transfer", "currency": "CNY", "direction": "expense", "amount_minor": 29},
                        {"id": "12", "user_id": "7", "bank_account_id": "2", "kind": "internal_transfer", "journal_id": "transfer", "currency": "CNY", "direction": "income", "amount_minor": 29}]
        operation = {"id": "operation", "user_id": "7", "kind": "transfer", "status": "succeeded", "authorization_id": "approval", "intent_digest": "digest",
                     "amount_minor": 29, "source_account_id": "1", "target_account_id": "2", "resource_id": "3", "confirmation_nonce": "must-not-escape",
                     "parameters": {"description": "must-not-escape"},
                     "result": {"id": "3", "status": "succeeded", "journal_id": "transfer", "amount_minor": 29, "source_account_id": "1", "target_account_id": "2"}}
        view = {"operation": operation, "authorizations": [{"id": "approval", "user_id": "7", "operation_id": "operation", "decision": "approve", "consumed_at": 1, "intent_digest": "digest"}]}
        audits = [{"id": "audit", "state": "succeeded", "operation_id": "operation", "authorization_id": "approval"}]
        return ["7", accounts, balances, ledger, transactions, [operation], view, audits]

    def test_fingerprint_preserves_integer_precision(self):
        self.assertEqual(fingerprint({"b": 9007199254740993, "a": 1}), fingerprint({"a": 1, "b": 9007199254740993}))
        self.assertNotEqual(fingerprint(9007199254740993), fingerprint(9007199254740992))

    def test_loopback_only(self):
        for value in ["https://example.com", "http://127.0.0.1@evil.example", "http://localhost/x", "http://localhost?x=1"]:
            with self.assertRaises(CheckFailed):
                local_url(value)

    def test_minor_units_reject_float_and_boolean(self):
        for value in [29.0, True, "29"]:
            with self.assertRaises(CheckFailed):
                integer(value)

    def test_duplicate_ledger_ids_rejected(self):
        with self.assertRaises(CheckFailed):
            sorted_rows([{"id": "1"}, {"id": "1"}])

    def test_valid_proof_redacts_nonce_payload_and_amounts(self):
        witness = make_witness(*self.fixture())
        raw = json.dumps(witness)
        self.assertEqual(witness["counts"]["consumed_authorizations"], 1)
        self.assertNotIn("must-not-escape", raw)
        self.assertNotIn("amount_minor", raw)
        self.assertNotIn("balance_minor", raw)

    def test_ledger_balance_mismatch_rejected(self):
        values = self.fixture()
        values[3][0]["delta_minor"] += 1
        with self.assertRaisesRegex(CheckFailed, "account_ledger_not_reconciled"):
            make_witness(*values)

    def test_unconsumed_authorization_rejected(self):
        values = self.fixture()
        values[6]["authorizations"][0]["consumed_at"] = 0
        with self.assertRaisesRegex(CheckFailed, "bound_authorization_not_consumed_once"):
            make_witness(*values)

    def test_transaction_amount_must_match_ledger(self):
        values = self.fixture()
        values[4][0]["amount_minor"] = 30
        with self.assertRaisesRegex(CheckFailed, "transaction_ledger_mismatch"):
            make_witness(*values)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--mode", choices=["capture", "verify"], default="verify")
    parser.add_argument("--env-file", type=Path, default=ROOT / "deploy/banking/.env.local")
    parser.add_argument("--base-url", default="http://127.0.0.1:8090")
    parser.add_argument("--agent-base-url", default="http://127.0.0.1:8091")
    parser.add_argument("--user-id", default="1960014006001")
    parser.add_argument("--foreign-user-id", default="1960014006002")
    parser.add_argument("--witness", type=Path, default=ROOT / "output/p7/runtime-witness.json")
    parser.add_argument("--report", type=Path)
    parser.add_argument("--self-test", action="store_true")
    args = parser.parse_args()
    if args.self_test:
        result = unittest.TextTestRunner(verbosity=2).run(unittest.defaultTestLoader.loadTestsFromTestCase(RuntimeProofTests))
        return 0 if result.wasSuccessful() else 1
    report_path = args.report or ROOT / f"output/p7/runtime-{'capture' if args.mode == 'capture' else 'restart'}.json"
    report = {"passed": False, "mode": args.mode, "get_only": True, "financial_execution": False}
    try:
        require(args.user_id.isascii() and args.user_id.isdecimal() and int(args.user_id) > 0
                and args.foreign_user_id.isascii() and args.foreign_user_id.isdecimal()
                and int(args.foreign_user_id) > 0 and args.user_id != args.foreign_user_id, "distinct_positive_user_ids_required")
        if args.mode == "capture":
            require(not args.witness.exists(), "existing_witness_not_overwritten")
        else:
            require(args.witness.is_file(), "pre_restart_witness_required")
        signing = read_signing_config(args.env_file)
        witness, calls = collect(args, signing)
        if args.mode == "capture":
            write_json(args.witness, witness)
        else:
            previous = json.loads(args.witness.read_text(encoding="utf-8"))
            require(previous == witness, "durable_witness_changed")
        report.update(passed=True, user_id=args.user_id, counts=witness["counts"], get_requests=calls,
                      witness_sha256=fingerprint(witness), checks={"postgres_storage_ready": True, "agent_dependencies_ready": True,
                      "owned_simulated_accounts": True, "account_ledger_reconciled": True, "internal_journals_balanced": True,
                      "bound_authorization_consumed_once": True, "succeeded_transfer_audited": True,
                      "cross_user_account_not_found": True, "restart_hashes_match": args.mode == "verify"})
    except CheckFailed as error:
        report["error"] = str(error)
    except Exception as error:
        report["error"] = "runtime_verification_failed"
        report["error_type"] = type(error).__name__
    write_json(report_path, report)
    print(json.dumps(report, ensure_ascii=False))
    return 0 if report["passed"] else 1


if __name__ == "__main__":
    sys.exit(main())
