"""Export the actual FastAPI schema without starting connections or inventing endpoints."""

import json
import sys
from pathlib import Path

service = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(service / "src"))
from banking_agent.agent_models import AgentSubmit  # noqa: E402
from banking_agent.main import app  # noqa: E402

destination = service / "api/openapi.json"
destination.parent.mkdir(exist_ok=True)
destination.write_text(
    json.dumps(app.openapi(), ensure_ascii=False, indent=2) + "\n", encoding="utf8"
)
print("Exported actual OpenAPI:", destination)
(service / "api/agent-task.schema.json").write_text(
    json.dumps(AgentSubmit.model_json_schema(), ensure_ascii=False, indent=2) + "\n", encoding="utf8"
)
