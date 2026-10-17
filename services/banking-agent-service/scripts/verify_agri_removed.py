"""Check removed business routes with an authenticated native Coze session."""

import json
import urllib.error
import urllib.request
from pathlib import Path

root = Path(__file__).resolve().parents[3]
private = json.loads((root / "output/coze-local-private.json").read_text(encoding="utf8"))
base = "http://127.0.0.1:8888"
cookie = private.get("cookie", "")
report = {"passed": False, "responses": {}}


def call(path, body=None):
    req = urllib.request.Request(
        base + path,
        data=None if body is None else json.dumps(body).encode(),
        headers={"Content-Type": "application/json", "Cookie": cookie},
    )
    try:
        response = urllib.request.urlopen(req, timeout=15)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        raw = response.read()
        try:
            value = json.loads(raw)
        except ValueError:
            value = {}
        # Retain status/code only: native payloads may contain identity material.
        report["responses"][path] = {"status": response.status, "code": value.get("code")}
        return response.status, value


try:
    status, value = call("/api/playground_api/space/list", {})
    if status != 200 or value.get("code") != 0:
        login = urllib.request.Request(
            base + "/api/passport/web/email/login/",
            data=json.dumps({"email": private["email"], "password": private["password"]}).encode(),
            headers={"Content-Type": "application/json"},
        )
        with urllib.request.urlopen(login, timeout=15) as response:
            assert response.status == 200 and json.load(response).get("code") == 0
            cookie = "; ".join(
                item.split(";", 1)[0] for item in response.headers.get_all("Set-Cookie") or []
            )
        status, value = call("/api/playground_api/space/list", {})
    assert status == 200 and value.get("code") == 0, "native session validation required"
    for path in ("/api/agri/dashboard/overview", "/api/agri/collaboration-sessions"):
        status, _ = call(path)
        assert status == 404, "former agricultural route remains active"
    report["passed"] = True
finally:
    (root / "output/p5/agri-runtime-checks.json").write_text(
        json.dumps(report, indent=2) + "\n", encoding="utf8"
    )
print(json.dumps(report))
