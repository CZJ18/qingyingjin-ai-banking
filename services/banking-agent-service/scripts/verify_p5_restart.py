"""Restart the actual P5 app, PostgreSQL and Redis, then resume a bound interrupt."""

import json
import subprocess
import sys
import time
from pathlib import Path

root = Path(__file__).resolve().parents[3]
out = root / "services/banking-agent-service/output/p5"
names = [
    "qingrongjin-agent-banking-agent-postgres-1",
    "qingrongjin-agent-banking-agent-redis-1",
    "qingrongjin-agent-banking-agent-service-1",
]


def states():
    rows = json.loads(subprocess.check_output(["docker", "inspect", *names], encoding="utf8"))
    return {
        row["Name"].lstrip("/"): {
            "container_id": row["Id"],
            "image_id": row["Image"],
            "started_at": row["State"]["StartedAt"],
            "running": row["State"]["Running"],
            "health": row["State"].get("Health", {}).get("Status"),
        }
        for row in rows
    }


state = json.loads((out / "live-state.json").read_text(encoding="utf8"))
assert state.get("completed_task_id") and state.get("waiting_task_id"), (
    "run verify_p5_live.py first"
)
report = {"passed": False, "before": states()}
path = out / "restart-control.json"
if path.exists() and not json.loads(path.read_text(encoding="utf8")).get("passed"):
    path.rename(out / ("restart-control-failed-" + str(time.time_ns()) + ".json"))


def save():
    path.write_text(json.dumps(report, indent=2) + "\n", encoding="utf8")


save()
command = [
    "docker",
    "compose",
    "--env-file",
    "deploy/banking-agent/.env",
    "-f",
    "deploy/banking-agent/compose.yaml",
    "-f",
    "deploy/banking-agent/compose.test.yaml",
    "restart",
    "banking-agent-postgres",
    "banking-agent-redis",
    "banking-agent-service",
]
result = subprocess.run(command, cwd=root, check=False, capture_output=True)
(out / "restart.log").write_bytes(result.stdout + result.stderr)
report["restart_exit"] = result.returncode
save()
if result.returncode:
    sys.exit(result.returncode)
deadline = time.monotonic() + 50
while True:
    report["after"] = states()
    save()
    if all(row["running"] and row["health"] == "healthy" for row in report["after"].values()):
        break
    if time.monotonic() >= deadline:
        raise RuntimeError("P5 containers did not become healthy after restart")
    time.sleep(0.5)
for name in names:
    before, after = report["before"][name], report["after"][name]
    assert before["container_id"] == after["container_id"]
    assert before["image_id"] == after["image_id"]
    assert before["started_at"] != after["started_at"]
result = subprocess.run(
    [sys.executable, str(Path(__file__).with_name("verify_p5_live.py")), "--restart"],
    cwd=root,
    check=False,
)
report["verification_exit"] = result.returncode
report["passed"] = result.returncode == 0
save()
sys.exit(result.returncode)
