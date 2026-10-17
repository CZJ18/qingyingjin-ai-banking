"""Read actual tenant data with the restricted runtime role; never print credentials."""

import json
import re
import sys
from pathlib import Path

root = Path(__file__).resolve().parents[3]
service = root / "services/banking-agent-service"
sys.path.insert(0, str(service / "src"))
from banking_agent.repository import Repository  # noqa: E402


def env(path):
    return dict(
        line.split("=", 1)
        for line in path.read_text(encoding="utf-8-sig").splitlines()
        if "=" in line and not line.startswith("#")
    )


out = service / "output/p5"
state = json.loads((out / "live-state.json").read_text(encoding="utf8"))
private = env(root / "deploy/banking-agent/.env.local")
key = private["BANKING_AGENT_SERVICE_KEY"].encode()
deepseek_path = root / "deploy/banking/.env.deepseek.local"
provider_key = (
    env(deepseek_path).get("DEEPSEEK_API_KEY", "").encode() if deepseek_path.exists() else b""
)
repo = Repository(private["BANKING_AGENT_DATABASE_URL"])
tables = (
    "agent_tasks",
    "agent_tool_calls",
    "agent_confirmations",
    "agent_task_events",
    "agent_checkpoints",
    "agent_checkpoint_writes",
)
jwt = re.compile(rb"eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+")
report = {"passed": False, "counts": {}, "checks": {}}
try:
    assert repo.ready()
    with repo._transaction(state["user_id"]) as conn:
        metadata = conn.execute(
            "SELECT relname,relrowsecurity,relforcerowsecurity,pg_get_userbyid(relowner) AS owner "
            "FROM pg_class WHERE relnamespace='public'::regnamespace AND relname=ANY(%s)",
            (list(tables),),
        ).fetchall()
        role = conn.execute("SELECT current_user AS name").fetchone()["name"]
        assert len(metadata) == 6 and all(
            row["relrowsecurity"] and row["relforcerowsecurity"] and row["owner"] != role
            for row in metadata
        )
        # Table names are the fixed local allowlist above, never request/model input.
        for table in tables:
            rows = conn.execute("SELECT * FROM public." + table).fetchall()
            report["counts"][table] = len(rows)
            assert rows, "actual P5 acceptance data must exist"
            for row in rows:
                assert row["owner_id"] == state["user_id"]
                for value in row.values():
                    if isinstance(value, (bytes, memoryview)):
                        raw = bytes(value)
                    else:
                        raw = json.dumps(value, default=str, ensure_ascii=False).encode()
                    assert key not in raw and not jwt.search(raw), "credential persistence detected"
                    assert not provider_key or provider_key not in raw, (
                        "provider credential persistence detected"
                    )
            if table == "agent_tasks":
                assert all(row["owner_engine"] == "langgraph" for row in rows)
                assert all(row["task_id"].startswith("lg-") for row in rows)
        prepared = conn.execute(
            "SELECT result,state FROM public.agent_tool_calls WHERE task_id=%s AND node_key=%s",
            (state["waiting_task_id"], "prepare_transfer"),
        ).fetchall()
        assert len(prepared) == 1 and prepared[0]["state"] == "succeeded"
        assert prepared[0]["result"]["result"]["id"] == state["prepared_operation_id"]
        parallel = conn.execute(
            "SELECT created_at,updated_at FROM public.agent_tool_calls "
            "WHERE task_id=%s AND node_key=ANY(%s)",
            (state["completed_task_id"], ["billing", "subscription"]),
        ).fetchall()
        assert len(parallel) == 2
        assert max(row["created_at"] for row in parallel) < min(
            row["updated_at"] for row in parallel
        )
    with repo._transaction("p5-not-the-owner") as conn:
        assert all(
            conn.execute("SELECT count(*) AS n FROM public." + table).fetchone()["n"] == 0
            for table in tables
        )
    with repo.pool.connection() as conn:
        assert all(
            conn.execute("SELECT count(*) AS n FROM public." + table).fetchone()["n"] == 0
            for table in tables
        )
    report["checks"].update(
        restricted_role_and_six_force_rls=True,
        actual_checkpoints_and_pending_writes=True,
        no_bearer_or_service_credentials_persisted=True,
        no_deepseek_key_in_product_persistence=True,
        one_task_engine=True,
        one_prepared_operation_journal_after_replay=True,
        actual_read_only_tool_intervals_overlap=True,
        other_owner_and_pool_context_isolated=True,
    )
    report["passed"] = True
finally:
    repo.close()
    (out / "storage.json").write_text(json.dumps(report, indent=2) + "\n", encoding="utf8")
print(json.dumps({"passed": True, "counts": report["counts"], "checks": report["checks"]}))
