"""Record executed P5 evidence; a model or cleanup failure stays explicitly incomplete."""

import hashlib
import json
import subprocess
import sys
import urllib.request
import xml.etree.ElementTree as ET
from datetime import UTC, datetime
from pathlib import Path

service = Path(__file__).resolve().parents[1]
root = service.parents[1]
out = service / "output/p5"
sys.path.insert(0, str(service / "src"))
from banking_agent.main import app  # noqa: E402
from banking_agent.repository import Repository  # noqa: E402


def load(path):
    return json.loads(path.read_text(encoding="utf-8-sig"))


def env(path):
    return dict(
        line.split("=", 1)
        for line in path.read_text(encoding="utf-8-sig").splitlines()
        if "=" in line and not line.startswith("#")
    )


def inspect(name):
    return json.loads(subprocess.check_output(["docker", "inspect", name], encoding="utf8"))[0]


checks = load(out / "checks.json")
assert checks["integration"] and all(
    checks[name] == 0 for name in ("ruff_exit", "test_exit", "compile_exit")
)
suite = ET.parse(out / "pytest.xml").getroot().find("testsuite")
tests = {name: int(suite.attrib[name]) for name in ("tests", "failures", "errors", "skipped")}
assert tests["tests"] and not any(tests[name] for name in ("failures", "errors", "skipped"))
live, restart, control, storage = [
    load(out / name)
    for name in (
        "live.json",
        "live-restart.json",
        "restart-control.json",
        "storage.json",
    )
]
assert all(value["passed"] for value in (live, restart, control, storage))
assert control["restart_exit"] == control["verification_exit"] == 0
for name, before in control["before"].items():
    after = control["after"][name]
    assert before["container_id"] == after["container_id"]
    assert before["image_id"] == after["image_id"]
    assert before["started_at"] != after["started_at"] and after["health"] == "healthy"
go_checks = load(root / "services/banking-service/output/module13/go-checks.final.json")
assert go_checks["exit_code"] == 0
builds = {name: load(out / ("build-" + name + ".json")) for name in ("agent", "go")}
assert all(value["exit_code"] == 0 for value in builds.values())
runtime = inspect("qingrongjin-agent-banking-agent-service-1")
assert runtime["State"]["Running"] and runtime["State"]["Health"]["Status"] == "healthy"
assert runtime["Config"]["User"] == "10001:10001" and runtime["HostConfig"]["ReadonlyRootfs"]
assert runtime["HostConfig"]["CapDrop"] == ["ALL"]
env_values = dict(line.split("=", 1) for line in runtime["Config"]["Env"])
assert env_values["LANGSMITH_TRACING"] == env_values["LANGCHAIN_TRACING_V2"] == "false"
assert not any(
    name.endswith("_JWT_SECRET")
    or name.endswith("_PAT")
    or name
    in (
        "BANKING_AGENT_MIGRATION_URL",
        "BANKING_TOOL_TOKEN",
        "DEEPSEEK_API_KEY",
    )
    for name in env_values
)
with urllib.request.urlopen("http://127.0.0.1:8091/openapi.json", timeout=5) as response:
    actual_api = json.load(response)
assert actual_api == app.openapi() == load(service / "api/openapi.json")
with urllib.request.urlopen("http://127.0.0.1:8091/readyz", timeout=5) as response:
    ready = json.load(response)
assert ready["data"]["dependencies"] == {
    "postgresql": "ready",
    "redis": "ready",
    "banking": "ready",
}
with urllib.request.urlopen("http://127.0.0.1:8090/openapi.yaml", timeout=5) as response:
    assert response.read() == (root / "services/banking-service/api/openapi.yaml").read_bytes()
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
assert container_hashes == local_hashes
private = env(root / "deploy/banking-agent/.env.local")
repo = Repository(private["BANKING_AGENT_DATABASE_URL"])
try:
    assert repo.ready()
    with repo.pool.connection() as conn:
        migrations = conn.execute(
            "SELECT version,checksum FROM agent_schema_migrations ORDER BY version"
        ).fetchall()
    assert [row["version"] for row in migrations] == [1, 2]
    for row in migrations:
        path = (
            service
            / "migrations"
            / ("001_projection.sql" if row["version"] == 1 else "002_langgraph.sql")
        )
        assert row["checksum"] == hashlib.sha256(path.read_bytes()).hexdigest()
finally:
    repo.close()
mysql_query = 'MYSQL_PWD="$MYSQL_PASSWORD" mysql -u "$MYSQL_USER" "$MYSQL_DATABASE" -Nse "SELECT MAX(version) FROM banking_schema_migrations"'
mysql_version = int(
    subprocess.check_output(
        [
            "docker",
            "exec",
            "qingyingjin-banking-mysql-1",
            "sh",
            "-c",
            mysql_query,
        ],
        encoding="utf8",
    ).strip()
)
assert mysql_version == 10
models = load(out / "live-models.json")
model_exit = load(out / "models-exit.json")
model_passed = models.get("passed") is True and model_exit["exit_code"] == 0
coze_binding = load(root / "output/deepseek-config-result.json")
assert coze_binding["model"] == "deepseek-flash" and coze_binding["provider"] == "DeepSeek"
agri_source = load(root / "output/p5/agri-backend-source-checks.json")
agri_runtime = load(root / "output/p5/agri-runtime-checks.json")
agri_build = load(root / "output/p5/agri-backend-build.json")
assert agri_source["passed"] and agri_runtime["passed"] and agri_build["exit_code"] == 0
model_removal = load(out / "model-switch.json")
record = {
    "stage": "P5",
    "recorded_at": datetime.now(UTC).isoformat(),
    "p5_accepted": model_passed,
    "all_requested_cleanup_complete": model_removal["removed"],
    "tests": tests,
    "python_checks": checks,
    "go_checks": go_checks,
    "builds": builds,
    "real_http_checks": live["checks"],
    "restart_checks": restart["checks"],
    "storage_checks": storage["checks"],
    "model_checks": models["checks"],
    "model_verification_exit": model_exit["exit_code"],
    "coze_model": coze_binding,
    "postgresql_migrations": migrations,
    "mysql_migration": mysql_version,
    "openapi_matches_runtime": True,
    "container_source_hashes_match": True,
    "runtime": {
        "image_id": runtime["Image"],
        "started_at": runtime["State"]["StartedAt"],
        "env_names": sorted(env_values),
        "source_hashes": local_hashes,
    },
    "agriculture_routes_removed": agri_runtime,
    "local_model_removal": model_removal,
    "limitations": [
        "P5 prepares pending intents; user financial authorization/execution remains in Go gateway.",
        "Recovery requires fresh user authentication; there is no persisted-token worker.",
        "Native DeepSeek builder keeps supplier default thinking mode; output and deadlines are validated.",
    ]
    + (
        []
        if model_removal["removed"]
        else [
            "Local Qwen binary file deletion was rejected by automatic policy and requires user action."
        ]
    ),
}
(out / "acceptance.json").write_text(
    json.dumps(record, ensure_ascii=False, indent=2) + "\n", encoding="utf8"
)
print(
    json.dumps(
        {"p5_accepted": model_passed, "tests": tests, "cleanup_complete": model_removal["removed"]}
    )
)
