"""Actually restart P4 containers and verify their stored data on the same image."""

import json
import subprocess
import sys
import time
from pathlib import Path

root = Path(__file__).resolve().parents[3]
out = root / "services/banking-agent-service/output/p4"
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


report = {"passed": False, "before": states()}
path = out / "restart-control.json"


def persist():
    path.write_text(json.dumps(report, indent=2) + "\n", encoding="utf8")


persist()
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
persist()
if result.returncode:
    sys.exit(result.returncode)

deadline = time.monotonic() + 40
while True:
    report["after"] = states()
    persist()
    if all(row["running"] and row["health"] == "healthy" for row in report["after"].values()):
        break
    if time.monotonic() >= deadline:
        raise RuntimeError("P4 containers did not become healthy after actual restart")
    time.sleep(0.5)
for name in names:
    before, after = report["before"][name], report["after"][name]
    assert before["container_id"] == after["container_id"]
    assert before["image_id"] == after["image_id"]
    assert before["started_at"] != after["started_at"]
result = subprocess.run(
    [sys.executable, str(Path(__file__).with_name("verify_live.py")), "--restart"],
    cwd=root,
    check=False,
)
report["verification_exit"] = result.returncode
report["passed"] = result.returncode == 0
persist()
sys.exit(result.returncode)
