"""Record actual P4 execution evidence and source hashes; never dump connection secrets."""

import hashlib
import json
import subprocess
import sys
import urllib.request
import xml.etree.ElementTree as ET
from pathlib import Path

service = Path(__file__).resolve().parents[1]
root = service.parents[1]
out = service / "output/p4"
sys.path.insert(0, str(service / "src"))
from banking_agent.main import app  # noqa: E402


def load(name):
    return json.loads((out / name).read_text(encoding="utf-8-sig"))


def inspect(name):
    return json.loads(subprocess.check_output(["docker", "inspect", name], encoding="utf8"))[0]


checks = load("checks.json")
assert checks["integration"] and all(
    checks[k] == 0 for k in ("ruff_exit", "test_exit", "compile_exit")
)
suite = ET.parse(out / "pytest.xml").getroot().find("testsuite")
tests = {key: int(suite.attrib[key]) for key in ("tests", "failures", "errors", "skipped")}
assert tests["tests"] and not any(tests[k] for k in ("failures", "errors", "skipped"))
live, restart = load("live.json"), load("live-restart.json")
assert live["passed"] and restart["passed"] and restart["restart"]
restart_control = load("restart-control.json")
assert restart_control["passed"] and restart_control["restart_exit"] == 0
assert restart_control["verification_exit"] == 0
for name, before in restart_control["before"].items():
    after = restart_control["after"][name]
    assert before["container_id"] == after["container_id"]
    assert before["image_id"] == after["image_id"]
    assert before["started_at"] != after["started_at"]
    assert after["running"] and after["health"] == "healthy"
for name in ("receipt", "projected_account", "profile", "recommendation", "evidence"):
    assert live["responses"][name]["body"]["data"] == restart["responses"][name]["body"]["data"]
runtime = inspect("qingrongjin-agent-banking-agent-service-1")
assert runtime["State"]["Running"] and runtime["State"]["Health"]["Status"] == "healthy"
before = restart_control["before"][runtime["Name"].lstrip("/")]
after = restart_control["after"][runtime["Name"].lstrip("/")]
assert live["runtime"]["started_at"] == before["started_at"]
assert restart["runtime"]["started_at"] == after["started_at"] == runtime["State"]["StartedAt"]
assert live["runtime"]["image_id"] == restart["runtime"]["image_id"] == runtime["Image"]
build = load("build.json")
assert build["exit_code"] == 0 and build["image_id"] == runtime["Image"]
p3 = load("p3-regression.json")
assert p3["exit_code"] == 0
p3_checks = json.loads((out / "p3-regression.log").read_text(encoding="utf-8-sig"))
assert p3_checks["/healthz"] == p3_checks["/readyz"] == 200
assert all(
    p3_checks[key] is True
    for key in (
        "execution_ids_persisted",
        "balance_unchanged",
        "wrong_role_403",
        "authoritative_amounts_unchanged",
        "no_automatic_retry",
        "real_lifestyle_explanation",
        "real_evidence_references",
    )
)
assert all(value is True for key, value in p3_checks.items() if not key.startswith("/"))
assert runtime["Config"]["User"] == "10001:10001" and runtime["HostConfig"]["ReadonlyRootfs"]
env_names = sorted(line.split("=", 1)[0] for line in runtime["Config"]["Env"])
assert "BANKING_AGENT_MIGRATION_URL" not in env_names
assert not any(
    name.endswith("_JWT_SECRET")
    or name.endswith("_PAT")
    or name in ("PAT", "BANKING_TOOL_TOKEN", "COZE_ACCESS_TOKEN")
    for name in env_names
)
assert "--no-access-log" in runtime["Config"]["Cmd"]
with urllib.request.urlopen("http://127.0.0.1:8091/readyz", timeout=5) as response:
    readiness = json.load(response)
assert readiness["data"]["dependencies"] == {
    "postgresql": "ready",
    "redis": "ready",
    "banking": "ready",
}
with urllib.request.urlopen("http://127.0.0.1:8091/openapi.json", timeout=5) as response:
    runtime_api = json.load(response)
source_api = app.openapi()
published_api = json.loads((service / "api/openapi.json").read_text(encoding="utf8"))
assert runtime_api == source_api == published_api

# Read safe schema metadata through the already initialized PostgreSQL container.
pg_query = 'PGPASSWORD="$POSTGRES_PASSWORD" psql --no-psqlrc -h 127.0.0.1 -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atc "SELECT version,checksum FROM agent_schema_migrations ORDER BY version"'
pg_version, pg_checksum = (
    subprocess.check_output(
        ["docker", "exec", "qingrongjin-agent-banking-agent-postgres-1", "sh", "-c", pg_query],
        encoding="utf8",
    )
    .strip()
    .split("|")
)
assert (
    int(pg_version) == 1
    and pg_checksum
    == hashlib.sha256((service / "migrations/001_projection.sql").read_bytes()).hexdigest()
)
mysql_query = 'MYSQL_PWD="$MYSQL_PASSWORD" mysql -u "$MYSQL_USER" "$MYSQL_DATABASE" -Nse "SELECT MAX(version) FROM banking_schema_migrations"'
mysql_version = int(
    subprocess.check_output(
        ["docker", "exec", "qingyingjin-banking-mysql-1", "sh", "-c", mysql_query], encoding="utf8"
    ).strip()
)
assert mysql_version == 10
go_runtime = inspect("qingyingjin-banking-service-1")
previous_go = json.loads(
    (root / "services/banking-service/output/module11/acceptance.json").read_text(encoding="utf8")
)["container"]
assert go_runtime["Image"] == previous_go["image_id"]

# Compare actual in-container code with local source, not merely an image tag.
container_hashes = json.loads(
    subprocess.check_output(
        [
            "docker",
            "exec",
            runtime["Name"].lstrip("/"),
            "python",
            "-c",
            "import pathlib,hashlib,json; p=pathlib.Path('/app'); print(json.dumps({str(f.relative_to(p)):hashlib.sha256(f.read_bytes()).hexdigest() for d in ['src','migrations'] for f in (p/d).rglob('*') if f.is_file() and f.suffix in ['.py','.sql']}))",
        ],
        encoding="utf8",
    )
)
local_hashes = {
    path.relative_to(service).as_posix(): hashlib.sha256(path.read_bytes()).hexdigest()
    for folder in ("src", "migrations")
    for path in (service / folder).rglob("*")
    if path.is_file() and path.suffix in (".py", ".sql")
}
assert local_hashes == container_hashes

paths = [
    service / "pyproject.toml",
    service / "uv.lock",
    service / "Dockerfile",
    service / "Dockerfile.dockerignore",
    service / "README.md",
]
for folder in ("src", "migrations", "tests", "scripts", "api"):
    paths += [
        p for p in (service / folder).rglob("*") if p.is_file() and "__pycache__" not in p.parts
    ]
deployment = root / "deploy/banking-agent"
paths += [p for p in deployment.iterdir() if p.is_file() and p.name not in (".env", ".env.local")]
paths += [
    root / ".gitignore",
    root / "services/banking-service/MODULE12.md",
    root / "docs/superpowers/plans/2026-10-13-qingrongjin-projection-p4.md",
    root / "docs/superpowers/plans/2026-10-13-qingrongjin-upgrade-roadmap.md",
]
files = [
    {"path": str(path), "sha256": hashlib.sha256(path.read_bytes()).hexdigest()}
    for path in sorted(set(paths))
]
(out / "files.json").write_text(
    json.dumps(
        {
            "description": "P4 intentionally implemented or updated source files; not a Git diff",
            "files": files,
        },
        ensure_ascii=False,
        indent=2,
    )
    + "\n",
    encoding="utf8",
)
report = {
    "checks": checks,
    "pytest": tests,
    "build": {**build, "succeeded": True},
    "live": {"passed": live["passed"], "checks": live["checks"]},
    "restart": {
        "passed": restart["passed"],
        "checks": restart["checks"],
        "same_stored_data": True,
        "actual_restart": restart_control,
    },
    "container": {
        "image": runtime["Config"]["Image"],
        "image_id": runtime["Image"],
        "health": runtime["State"]["Health"]["Status"],
        "user": runtime["Config"]["User"],
        "readonly_rootfs": True,
        "env_names_only": env_names,
        "source_hashes_match": True,
    },
    "readiness": readiness["data"],
    "schema": {
        "postgresql": int(pg_version),
        "postgresql_checksum": pg_checksum,
        "mysql": mysql_version,
        "go_runtime_unchanged": True,
    },
    "openapi": {
        "version": runtime_api["openapi"],
        "runtime_matches_export": True,
        "paths": len(runtime_api["paths"]),
    },
    "p3_regression": {
        "passed": True,
        "exit_code": p3["exit_code"],
        "checks": p3_checks,
        "kind": "persisted results read; no new model run",
    },
    "financial_execution_enabled": False,
    "file_count": len(files),
    "known_limits": [
        "Explicit refresh only; no CDC or automatic complete synchronization.",
        "Subscription payment history remains in Go; parent projection excludes unversioned children.",
        "Transaction/budget same-millisecond source conflicts fail closed.",
        "Profile preferences do not modify Banking Core or gateway policies.",
        "Starlette TestClient emits an httpx compatibility deprecation warning.",
        "P5 delegated Agent identity and LangGraph, P6 frontend and P7 funds migration are not implemented.",
    ],
}
assert report["build"]["succeeded"] and report["p3_regression"]["passed"]
(out / "acceptance.json").write_text(
    json.dumps(report, ensure_ascii=False, indent=2) + "\n", encoding="utf8"
)
print(
    json.dumps(
        {
            "pytest": tests,
            "container": {
                k: report["container"][k] for k in ("image", "health", "source_hashes_match")
            },
            "schema": report["schema"],
            "file_count": len(files),
        },
        ensure_ascii=False,
    )
)
