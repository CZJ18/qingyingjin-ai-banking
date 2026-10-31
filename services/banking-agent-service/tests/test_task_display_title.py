"""History titles describe persisted tools; they never invent an instruction or authorize work."""
from contextlib import contextmanager
from datetime import UTC, datetime

import pytest

from banking_agent.task_repository import TaskRepository, task_display_title


@pytest.mark.parametrize(("scenario", "name"), [
    ("transfer", "转账范围"), ("cards", "卡片管理"),
    ("investment", "理财产品"), ("birthday", "生日安排"),
])
def test_scenario_titles_are_distinct_without_sensitive_parameters(scenario, name):
    request = {"instruction": "", "tasks": [{"agent_type": "supervisor", "tool": "get_scenario_overview",
        "execution_mode": "read_only", "parameters": {"scenario": scenario, "account_id": "23", "secret": "never-show"}}]}
    assert task_display_title(request) == "只读查询：" + name
    assert "never-show" not in task_display_title(request)


def test_billing_subscription_and_original_instruction_titles():
    for role, tool, name in [("billing", "generate_billing_report", "账单分析"),
                             ("subscription", "detect_subscriptions", "订阅识别")]:
        request = {"tasks": [{"agent_type": role, "tool": tool, "execution_mode": "read_only"}]}
        assert task_display_title(request) == "只读查询：" + name
        assert "instruction" not in request
    assert task_display_title({"instruction": "😀" * 600}) == "😀" * 512


def test_unknown_or_historical_shapes_stay_readable_without_invented_success():
    for request in [None, [], {"tasks": []}, {"tasks": [None]}, {"tasks": [{"tool": "execute", "parameters": {"label": "approved"}}]},
                    {"tasks": [{"agent_type": [], "tool": {}, "execution_mode": "read_only"}]}]:
        assert task_display_title(request) == "结构化业务任务"
    assert task_display_title({"tasks": [{"agent_type": "billing", "tool": "generate_billing_report", "execution_mode": "intent"}]}) == "结构化业务任务"


def test_history_adds_only_a_title_leaves_instruction_exact_and_keeps_readonly_query():
    now = datetime(2026, 10, 30, tzinfo=UTC)
    source = {"instruction": "", "tasks": [{"agent_type": "billing", "tool": "generate_billing_report", "execution_mode": "read_only"}]}
    row = {"task_id": "lg-11111111-1111-4111-8111-111111111111", "status": "succeeded", "version": 3,
        "instruction": "", "request": source, "error_code": "", "created_at": now, "updated_at": now}
    calls = []

    class Result:
        def fetchone(self): return {"total": 1}
        def fetchall(self): return [dict(row)]

    class Connection:
        def execute(self, query, params):
            assert query.startswith("SELECT ")
            calls.append((query, params))
            return Result()

    class Repo:
        @contextmanager
        def _transaction(self, user, *, readonly=False):
            assert user == "synthetic-user" and readonly is True
            yield Connection()

    result = TaskRepository(Repo()).list_recent("synthetic-user")
    item = result["items"][0]
    assert item["instruction"] == "" and item["display_title"] == "只读查询：账单分析"
    assert "request" not in item and source == row["request"]
    assert all("owner_id=%s" in query and params[0] == "synthetic-user" for query, params in calls)
