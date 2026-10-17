"""Tenant-scoped read-model persistence; no bank signing key or funds ledger."""

from __future__ import annotations

import hashlib
import json
from collections.abc import Iterator
from contextlib import contextmanager
from dataclasses import dataclass
from datetime import datetime
from typing import Any
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

import psycopg
from psycopg.rows import dict_row
from psycopg.types.json import Jsonb
from psycopg_pool import ConnectionPool

from .errors import DomainError

KINDS = frozenset({
    "account", "transaction", "subscription", "budget", "lifestyle_goal",
    "lifestyle_plan", "recommendation", "operation",
})
MAX_BIGINT = 2**63 - 1
MAX_SAFETY_RESERVE_MINOR = 1_000_000_000_000


@dataclass(frozen=True)
class PreparedProjection:
    kind: str
    source_id: str
    source_version: int
    payload: dict[str, Any]
    evidence: list[dict[str, Any]]
    audit_id: str | None
    source_digest: str


def _text(value: Any, name: str) -> str:
    if not isinstance(value, str) or not value or value != value.strip() or "\x00" in value:
        raise DomainError("INVALID_INPUT", f"{name} must be a nonempty canonical string", 400)
    return value


def _prepare(projection: Any) -> PreparedProjection:
    try:
        kind, source_id = projection.kind, projection.source_id
        version = projection.source_version
        payload, evidence, audit_id = projection.payload, projection.evidence, projection.audit_id
        if kind not in KINDS or not isinstance(source_id, str) or not source_id or "\x00" in source_id:
            raise ValueError("Unknown kind or empty source_id")
        if type(version) is not int or not 0 < version <= MAX_BIGINT:
            raise ValueError("source_version must be a positive bigint")
        if not isinstance(payload, dict):
            raise ValueError("payload must be a JSON object")
        if not isinstance(evidence, list) or not all(isinstance(item, dict) for item in evidence):
            raise ValueError("evidence must be a list of JSON objects")
        if audit_id is not None and (not isinstance(audit_id, str) or "\x00" in audit_id):
            raise ValueError("audit_id must be text or null")
        # Round-trip to a private JSON snapshot so caller mutation cannot race the
        # digest or alter an already prepared projection inside a transaction.
        canonical = json.dumps(
            {"payload": payload, "evidence": evidence, "audit_id": audit_id},
            ensure_ascii=False, sort_keys=True, separators=(",", ":"), allow_nan=False,
        )
        data = json.loads(canonical)
        digest = hashlib.sha256(canonical.encode("utf-8")).hexdigest()
        return PreparedProjection(kind, source_id, version, data["payload"], data["evidence"], audit_id, digest)
    except (AttributeError, TypeError, ValueError, OverflowError) as error:
        raise DomainError("INVALID_PROJECTION", "Projection is not valid versioned JSON", 400) from error


def projection_digest(projection: Any) -> str:
    return _prepare(projection).source_digest


def prepare_batch(projections: list[Any]) -> tuple[list[PreparedProjection], int, int]:
    selected: dict[tuple[str, str], PreparedProjection] = {}
    versions: dict[tuple[str, str, int], str] = {}
    duplicates = stale = 0
    for raw in projections:
        projection = _prepare(raw)
        key = (projection.kind, projection.source_id)
        version_key = (*key, projection.source_version)
        prior_digest = versions.get(version_key)
        if prior_digest is not None:
            if prior_digest != projection.source_digest:
                raise DomainError("SOURCE_VERSION_CONFLICT", "A source version has conflicting data", 409)
            duplicates += 1
            continue
        versions[version_key] = projection.source_digest
        prior = selected.get(key)
        if prior is None:
            selected[key] = projection
        elif projection.source_version > prior.source_version:
            selected[key] = projection
            stale += 1
        else:
            stale += 1
    return list(selected.values()), duplicates, stale


def _clear_context(connection: psycopg.Connection) -> None:
    connection.execute("SELECT set_config('app.user_id', '', false)")
    connection.commit()


class Repository:
    def __init__(self, conninfo: str):
        self.pool = ConnectionPool(
            conninfo, min_size=1, max_size=10, timeout=10,
            kwargs={"row_factory": dict_row, "application_name": "banking-agent"},
            configure=_clear_context, reset=_clear_context,
            open=True,
        )

    def close(self) -> None:
        self.pool.close()

    @contextmanager
    def _transaction(self, user_id: str) -> Iterator[psycopg.Connection]:
        _text(user_id, "user_id")
        with self.pool.connection() as connection:
            with connection.transaction():
                connection.execute("SELECT set_config('app.user_id', %s, true)", (user_id,))
                yield connection

    def ready(self) -> bool:
        try:
            with self.pool.connection() as connection:
                role = connection.execute(
                    "SELECT current_user AS name, rolsuper, rolbypassrls, rolcanlogin, "
                    "rolcreatedb, rolcreaterole, rolreplication FROM pg_roles WHERE rolname=current_user"
                ).fetchone()
                if not role or role["name"] != "banking_agent_app" or not role["rolcanlogin"]:
                    return False
                if any(role[name] for name in ("rolsuper", "rolbypassrls", "rolcreatedb", "rolcreaterole", "rolreplication")):
                    return False
                privileged = connection.execute(
                    "SELECT EXISTS (SELECT 1 FROM pg_roles WHERE "
                    "(rolsuper OR rolbypassrls OR rolcreatedb OR rolcreaterole OR rolreplication) "
                    "AND pg_has_role(current_user, oid, 'MEMBER')) AS inherited, "
                    "EXISTS (SELECT 1 FROM pg_namespace WHERE pg_has_role(current_user, nspowner, 'MEMBER')) AS schema_owner, "
                    "EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace "
                    "WHERE n.nspname='public' AND c.relname LIKE 'agent_%' "
                    "AND pg_has_role(current_user, c.relowner, 'MEMBER')) AS table_owner, "
                    "EXISTS (SELECT 1 FROM pg_database WHERE datname=current_database() "
                    "AND pg_has_role(current_user, datdba, 'MEMBER')) AS database_owner"
                ).fetchone()
                if any(privileged.values()):
                    return False
                schema = connection.execute(
                    "SELECT count(*) AS n, bool_and(c.relrowsecurity AND c.relforcerowsecurity) AS rls "
                    "FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace "
                    "WHERE n.nspname='public' AND c.relname = ANY(%s)",
                    (["agent_identities", "agent_financial_profiles", "agent_projection_resources", "agent_projection_receipts", "agent_tasks", "agent_tool_calls", "agent_confirmations", "agent_task_events", "agent_checkpoints", "agent_checkpoint_writes"],),
                ).fetchone()
                if schema["n"] != 10 or schema["rls"] is not True:
                    return False
                rights = connection.execute(
                    "SELECT has_schema_privilege(current_user, 'public', 'CREATE') AS ddl, "
                    "EXISTS (SELECT 1 FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace "
                    "WHERE n.nspname='public' AND c.relname LIKE 'agent_%' "
                    "AND (has_table_privilege(current_user, c.oid, 'DELETE') "
                    "OR has_table_privilege(current_user, c.oid, 'TRUNCATE'))) AS destructive"
                ).fetchone()
                if any(rights.values()):
                    return False
                migration = connection.execute(
                    "SELECT max(version) AS version FROM public.agent_schema_migrations"
                ).fetchone()
                return migration["version"] == 2
        except (psycopg.Error, RuntimeError):
            return False

    @staticmethod
    def _lock_identity(connection: psycopg.Connection, user_id: str) -> None:
        identity = connection.execute(
            "SELECT owner_id FROM public.agent_identities WHERE owner_id=%s FOR UPDATE", (user_id,),
        ).fetchone()
        if identity is None:
            raise DomainError("IDENTITY_NOT_FOUND", "Identity must be initialized first", 409)

    def ensure_identity(self, user_id: str, coze_user_id: str | None, trace_id: str) -> dict[str, Any]:
        _text(user_id, "user_id")
        if coze_user_id is not None and not isinstance(coze_user_id, str):
            raise DomainError("INVALID_INPUT", "coze_user_id must be text", 400)
        coze_id = _text(coze_user_id, "coze_user_id") if coze_user_id else None
        try:
            with self._transaction(user_id) as connection:
                connection.execute(
                    "INSERT INTO public.agent_identities(owner_id, banking_user_id, coze_user_id, trace_id) "
                    "VALUES (%s,%s,%s,%s) ON CONFLICT (owner_id) DO NOTHING",
                    (user_id, user_id, coze_id, trace_id),
                )
                identity = connection.execute(
                    "SELECT owner_id, banking_user_id, coze_user_id FROM public.agent_identities "
                    "WHERE owner_id=%s FOR UPDATE", (user_id,),
                ).fetchone()
                if coze_id and identity["coze_user_id"] and coze_id != identity["coze_user_id"]:
                    raise DomainError("IDENTITY_CONFLICT", "Identity is already bound to another Coze user", 409)
                if coze_id and not identity["coze_user_id"]:
                    connection.execute(
                        "UPDATE public.agent_identities SET coze_user_id=%s, trace_id=%s WHERE owner_id=%s",
                        (coze_id, trace_id, user_id),
                    )
                    identity["coze_user_id"] = coze_id
                return {"user_id": user_id, "banking_user_id": user_id, "coze_user_id": identity["coze_user_id"]}
        except psycopg.errors.UniqueViolation as error:
            raise DomainError("IDENTITY_CONFLICT", "Coze identity is already bound", 409) from error

    def profile(self, user_id: str) -> dict[str, Any]:
        with self._transaction(user_id) as connection:
            row = connection.execute(
                "SELECT currency, timezone, safety_reserve_minor, revision "
                "FROM public.agent_financial_profiles WHERE owner_id=%s", (user_id,),
            ).fetchone()
            return {"user_id": user_id, **(row or {
                "currency": "CNY", "timezone": "Asia/Shanghai", "safety_reserve_minor": 0, "revision": 0,
            })}

    def save_profile(self, user_id: str, expected_version: int, timezone: str,
                     safety_reserve_minor: int, trace_id: str) -> dict[str, Any]:
        if type(expected_version) is not int or not 0 <= expected_version < MAX_BIGINT:
            raise DomainError("INVALID_INPUT", "expected_version must be a nonnegative revision", 400)
        if type(safety_reserve_minor) is not int or not 0 <= safety_reserve_minor <= MAX_SAFETY_RESERVE_MINOR:
            raise DomainError("INVALID_INPUT", "safety_reserve_minor must be between 0 and 1000000000000", 400)
        try:
            ZoneInfo(timezone)
        except (ZoneInfoNotFoundError, TypeError, ValueError) as error:
            raise DomainError("INVALID_INPUT", "timezone must be a valid IANA timezone", 400) from error
        with self._transaction(user_id) as connection:
            self._lock_identity(connection, user_id)
            existing = connection.execute(
                "SELECT revision FROM public.agent_financial_profiles WHERE owner_id=%s", (user_id,),
            ).fetchone()
            current = existing["revision"] if existing else 0
            if current != expected_version:
                raise DomainError("PROFILE_VERSION_CONFLICT", "Profile revision has changed", 409)
            row = connection.execute(
                "INSERT INTO public.agent_financial_profiles(owner_id, timezone, safety_reserve_minor, revision, trace_id) "
                "VALUES (%s,%s,%s,%s,%s) ON CONFLICT (owner_id) DO UPDATE SET "
                "timezone=EXCLUDED.timezone, safety_reserve_minor=EXCLUDED.safety_reserve_minor, "
                "revision=EXCLUDED.revision, trace_id=EXCLUDED.trace_id, updated_at=clock_timestamp() "
                "RETURNING currency, timezone, safety_reserve_minor, revision",
                (user_id, timezone, safety_reserve_minor, current + 1, trace_id),
            ).fetchone()
            return {"user_id": user_id, **row}

    @staticmethod
    def _receipt(connection: psycopg.Connection, user_id: str, event_id: str,
                 request_digest: str | None = None) -> dict[str, Any] | None:
        row = connection.execute(
            "SELECT request_digest, result FROM public.agent_projection_receipts "
            "WHERE owner_id=%s AND event_id=%s", (user_id, event_id),
        ).fetchone()
        if row is None:
            return None
        if request_digest is not None and row["request_digest"] != request_digest:
            raise DomainError("IDEMPOTENCY_CONFLICT", "Event key was already used for another request", 409)
        return row["result"]

    def receipt(self, user_id: str, event_id: str, request_digest: str) -> dict[str, Any] | None:
        _text(event_id, "event_id")
        _text(request_digest, "request_digest")
        with self._transaction(user_id) as connection:
            return self._receipt(connection, user_id, event_id, request_digest)

    def event(self, user_id: str, event_id: str) -> dict[str, Any] | None:
        _text(event_id, "event_id")
        with self._transaction(user_id) as connection:
            return self._receipt(connection, user_id, event_id)

    def apply(self, user_id: str, event_id: str, request_digest: str,
              projections: list[Any], trace_id: str) -> dict[str, Any]:
        _text(event_id, "event_id")
        _text(request_digest, "request_digest")
        # Replay is checked before processing refreshed source observations: the
        # recorded outcome remains stable even if the source has since changed.
        with self._transaction(user_id) as connection:
            self._lock_identity(connection, user_id)
            receipt = self._receipt(connection, user_id, event_id, request_digest)
            if receipt is not None:
                return receipt
            batch, duplicates, stale = prepare_batch(projections)
            result: dict[str, Any] = {
                "event_id": event_id, "applied": 0, "duplicates": duplicates,
                "stale": stale, "resources": [],
            }
            for projection in batch:
                existing = connection.execute(
                    "SELECT source_version, source_digest, observed_at FROM public.agent_projection_resources "
                    "WHERE owner_id=%s AND kind=%s AND source_id=%s",
                    (user_id, projection.kind, projection.source_id),
                ).fetchone()
                retained = existing
                status = "applied"
                if existing and existing["source_version"] > projection.source_version:
                    status = "stale"
                    result["stale"] += 1
                elif existing and existing["source_version"] == projection.source_version:
                    if existing["source_digest"] != projection.source_digest:
                        raise DomainError("SOURCE_VERSION_CONFLICT", "A source version has conflicting data", 409)
                    status = "duplicate"
                    result["duplicates"] += 1
                else:
                    retained = connection.execute(
                        "INSERT INTO public.agent_projection_resources "
                        "(owner_id,kind,source_id,source_version,payload,evidence,audit_id,source_digest,trace_id) "
                        "VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s) "
                        "ON CONFLICT (owner_id,kind,source_id) DO UPDATE SET "
                        "source_version=EXCLUDED.source_version, payload=EXCLUDED.payload, "
                        "evidence=EXCLUDED.evidence, audit_id=EXCLUDED.audit_id, "
                        "source_digest=EXCLUDED.source_digest, trace_id=EXCLUDED.trace_id, observed_at=clock_timestamp() "
                        "RETURNING source_version, source_digest, observed_at",
                        (user_id, projection.kind, projection.source_id, projection.source_version,
                         Jsonb(projection.payload), Jsonb(projection.evidence), projection.audit_id,
                         projection.source_digest, trace_id),
                    ).fetchone()
                    result["applied"] += 1
                result["resources"].append({
                    "kind": projection.kind, "source_id": projection.source_id,
                    "source_version": retained["source_version"],
                    "incoming_source_version": projection.source_version, "status": status,
                    "observed_at": retained["observed_at"].isoformat(),
                    "source_digest": retained["source_digest"],
                })
            connection.execute(
                "INSERT INTO public.agent_projection_receipts(owner_id,event_id,request_digest,result,trace_id) "
                "VALUES (%s,%s,%s,%s,%s)", (user_id, event_id, request_digest, Jsonb(result), trace_id),
            )
            return result

    def query(self, user_id: str, kind: str, source_id: str | None = None,
              limit: int = 50, offset: int = 0) -> list[dict[str, Any]]:
        if kind not in KINDS:
            raise DomainError("INVALID_INPUT", "Unknown projection kind", 400)
        if type(limit) is not int or not 1 <= limit <= 200 or type(offset) is not int or offset < 0:
            raise DomainError("INVALID_INPUT", "Pagination is outside the supported range", 400)
        if source_id is not None:
            _text(source_id, "source_id")
        with self._transaction(user_id) as connection:
            rows = connection.execute(
                "SELECT kind,source_id,source_version,payload,evidence,audit_id,observed_at,source_digest "
                "FROM public.agent_projection_resources WHERE owner_id=%s AND kind=%s "
                "AND (%s::text IS NULL OR source_id=%s) "
                "ORDER BY observed_at DESC, source_id LIMIT %s OFFSET %s",
                (user_id, kind, source_id, source_id, limit, offset),
            ).fetchall()
            for row in rows:
                if isinstance(row["observed_at"], datetime):
                    row["observed_at"] = row["observed_at"].isoformat()
            return rows
