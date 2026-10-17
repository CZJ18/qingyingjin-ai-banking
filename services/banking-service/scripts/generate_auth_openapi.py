"""Add browser authentication to the existing Banking contract, never the Coze plugin."""
from pathlib import Path

import yaml


ROOT = Path(__file__).resolve().parents[1]
path = ROOT / "api/openapi.yaml"
doc = yaml.safe_load(path.read_text(encoding="utf-8"))
doc["info"]["version"] = "0.15.0"
if not any(t["name"] == "Authentication" for t in doc["tags"]):
    doc["tags"].append({"name": "Authentication"})

schemas = doc["components"]["schemas"]
phone = {"type": "string", "pattern": r"^(\+86)?1[3-9][0-9]{9}$", "description": "Mainland China mobile login identifier; no SMS verification is claimed."}
password = {"type": "string", "format": "password", "minLength": 8, "maxLength": 128, "writeOnly": True, "description": "8–128 Unicode characters, at most 512 UTF-8 bytes. Whitespace is significant."}
schemas["PhoneCredentials"] = {"type": "object", "additionalProperties": False, "required": ["phone", "password"], "properties": {"phone": phone, "password": password}}
schemas["PhoneRegistration"] = {"type": "object", "additionalProperties": False, "required": ["phone", "password"], "properties": {"phone": phone, "password": password, "display_name": {"type": "string", "maxLength": 128, "description": "Optional display name; at most 128 UTF-8 bytes."}}}
schemas["EmptyAuthRequest"] = {"type": "object", "additionalProperties": False, "properties": {}}
schemas["BrowserSession"] = {"type": "object", "additionalProperties": False, "required": ["access_token", "expires_in", "user", "phone_masked"], "properties": {
    "access_token": {"type": "string", "description": "Short-lived user Bearer JWT. Keep only in memory; never send to an LLM."},
    "expires_in": {"type": "integer", "enum": [900]},
    "user": {"$ref": "#/components/schemas/User"},
    "phone_masked": {"type": "string"},
}}
schemas["BrowserSessionResponse"] = {"allOf": [{"$ref": "#/components/schemas/Envelope"}, {"type": "object", "required": ["data"], "properties": {"data": {"$ref": "#/components/schemas/BrowserSession"}}}]}

def response(description, ref="Envelope"):
    return {"description": description, "content": {"application/json": {"schema": {"$ref": "#/components/schemas/" + ref}}}}

descriptions = {
    "register": "Register an independent simulated user and sign in. Does not claim historical users, open accounts or credit funds.",
    "login": "Sign in with phone and password. A new login revokes the previous browser session.",
    "bind": "Bind credentials once to the verified legacy account owner. User Bearer subject must equal user_id; Agents are forbidden.",
    "refresh": "Restore the current session using the HttpOnly cookie. Does not extend the fixed 24-hour expiry or rotate the identity epoch.",
    "logout": "Idempotently revoke the matching cookie session and its versioned Bearer JWTs. A stale cookie cannot revoke a newer login.",
}
for action in descriptions:
    schema = "PhoneRegistration" if action == "register" else "PhoneCredentials" if action in ("login", "bind") else "EmptyAuthRequest"
    status = "201" if action == "register" else "200"
    success = response("Signed out" if action == "logout" else "Authenticated browser session", "Envelope" if action == "logout" else "BrowserSessionResponse")
    if action in ("register", "login", "bind", "logout"):
        success["headers"] = {"Set-Cookie": {"description": "Host-only qingrongjin_refresh; Path=/api/v1/auth; HttpOnly; SameSite=Strict; Secure by default. Removed on logout. Never appears in JSON.", "schema": {"type": "string"}}}
    parameters = []
    if action in ("refresh", "logout"):
        parameters = [{"in": "header", "name": "Origin", "required": True, "schema": {"type": "string"}, "description": "Must exactly match the current scheme and host including port. Required whenever any authentication cookie is present."}]
    operation = {"operationId": action + "PhoneSession", "tags": ["Authentication"], "description": descriptions[action] + " JSON only, no-store. Auth cookies are never accepted as business API authorization.", "security": [{"UserBearer": []}] if action == "bind" else [], "parameters": parameters,
        "requestBody": {"required": True, "content": {"application/json": {"schema": {"$ref": "#/components/schemas/" + schema}}}},
        "responses": {status: success,
            "400": response("INVALID_ARGUMENT: invalid phone, password or JSON"),
            "401": response("UNAUTHENTICATED or AUTH_INVALID_CREDENTIALS: fixed generic failure; no account existence disclosure"),
            "403": response("FORBIDDEN: invalid Origin or caller"),
            "409": response("AUTH_PHONE_UNAVAILABLE or STATE_CONFLICT: phone or identity already bound"),
            "429": {**response("AUTH_RATE_LIMITED: Redis atomic phone/IP limit"), "headers": {"Retry-After": {"schema": {"type": "string"}}}},
            "503": response("DEPENDENCY_UNAVAILABLE or STORAGE_FROZEN: authentication fails closed"),
            "500": response("INTERNAL_ERROR")}}
    doc["paths"]["/api/v1/auth/" + action] = {"post": operation}
path.write_text(yaml.safe_dump(doc, sort_keys=False, allow_unicode=True, width=100), encoding="utf-8")
