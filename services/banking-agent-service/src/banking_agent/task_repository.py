"""Durable tenant tasks, execution leases, tool journal and bound approvals."""

from __future__ import annotations

import hashlib
import json
import re
from datetime import UTC, datetime
from uuid import UUID, uuid4

from psycopg.types.json import Jsonb

from . import monthly_plan
from .errors import DomainError
from .repository import Repository, _text

MAX_BODY = 1048576


def task_display_title(request):
    """Describe a stored query for history, keeping the actual instruction separate."""
    if not isinstance(request, dict):
        return "结构化业务任务"
    instruction = request.get("instruction")
    if isinstance(instruction, str) and instruction.strip():
        return instruction[:512]
    specs = request.get("tasks")
    if not isinstance(specs, list) or len(specs) != 1 or not isinstance(specs[0], dict):
        return "结构化业务任务"
    spec = specs[0]
    if spec.get("execution_mode") != "read_only":
        return "结构化业务任务"
    if not isinstance(spec.get("agent_type"), str) or not isinstance(spec.get("tool"), str):
        return "结构化业务任务"
    name = {("billing", "generate_billing_report"): "账单分析",
            ("subscription", "detect_subscriptions"): "订阅识别"}.get(
        (spec.get("agent_type"), spec.get("tool")))
    if spec.get("agent_type") == "supervisor" and spec.get("tool") == "get_scenario_overview":
        parameters = spec.get("parameters")
        scenario = parameters.get("scenario") if isinstance(parameters, dict) else None
        name = {"transfer": "转账范围", "cards": "卡片管理", "investment": "理财产品",
                "subscriptions": "订阅范围", "birthday": "生日安排"}.get(scenario) if isinstance(scenario, str) else None
    return "只读查询：" + name if name else "结构化业务任务"


def body(value):
    try:
        encoded = json.dumps(value, ensure_ascii=False, allow_nan=False, separators=(",", ":"))
        if len(encoded.encode("utf-8")) > MAX_BODY:
            raise ValueError("size")
        return json.loads(encoded)
    except (ValueError, TypeError, OverflowError) as exc:
        raise DomainError("INVALID_INPUT", "Body must be valid JSON at most 1 MiB", 400) from exc


def conflict(code="TASK_CONFLICT"):
    raise DomainError(code, "Task state or version conflicts with this operation", 409)


class TaskRepository:
    def __init__(self, repository: Repository):
        self.repository = repository

    def _transaction(self, user, *, readonly=False):
        return self.repository._transaction(user, readonly=True) if readonly else self.repository._transaction(user)

    def model_context(self, user, node_task_id):
        try:
            root, node = node_task_id.split(".")
            if (
                not root.startswith("lg-")
                or str(UUID(root[3:])) != root[3:]
                or not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9_-]{0,63}", node)
            ):
                raise ValueError
        except (ValueError, AttributeError, TypeError):
            raise DomainError(
                "INVALID_ARGUMENT", "Canonical task node identity required", 422
            ) from None
        journal_node = "__internal:supervisor_plan" if node == "supervisor_plan" else node
        with self._transaction(user) as conn:
            row = self._owned(conn, user, root, True)
            if not row["lease_token"] or not self._active(row, row["lease_token"]):
                conflict("MODEL_TASK_NOT_ACTIVE")
            call = conn.execute(
                "SELECT state FROM public.agent_tool_calls WHERE owner_id=%s AND task_id=%s AND node_key=%s",
                (user, root, journal_node),
            ).fetchone()
            if not call or call["state"] != "running":
                conflict("MODEL_TASK_NOT_ACTIVE")
            return {
                "owner_id": user,
                "task_id": node_task_id,
                "request_digest": row["request_digest"],
                "version": row["version"],
                "lease_fingerprint": hashlib.sha256(row["lease_token"].encode()).hexdigest(),
            }

    def enroll(self, user, task, request_digest, capability, expires_in_seconds):
        if (
            not isinstance(capability, str)
            or not capability.startswith("bgt1.")
            or not 16 <= len(capability) <= 8192
            or type(expires_in_seconds) is not int
            or not 1 <= expires_in_seconds <= 86400
        ):
            raise DomainError("TOOL_OUTPUT_INVALID", "Invalid background delegation", 502)
        with self._transaction(user) as conn:
            row = self._owned(conn, user, task, True)
            if row["request_digest"] != request_digest:
                conflict("IDEMPOTENCY_CONFLICT")
            conn.execute(
                "INSERT INTO public.agent_background_credentials(owner_id,task_id,capability,expires_at) VALUES(%s,%s,%s,clock_timestamp()+(%s*interval '1 second')) ON CONFLICT(owner_id,task_id) DO UPDATE SET capability=EXCLUDED.capability,expires_at=EXCLUDED.expires_at",
                (user, task, capability, expires_in_seconds),
            )

    def candidates(self):
        with self.repository.pool.connection() as conn:
            return conn.execute("SELECT * FROM public.agent_background_candidates()").fetchall()

    def background_claim(self, user, task, lease_seconds=30):
        with self._transaction(user) as conn:
            row = self._owned(conn, user, task, True)
            credential = conn.execute(
                "SELECT capability,expires_at FROM public.agent_background_credentials WHERE owner_id=%s AND task_id=%s",
                (user, task),
            ).fetchone()
            if (
                not credential
                or row["next_run_at"] > datetime.now(UTC)
                or not (
                    row["status"] == "queued"
                    or (row["status"] == "running" and row["lease_until"] <= datetime.now(UTC))
                )
            ):
                return None
            terminal = (
                "BACKGROUND_RETRIES_EXHAUSTED"
                if row["background_attempts"] >= 3
                else "BACKGROUND_DELEGATION_EXPIRED"
                if credential["expires_at"] <= datetime.now(UTC)
                else ""
            )
            if terminal:
                row = conn.execute(
                    "UPDATE public.agent_tasks SET status='failed',error_code=%s,lease_token=NULL,lease_until=NULL,version=version+1,updated_at=clock_timestamp() WHERE owner_id=%s AND task_id=%s RETURNING *",
                    (terminal, user, task),
                ).fetchone()
                self._event(conn, row, error=terminal)
                return None
            # An expired worker may have sent a paid/model or non-idempotent call.
            # Retain its journal and stop for inspection instead of replaying it.
            unsafe = conn.execute(
                "SELECT 1 FROM public.agent_tool_calls WHERE owner_id=%s AND task_id=%s AND state IN ('unknown','running') LIMIT 1",
                (user, task),
            ).fetchone()
            if unsafe:
                row = conn.execute(
                    "UPDATE public.agent_tasks SET status='interrupted',error_code='TOOL_OUTCOME_UNKNOWN',lease_token=NULL,lease_until=NULL,version=version+1,updated_at=clock_timestamp() WHERE owner_id=%s AND task_id=%s RETURNING *",
                    (user, task),
                ).fetchone()
                self._event(conn, row, error="TOOL_OUTCOME_UNKNOWN")
                return None
            token = str(uuid4())
            row = conn.execute(
                "UPDATE public.agent_tasks SET status='running',background_attempts=background_attempts+1,lease_token=%s,lease_until=clock_timestamp()+(%s*interval '1 second'),version=version+1,updated_at=clock_timestamp() WHERE owner_id=%s AND task_id=%s RETURNING *",
                (token, lease_seconds, user, task),
            ).fetchone()
            self._event(conn, row)
            return {
                "lease": token,
                "capability": credential["capability"],
                "trace_id": row["trace_id"],
            }

    def renew(self, user, task, lease, lease_seconds=30):
        with self._transaction(user) as conn:
            row = self._owned(conn, user, task, True)
            if not self._active(row, lease):
                return False
            conn.execute(
                "UPDATE public.agent_tasks SET lease_until=clock_timestamp()+(%s*interval '1 second') WHERE owner_id=%s AND task_id=%s",
                (lease_seconds, user, task),
            )
            return True

    def retry_background(self, user, task):
        with self._transaction(user) as conn:
            row = self._owned(conn, user, task, True)
            if not self._retry_eligible(conn, row):
                return False
            row = self._queue_retry(conn, row)
            self._event(conn, row)
            return True

    @staticmethod
    def _retry_eligible(conn, row):
        if (
            row["status"] != "failed"
            or not 0 < row["background_attempts"] < 3
            or row["error_code"] not in ("BANKING_UNAVAILABLE", "DEPENDENCY_UNAVAILABLE")
        ):
            return False
        calls = conn.execute(
            "SELECT node_key,mode,state,error_code FROM public.agent_tool_calls WHERE owner_id=%s AND task_id=%s AND state<>'succeeded'",
            (row["owner_id"], row["task_id"]),
        ).fetchall()
        if any(
            call["state"] != "failed"
            or call["mode"] not in ("read_only", "intent")
            or call["node_key"] == "__internal:supervisor_plan"
            or call["error_code"] not in ("BANKING_UNAVAILABLE", "DEPENDENCY_UNAVAILABLE")
            for call in calls
        ):
            return False
        return True

    @staticmethod
    def _queue_retry(conn, row):
        return conn.execute(
            "UPDATE public.agent_tasks SET status='queued',next_run_at=clock_timestamp()+(%s*interval '1 second'),version=version+1,updated_at=clock_timestamp() WHERE owner_id=%s AND task_id=%s RETURNING *",
            (2 ** row["background_attempts"], row["owner_id"], row["task_id"]),
        ).fetchone()

    @staticmethod
    def _owned(conn, user, task, lock=False):
        row = conn.execute(
            "SELECT * FROM public.agent_tasks WHERE owner_id=%s AND task_id=%s AND owner_engine='langgraph'"
            + (" FOR UPDATE" if lock else ""),
            (user, task),
        ).fetchone()
        if row is None:
            raise DomainError("TASK_NOT_FOUND", "Task does not exist", 404)
        return row

    @staticmethod
    def _active(row, lease):
        return (
            row["status"] == "running"
            and row["lease_token"] == lease
            and row["lease_until"] is not None
            and row["lease_until"] > datetime.now(UTC)
        )

    @staticmethod
    def _cas(row, expected):
        if type(expected) is not int or expected != row["version"]:
            conflict("TASK_VERSION_CONFLICT")

    @staticmethod
    def _event(conn, row, node="", error=""):
        conn.execute(
            "INSERT INTO public.agent_task_events(owner_id,task_id,seq,status,node_key,error_code,trace_id) SELECT %s,%s,COALESCE(max(seq),0)+1,%s,%s,%s,%s FROM public.agent_task_events WHERE owner_id=%s AND task_id=%s",
            (
                row["owner_id"],
                row["task_id"],
                row["status"],
                node,
                error,
                row["trace_id"],
                row["owner_id"],
                row["task_id"],
            ),
        )

    @staticmethod
    def _view(conn, row):
        result = {
            key: row[key]
            for key in (
                "task_id",
                "owner_engine",
                "status",
                "version",
                "request",
                "result",
                "error_code",
                "created_at",
                "updated_at",
            )
        }
        for key in ("created_at", "updated_at"):
            result[key] = result[key].isoformat()
        if row["status"] in ("failed", "interrupted"):
            uncertain = conn.execute(
                "SELECT node_key FROM public.agent_tool_calls WHERE owner_id=%s AND task_id=%s AND state IN ('unknown','running') ORDER BY CASE WHEN node_key='__internal:supervisor_plan' THEN 0 ELSE 1 END,node_key LIMIT 1",
                (row["owner_id"], row["task_id"]),
            ).fetchone()
            if uncertain:
                # This read-only projection reflects the durable call journal.
                # Preserve the original task error, events and fee records.
                result["error_code"] = (
                    "MODEL_OUTCOME_UNKNOWN"
                    if uncertain["node_key"] == "__internal:supervisor_plan"
                    else "TOOL_OUTCOME_UNKNOWN"
                )
        challenge = conn.execute(
            "SELECT node_key,digest,nonce,expires_at,status,parameters_view FROM public.agent_confirmations WHERE owner_id=%s AND task_id=%s AND status='pending' ORDER BY created_at DESC LIMIT 1",
            (row["owner_id"], row["task_id"]),
        ).fetchone()
        if challenge:
            challenge["expires_at"] = challenge["expires_at"].isoformat()
            challenge["confirmation_scope"] = challenge["parameters_view"].get(
                "confirmation_scope", "PREPARE_ONLY"
            )
        result["confirmation"] = challenge
        return result

    def submit(self, user, key, request_digest, request_dict, trace):
        _text(key, "idempotency_key")
        _text(request_digest, "request_digest")
        _text(trace, "trace_id")
        request = body(request_dict)
        if not isinstance(request, dict):
            raise DomainError("INVALID_INPUT", "Task request must be an object", 400)
        with self._transaction(user) as conn:
            self.repository._lock_identity(conn, user)
            existing = conn.execute(
                "SELECT * FROM public.agent_tasks WHERE owner_id=%s AND idempotency_key=%s",
                (user, key),
            ).fetchone()
            if existing:
                if existing["request_digest"] != request_digest:
                    conflict("IDEMPOTENCY_CONFLICT")
                return self._view(conn, existing)
            row = conn.execute(
                "INSERT INTO public.agent_tasks(owner_id,task_id,idempotency_key,request_digest,request,trace_id) VALUES(%s,%s,%s,%s,%s,%s) RETURNING *",
                (user, "lg-" + str(uuid4()), key, request_digest, Jsonb(request), trace),
            ).fetchone()
            self._event(conn, row)
            return self._view(conn, row)

    def get(self, user, task):
        with self._transaction(user) as conn:
            return self._view(conn, self._owned(conn, user, task))

    def list_recent(self, user, limit=20, offset=0):
        if type(limit) is not int or not 1 <= limit <= 100 or type(offset) is not int or not 0 <= offset <= 1000000:
            raise DomainError("INVALID_ARGUMENT", "Bounded task history pagination required", 400)
        # Tenant RLS and explicit owner predicate both apply. History cannot claim
        # a lease, load background capabilities or generate a confirmation/grant.
        with self._transaction(user, readonly=True) as conn:
            total = conn.execute(
                "SELECT count(*) AS total FROM public.agent_tasks WHERE owner_id=%s", (user,)
            ).fetchone()["total"]
            rows = conn.execute(
                "SELECT task_id,status,version,left(COALESCE(request->>'instruction',''),512) AS instruction,request,"
                "error_code,created_at,updated_at FROM public.agent_tasks WHERE owner_id=%s "
                "ORDER BY created_at DESC,task_id DESC LIMIT %s OFFSET %s", (user, limit, offset)
            ).fetchall()
            for row in rows:
                row["display_title"] = task_display_title(row.pop("request", None))
                for key in ("created_at", "updated_at"):
                    row[key] = row[key].isoformat()
            return {"items": rows, "total": total, "limit": limit, "offset": offset}

    def claim(self, user, task, expected_version=None, lease_seconds=120):
        if type(lease_seconds) is not int or not 1 <= lease_seconds <= 3600:
            raise DomainError("INVALID_INPUT", "Invalid lease duration", 400)
        with self._transaction(user) as conn:
            row = self._owned(conn, user, task, True)
            if expected_version is not None:
                self._cas(row, expected_version)
            recoverable = (
                row["status"] == "running"
                and row["lease_until"] is not None
                and row["lease_until"] <= datetime.now(UTC)
            )
            if row["status"] not in ("queued", "interrupted", "failed") and not recoverable:
                conflict()
            token = str(uuid4())
            row = conn.execute(
                "UPDATE public.agent_tasks SET status='running',version=version+1,lease_token=%s,lease_until=clock_timestamp()+(%s * interval '1 second'),updated_at=clock_timestamp() WHERE owner_id=%s AND task_id=%s RETURNING *",
                (token, lease_seconds, user, task),
            ).fetchone()
            self._event(conn, row)
            return token

    def finish(self, user, task, lease, status, result=None, error_code=""):
        if status not in ("succeeded", "failed", "interrupted", "waiting_confirmation"):
            raise DomainError("INVALID_INPUT", "Invalid completion status", 400)
        result = body(result)
        with self._transaction(user) as conn:
            row = self._owned(conn, user, task, True)
            if row["status"] == "cancelled":
                return self._view(conn, row)
            if not self._active(row, lease):
                conflict("LEASE_CONFLICT")
            if status == "waiting_confirmation":
                pending = conn.execute(
                    "UPDATE public.agent_confirmations SET waiting_version=%s WHERE owner_id=%s AND task_id=%s AND status='pending' RETURNING node_key",
                    (row["version"] + 1, user, task),
                ).fetchall()
                if not pending:
                    conflict("CONFIRMATION_CONFLICT")
            row = conn.execute(
                "UPDATE public.agent_tasks SET status=%s,result=%s,error_code=%s,lease_token=NULL,lease_until=NULL,background_attempts=CASE WHEN %s='waiting_confirmation' THEN 0 ELSE background_attempts END,version=version+1,updated_at=clock_timestamp() WHERE owner_id=%s AND task_id=%s RETURNING *",
                (status, Jsonb(result), error_code, status, user, task),
            ).fetchone()
            if self._retry_eligible(conn, row):
                row = self._queue_retry(conn, row)
            self._event(conn, row, error=error_code)
            return self._view(conn, row)

    def cancel(self, user, task, expected_version):
        with self._transaction(user) as conn:
            row = self._owned(conn, user, task, True)
            self._cas(row, expected_version)
            if row["status"] == "succeeded":
                conflict()
            if row["status"] != "cancelled":
                row = conn.execute(
                    "UPDATE public.agent_tasks SET status='cancelled',lease_token=NULL,lease_until=NULL,version=version+1,updated_at=clock_timestamp() WHERE owner_id=%s AND task_id=%s RETURNING *",
                    (user, task),
                ).fetchone()
                self._event(conn, row)
            return self._view(conn, row)

    def check_active(self, user, task, lease):
        with self._transaction(user) as conn:
            return self._active(self._owned(conn, user, task), lease)

    def call_get(self, user, task, node):
        with self._transaction(user) as conn:
            self._owned(conn, user, task)
            return conn.execute(
                "SELECT * FROM public.agent_tool_calls WHERE owner_id=%s AND task_id=%s AND node_key=%s",
                (user, task, node),
            ).fetchone()

    def call_start(self, user, task, lease, node, request_digest, mode):
        _text(node, "node_key")
        _text(request_digest, "request_digest")
        _text(mode, "mode")
        with self._transaction(user) as conn:
            taskrow = self._owned(conn, user, task, True)
            if not self._active(taskrow, lease):
                conflict("LEASE_CONFLICT")
            row = conn.execute(
                "SELECT * FROM public.agent_tool_calls WHERE owner_id=%s AND task_id=%s AND node_key=%s",
                (user, task, node),
            ).fetchone()
            if row:
                if row["request_digest"] != request_digest or row["mode"] != mode:
                    conflict("TOOL_CALL_CONFLICT")
                if (
                    row["state"] == "failed"
                    and mode == "read_only"
                    and node != "__internal:supervisor_plan"
                    and row["error_code"]
                    in ("BANKING_UNAVAILABLE", "DEPENDENCY_UNAVAILABLE", "TOOL_TIMEOUT")
                ):
                    row = conn.execute(
                        "UPDATE public.agent_tool_calls SET state='running',error_code='',result=NULL,updated_at=clock_timestamp() WHERE owner_id=%s AND task_id=%s AND node_key=%s RETURNING *",
                        (user, task, node),
                    ).fetchone()
                    self._event(conn, taskrow, node)
                return row
            row = conn.execute(
                "INSERT INTO public.agent_tool_calls(owner_id,task_id,node_key,request_digest,mode,state) VALUES(%s,%s,%s,%s,%s,'running') RETURNING *",
                (user, task, node, request_digest, mode),
            ).fetchone()
            self._event(conn, taskrow, node)
            return row

    def _call_complete(self, user, task, node, digest, status, result, error, lease_token=None):
        result = body(result)
        with self._transaction(user) as conn:
            taskrow = self._owned(conn, user, task, True)
            if taskrow["status"] == "cancelled" or (
                lease_token is not None and not self._active(taskrow, lease_token)
            ):
                conflict("LEASE_CONFLICT")
            row = conn.execute(
                "SELECT * FROM public.agent_tool_calls WHERE owner_id=%s AND task_id=%s AND node_key=%s",
                (user, task, node),
            ).fetchone()
            if not row or row["request_digest"] != digest:
                conflict("TOOL_CALL_CONFLICT")
            if row["state"] == "succeeded":
                return row
            row = conn.execute(
                "UPDATE public.agent_tool_calls SET state=%s,result=%s,error_code=%s,updated_at=clock_timestamp() WHERE owner_id=%s AND task_id=%s AND node_key=%s RETURNING *",
                (status, Jsonb(result), error, user, task, node),
            ).fetchone()
            self._event(conn, taskrow, node, error)
            return row

    def call_finish(self, user, task, node, request_digest, result, *, lease_token=None):
        return self._call_complete(
            user, task, node, request_digest, "succeeded", result, "", lease_token
        )

    def call_fail(
        self, user, task, node, request_digest, error_code, status="failed", *, lease_token=None
    ):
        if status not in ("failed", "unknown"):
            raise DomainError("INVALID_INPUT", "Invalid tool call failure state", 400)
        return self._call_complete(
            user, task, node, request_digest, status, None, error_code, lease_token
        )

    def challenge(self, user, task, lease, node, digest, parameters_view):
        parameters = body(parameters_view)
        _text(node, "node_key")
        _text(digest, "digest")
        with self._transaction(user) as conn:
            row = self._owned(conn, user, task, True)
            prior = conn.execute(
                "SELECT node_key,digest,nonce,expires_at,status,parameters_view FROM public.agent_confirmations WHERE owner_id=%s AND task_id=%s AND node_key=%s",
                (user, task, node),
            ).fetchone()
            if prior:
                if prior["digest"] != digest or prior["parameters_view"] != parameters:
                    conflict("CONFIRMATION_CONFLICT")
                return {
                    key: (value.isoformat() if key == "expires_at" else value)
                    for key, value in prior.items()
                    if key != "parameters_view"
                }
            if not self._active(row, lease):
                conflict("LEASE_CONFLICT")
            pending = conn.execute(
                "SELECT node_key FROM public.agent_confirmations WHERE owner_id=%s AND task_id=%s AND status='pending'",
                (user, task),
            ).fetchone()
            if pending:
                conflict("CONFIRMATION_CONFLICT")
            challenge = conn.execute(
                "INSERT INTO public.agent_confirmations(owner_id,task_id,node_key,digest,nonce,parameters_view,waiting_version,expires_at) VALUES(%s,%s,%s,%s,%s,%s,%s,clock_timestamp()+interval '10 minutes') RETURNING node_key,digest,nonce,expires_at,status",
                (user, task, node, digest, str(uuid4()), Jsonb(parameters), row["version"] + 1),
            ).fetchone()
            self._event(conn, row, node)
            challenge["expires_at"] = challenge["expires_at"].isoformat()
            return challenge

    def confirm(self, user, task, expected_version, node, digest, nonce, decision):
        if decision not in ("approve", "reject"):
            raise DomainError("INVALID_INPUT", "Invalid confirmation decision", 400)
        status = "approved" if decision == "approve" else "rejected"
        with self._transaction(user) as conn:
            row = self._owned(conn, user, task, True)
            challenge = conn.execute(
                "SELECT * FROM public.agent_confirmations WHERE owner_id=%s AND task_id=%s AND node_key=%s",
                (user, task, node),
            ).fetchone()
            if (
                not challenge
                or challenge["digest"] != digest
                or challenge["nonce"] != nonce
                or challenge["waiting_version"] != expected_version
            ):
                conflict("CONFIRMATION_CONFLICT")
            goal = row["request"].get("monthly_goal")
            if goal and goal["period"] != monthly_plan.current_period():
                conflict("MONTHLY_GOAL_EXPIRED")
            if challenge["status"] != "pending":
                if challenge["status"] != status:
                    conflict("CONFIRMATION_CONFLICT")
                return self._view(conn, row)
            self._cas(row, expected_version)
            if row["status"] != "waiting_confirmation" or challenge["expires_at"] <= datetime.now(
                UTC
            ):
                conflict("CONFIRMATION_EXPIRED")
            conn.execute(
                "UPDATE public.agent_confirmations SET status=%s,confirmed_at=clock_timestamp() WHERE owner_id=%s AND task_id=%s AND node_key=%s",
                (status, user, task, node),
            )
            row = conn.execute(
                "UPDATE public.agent_tasks SET status='queued',version=version+1,updated_at=clock_timestamp() WHERE owner_id=%s AND task_id=%s RETURNING *",
                (user, task),
            ).fetchone()
            self._event(conn, row, node)
            return self._view(conn, row)

    def approved(self, user, task, node, digest):
        with self._transaction(user) as conn:
            self._owned(conn, user, task)
            row = conn.execute(
                "SELECT status,expires_at FROM public.agent_confirmations WHERE owner_id=%s AND task_id=%s AND node_key=%s AND digest=%s",
                (user, task, node, digest),
            ).fetchone()
            return bool(
                row and row["status"] == "approved" and row["expires_at"] > datetime.now(UTC)
            )

    def decision(self, user, task, node, digest):
        """Read an existing, still-valid decision without granting new authority."""
        with self._transaction(user) as conn:
            self._owned(conn, user, task)
            row = conn.execute(
                "SELECT node_key,digest,status,expires_at FROM public.agent_confirmations WHERE owner_id=%s AND task_id=%s AND node_key=%s AND digest=%s AND status IN ('approved','rejected') AND expires_at>clock_timestamp()",
                (user, task, node, digest),
            ).fetchone()
            if not row:
                return None
            return {
                "decision": "approve" if row["status"] == "approved" else "reject",
                "node_key": row["node_key"],
                "digest": row["digest"],
                "expires_at": row["expires_at"].isoformat(),
            }

    def events(self, user, task, after=0, limit=100):
        if type(after) is not int or after < 0 or type(limit) is not int or not 1 <= limit <= 200:
            raise DomainError("INVALID_INPUT", "Invalid event pagination", 400)
        with self._transaction(user) as conn:
            self._owned(conn, user, task)
            rows = conn.execute(
                "SELECT seq,status,node_key,error_code,trace_id,created_at FROM public.agent_task_events WHERE owner_id=%s AND task_id=%s AND seq>%s ORDER BY seq LIMIT %s",
                (user, task, after, limit),
            ).fetchall()
            for row in rows:
                row["created_at"] = row["created_at"].isoformat()
            return rows
