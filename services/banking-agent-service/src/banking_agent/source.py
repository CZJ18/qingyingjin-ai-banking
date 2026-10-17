"""Strict allowlisted, read-only point-in-time projections of native Go resources.

Source versions are native counters or millisecond timestamps, never fabricated
from fetch time. Projections contain no financial writes or authorization material.
"""

import re
from dataclasses import dataclass
from typing import Any
from uuid import UUID

from .errors import DomainError

MAX_AMOUNT = 1_000_000_000_000
MAX_INT64 = 9_223_372_036_854_775_807
SOURCE_KINDS = frozenset(
    {
        "account",
        "transaction",
        "subscription",
        "budget",
        "lifestyle_goal",
        "lifestyle_plan",
        "operation",
    }
)
_ID = re.compile(r"[1-9][0-9]{0,18}\Z", re.ASCII)
_CORRELATION = re.compile(r"[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}\Z", re.ASCII)


@dataclass(frozen=True)
class Projection:
    kind: str
    source_id: str
    source_version: int
    payload: dict
    evidence: list[dict]
    audit_id: str | None = None


def canonical_numeric_id(value: Any, *, allow_zero: bool = False) -> str:
    if allow_zero and value == "0":
        return value
    if type(value) is not str or not _ID.fullmatch(value) or int(value) > MAX_INT64:
        raise ValueError("invalid canonical numeric identity")
    return value


def _uuid(value: Any) -> str:
    if type(value) is not str or str(UUID(value)) != value:
        raise ValueError("invalid canonical UUID")
    return value


def validate_source_id(kind: str, source_id: Any) -> str:
    try:
        if kind not in SOURCE_KINDS:
            raise ValueError("invalid kind")
        return _uuid(source_id) if kind == "operation" else canonical_numeric_id(source_id)
    except (ValueError, TypeError, AttributeError):
        raise DomainError(
            "INVALID_ARGUMENT", "A supported resource and canonical source ID are required", 400
        ) from None


def _obj(value: Any) -> dict:
    if type(value) is not dict:
        raise ValueError("object required")
    return value


def _list(value: Any, maximum: int = 1000, *, nullable: bool = False) -> list:
    if value is None and nullable:
        return []
    if type(value) is not list or len(value) > maximum:
        raise ValueError("bounded array required")
    return value


def _integer(value: Any, minimum: int = 0, maximum: int = MAX_INT64) -> int:
    if type(value) is not int or not minimum <= value <= maximum:
        raise ValueError("bounded integer required")
    return value


def _positive(value: Any) -> int:
    return _integer(value, 1)


def _amount(value: Any) -> int:
    return _integer(value, 0, MAX_AMOUNT)


def _signed_amount(value: Any) -> int:
    return _integer(value, -MAX_AMOUNT, MAX_AMOUNT)


def _boolean(value: Any) -> bool:
    if type(value) is not bool:
        raise ValueError("boolean required")
    return value


def _text(value: Any, maximum: int = 2048) -> str:
    if (
        type(value) is not str
        or len(value) > maximum
        or any(ord(c) < 32 and c not in "\n\r\t" for c in value)
    ):
        raise ValueError("bounded text required")
    return value


def _label(value: Any) -> str:
    return _text(value, 128)


def _currency(value: Any) -> str:
    if value != "CNY":
        raise ValueError("unsupported native currency")
    return value


def _strings(value: Any) -> list[str]:
    return [_text(x) for x in _list(value, 100, nullable=True)]


def _select(raw: dict, fields: dict) -> dict:
    return {name: validate(raw[name]) for name, validate in fields.items()}


def _optional(raw: dict, payload: dict, fields: dict) -> None:
    for name, validate in fields.items():
        if name in raw and raw[name] is not None:
            payload[name] = validate(raw[name])


def _base(raw: Any, source_id: str, user_id: str, *, uuid_id: bool = False) -> dict:
    raw = _obj(raw)
    payload = _select(
        raw,
        {
            "id": _uuid if uuid_id else canonical_numeric_id,
            "user_id": canonical_numeric_id,
            "created_at": _positive,
            "updated_at": _positive,
        },
    )
    if payload["id"] != source_id or payload["user_id"] != user_id:
        raise ValueError("source identity or ownership mismatch")
    if payload["updated_at"] < payload["created_at"]:
        raise ValueError("inconsistent source timestamps")
    return payload


def _native_ids(raw: dict, payload: dict) -> str | None:
    audit_id = None
    for name in ("trace_id", "task_id", "audit_id"):
        value = raw.get(name)
        if value in (None, ""):
            continue
        if type(value) is not str or not _CORRELATION.fullmatch(value):
            raise ValueError("invalid native correlation identity")
        payload[name] = value
        if name == "audit_id":
            audit_id = value
    return audit_id


def _evidence(values: Any) -> list[dict]:
    ids = [canonical_numeric_id(x) for x in _list(values, 1000, nullable=True)]
    if len(set(ids)) != len(ids):
        raise ValueError("duplicate source evidence")
    return [{"kind": "transaction", "source_id": value} for value in ids]


def _spec(raw: Any) -> dict:
    raw = _obj(raw)
    payload = _select(
        raw,
        {
            "schema_version": _label,
            "currency": _currency,
            "period_months": lambda x: _integer(x, 1, 120),
            "expense_reduction_monthly_minor": _amount,
            "savings_monthly_minor": _amount,
            "adjustable_categories": _strings,
            "protected_categories": _strings,
            "prohibited_operations": _strings,
        },
    )
    if payload["schema_version"] != "2.0":
        raise ValueError("unsupported goal schema")
    return payload


def _option(raw: Any) -> dict:
    raw = _obj(raw)
    payload = _select(
        raw,
        {
            "name": _label,
            "budget_minor": _amount,
            "expense_reduction_minor": _amount,
            "savings_monthly_minor": _amount,
            "explanation": _text,
        },
    )
    payload["categories"] = [
        _select(
            _obj(row),
            {
                "historical_minor": _amount,
                "category": _label,
                "original_minor": _amount,
                "proposed_minor": _amount,
                "protected": _boolean,
            },
        )
        for row in _list(raw["categories"], 100, nullable=True)
    ]
    return payload


def _account(raw: dict, source_id: str, user_id: str) -> list[Projection]:
    payload = _base(raw, source_id, user_id)
    payload.update(
        _select(
            raw,
            {
                "display_name": _text,
                "account_type": _label,
                "masked_account_number": _label,
                "currency": _currency,
                "balance_minor": _amount,
                "status": _label,
                "version": _positive,
                "is_simulated": _boolean,
            },
        )
    )
    _optional(raw, payload, {"savings_goal_id": canonical_numeric_id})
    return [Projection("account", source_id, payload["version"], payload, [])]


def _transaction(raw: dict, source_id: str, user_id: str) -> list[Projection]:
    payload = _base(raw, source_id, user_id)
    payload.update(
        _select(
            raw,
            {
                "bank_account_id": canonical_numeric_id,
                "direction": _label,
                "amount_minor": lambda x: _integer(x, 1, MAX_AMOUNT),
                "currency": _currency,
                "category": _label,
                "merchant_name": _text,
                "description": _text,
                "occurred_at": _positive,
                "kind": _label,
                "effect_mode": _label,
            },
        )
    )
    if payload["direction"] not in ("income", "expense"):
        raise ValueError("invalid direction")
    _optional(raw, payload, {"journal_id": _label, "transfer_id": canonical_numeric_id})
    audit_id = _native_ids(raw, payload)
    return [Projection("transaction", source_id, payload["updated_at"], payload, [], audit_id)]


def _subscription(raw: dict, source_id: str, user_id: str) -> list[Projection]:
    source = _obj(raw["subscription"])
    payload = _base(source, source_id, user_id)
    payload.update(
        _select(
            source,
            {
                "bank_account_id": canonical_numeric_id,
                "merchant_name": _text,
                "category": _label,
                "billing_interval": _label,
                "amount_minor": _amount,
                "currency": _currency,
                "next_billing_at": _integer,
                "confidence_bps": lambda x: _integer(x, 0, 10000),
                "status": _label,
                "version": _positive,
                "is_simulated": _boolean,
            },
        )
    )
    _optional(source, payload, {"last_used_at": _integer, "cancelled_at": _integer})
    # Native payment upserts can change history/duplicate flags without changing
    # subscription.version. Validate children, but keep this versioned projection
    # limited to the parent; payment history remains available through the Go API.
    seen = set()
    for row in _list(raw["payments"], 1000, nullable=True):
        row = _obj(row)
        payment_id = canonical_numeric_id(row["id"])
        if payment_id in seen:
            raise ValueError("duplicate payment")
        seen.add(payment_id)
        payment = _base(row, payment_id, user_id)
        payment.update(
            _select(
                row,
                {
                    "subscription_id": canonical_numeric_id,
                    "transaction_id": canonical_numeric_id,
                    "amount_minor": _amount,
                    "occurred_at": _positive,
                    "is_possible_duplicate": _boolean,
                },
            )
        )
        if payment["subscription_id"] != source_id:
            raise ValueError("payment source mismatch")
    return [Projection("subscription", source_id, payload["version"], payload, [])]


def _budget(raw: dict, source_id: str, user_id: str) -> list[Projection]:
    payload = _base(raw, source_id, user_id)
    payload.update(_select(raw, {"month": _label, "currency": _currency, "total_minor": _amount}))
    if not re.fullmatch(r"[0-9]{4}-(0[1-9]|1[0-2])", payload["month"], re.ASCII):
        raise ValueError("invalid budget month")
    schemas = {
        "categories": {
            "category": _label,
            "allocated_minor": _amount,
            "essential_reserve_minor": _amount,
        },
        "rules": {"kind": _label, "enabled": _boolean},
    }
    for name, fields in schemas.items():
        items = []
        seen = set()
        for row in _list(raw[name], 100, nullable=True):
            row = _obj(row)
            item = _select(
                row,
                {
                    "id": canonical_numeric_id,
                    "user_id": canonical_numeric_id,
                    "budget_id": canonical_numeric_id,
                    **fields,
                },
            )
            if item["user_id"] != user_id or item["budget_id"] != source_id or item["id"] in seen:
                raise ValueError("budget child identity or ownership mismatch")
            if name == "categories" and item["essential_reserve_minor"] > item["allocated_minor"]:
                raise ValueError("reserve exceeds allocation")
            seen.add(item["id"])
            items.append(item)
        payload[name] = items
    return [Projection("budget", source_id, payload["updated_at"], payload, [])]


def _goal(raw: dict, source_id: str, user_id: str) -> list[Projection]:
    goal = _obj(raw["goal"])
    payload = _base(goal, source_id, user_id)
    payload["current_version"] = _positive(goal["current_version"])
    versions = []
    seen_ids = set()
    seen_versions = set()
    for version in _list(raw["versions"], 100):
        version = _obj(version)
        version_id = canonical_numeric_id(version["id"])
        item = _base(version, version_id, user_id)
        item.update(_select(version, {"goal_id": canonical_numeric_id, "version": _positive}))
        if (
            item["goal_id"] != source_id
            or version_id in seen_ids
            or item["version"] in seen_versions
            or item["version"] > payload["current_version"]
        ):
            raise ValueError("goal version identity mismatch")
        item["spec"] = _spec(version["spec"])
        item["untrusted_text"] = _text(version["text"], 10000)
        _native_ids(version, item)
        seen_ids.add(version_id)
        seen_versions.add(item["version"])
        versions.append(item)
    current = next((x for x in versions if x["version"] == payload["current_version"]), None)
    if current is None:
        raise ValueError("current goal version missing")
    payload.update({"spec": current["spec"], "untrusted_text": current["untrusted_text"]})
    if "trace_id" in current:
        payload["trace_id"] = current["trace_id"]
    return [Projection("lifestyle_goal", source_id, payload["current_version"], payload, [])]


def _plan(raw: dict, source_id: str, user_id: str) -> list[Projection]:
    source = _obj(raw["plan"])
    payload = _base(source, source_id, user_id)
    payload.update(
        _select(
            source,
            {
                "goal_id": canonical_numeric_id,
                "goal_version": _positive,
                "input_digest": _label,
                "expires_at": _positive,
            },
        )
    )
    audit_id = _native_ids(source, payload)
    snapshot = _obj(source["snapshot"])
    payload["snapshot"] = _select(
        snapshot, {"budget_minor": _amount, "complete_month": _boolean, "source_digest": _label}
    )
    payload["snapshot"]["goal"] = _spec(snapshot["goal"])
    payload["snapshot"]["categories"] = [
        _select(
            _obj(row),
            {
                "category": _label,
                "allocated_minor": _amount,
                "essential_reserve_minor": _amount,
                "observed_minor": _amount,
            },
        )
        for row in _list(snapshot["categories"], 100, nullable=True)
    ]
    _optional(snapshot, payload["snapshot"], {"savings_ceiling_minor": _amount})
    sources = _select(
        _obj(snapshot["sources"]),
        {
            "expected_version": _positive,
            "budget_id": lambda x: canonical_numeric_id(x, allow_zero=True),
            "billing_report_id": lambda x: "" if x == "" else _uuid(x),
            "financial_plan_id": lambda x: canonical_numeric_id(x, allow_zero=True),
        },
    )
    if sources["expected_version"] != payload["goal_version"]:
        raise ValueError("plan goal source version mismatch")
    payload["snapshot"]["sources"] = sources
    evidence = _evidence(snapshot["evidence_transaction_ids"])
    payload["snapshot"]["evidence_transaction_ids"] = [x["source_id"] for x in evidence]
    result = _obj(source["result"])
    payload["result"] = _select(
        result,
        {
            "schema_version": _label,
            "rule_version": _label,
            "status": _label,
            "missing_information": _strings,
            "conflicts": _strings,
            "reduction_gap_minor": _amount,
            "savings_gap_minor": _amount,
            "requires_confirmation": _boolean,
            "financial_execution_enabled": _boolean,
        },
    )
    if (
        payload["result"]["schema_version"] != "2.0"
        or payload["result"]["financial_execution_enabled"]
    ):
        raise ValueError("invalid suggestion-only plan result")
    payload["result"]["options"] = [
        _option(x) for x in _list(result["options"], 100, nullable=True)
    ]
    projections = [
        Projection("lifestyle_plan", source_id, payload["created_at"], payload, evidence, audit_id)
    ]
    seen = set()
    for row in _list(raw["recommendations"], 100, nullable=True):
        row = _obj(row)
        recommendation_id = canonical_numeric_id(row["id"])
        if recommendation_id in seen:
            raise ValueError("duplicate recommendation")
        seen.add(recommendation_id)
        recommendation = _base(row, recommendation_id, user_id)
        recommendation.update(
            _select(
                row,
                {
                    "goal_id": canonical_numeric_id,
                    "plan_id": canonical_numeric_id,
                    "type": _label,
                    "expected_impact_minor": _signed_amount,
                    "impact_period": _label,
                    "explanation": _text,
                    "confidence": _label,
                    "status": _label,
                    "version": _positive,
                    "expires_at": _positive,
                    "rule_version": _label,
                },
            )
        )
        if (
            recommendation["goal_id"] != payload["goal_id"]
            or recommendation["plan_id"] != source_id
        ):
            raise ValueError("recommendation plan or goal identity mismatch")
        recommendation["option"] = _option(row["option"])
        projections.append(
            Projection(
                "recommendation",
                recommendation_id,
                recommendation["version"],
                recommendation,
                [dict(x) for x in evidence],
                audit_id,
            )
        )
    return projections


def _operation(raw: dict, source_id: str, user_id: str) -> list[Projection]:
    source = _obj(raw["operation"])
    payload = _base(source, source_id, user_id, uuid_id=True)
    payload.update(
        _select(
            source,
            {
                "kind": _label,
                "permission": _label,
                "agent": _label,
                "resource_id": lambda x: canonical_numeric_id(x, allow_zero=True),
                "source_account_id": lambda x: canonical_numeric_id(x, allow_zero=True),
                "target_account_id": lambda x: canonical_numeric_id(x, allow_zero=True),
                "amount_minor": _amount,
                "currency": lambda x: "" if x == "" else _currency(x),
                "status": _label,
                "version": _positive,
                "expires_at": _integer,
                "risk_level": _label,
                "error_code": _label,
            },
        )
    )
    _optional(source, payload, {"skill_id": _label, "skill_version": _integer})
    audit_id = _native_ids(source, payload)
    return [Projection("operation", source_id, payload["version"], payload, [], audit_id)]


_NORMALIZERS = {
    "account": _account,
    "transaction": _transaction,
    "subscription": _subscription,
    "budget": _budget,
    "lifestyle_goal": _goal,
    "lifestyle_plan": _plan,
    "operation": _operation,
}


def normalize_resource(kind: str, source_id: str, user_id: str, data: Any) -> list[Projection]:
    """Verify requested identities and emit only explicitly allowed native fields."""
    validate_source_id(kind, source_id)
    try:
        canonical_numeric_id(user_id)
        return _NORMALIZERS[kind](_obj(data), source_id, user_id)
    except (ValueError, TypeError, KeyError, AttributeError):
        raise DomainError(
            "SOURCE_OUTPUT_INVALID", "Banking source output failed validation", 502
        ) from None
