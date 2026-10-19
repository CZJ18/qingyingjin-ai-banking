"""Server-owned monthly plan bounds; acceptance grants no financial authority."""

from datetime import UTC, datetime
from zoneinfo import ZoneInfo

REVIEW_NODE = "monthly_plan_review"
SOURCE_MODES = frozenset({"read_only", "advice"})


def current_period():
    return datetime.now(ZoneInfo("Asia/Shanghai")).strftime("%Y-%m")


def validate_monthly_specs(specs):
    """All evidence and advice must be obtainable before any user/intent step."""
    sources = {task["key"] for task in specs if task["execution_mode"] in SOURCE_MODES}
    if not sources or any(
        task["blocked"] or (task["key"] in sources and not set(task["dependencies"]) <= sources)
        for task in specs
    ):
        raise ValueError("monthly suggestions require unblocked read/advice dependencies")


def validate_suggestions(specs, outputs, task_id):
    suggestions = {}
    for task in specs:
        if task["execution_mode"] not in SOURCE_MODES:
            continue
        output = outputs.get(task["key"])
        if (
            not isinstance(output, dict)
            or output.get("task_id") != task_id + "." + task["key"]
            or output.get("agent_type") != task["agent_type"]
            or output.get("skill_id") != task["skill_id"]
            or type(output.get("skill_version")) is not int
            or output.get("skill_version") != task["skill_version"]
            or output.get("status") != "succeeded"
            or not isinstance(output.get("result"), (dict, list))
            or output.get("errors")
            or output.get("financial_execution_enabled") is not False
        ):
            raise ValueError("monthly plan requires verified successful suggestions")
        suggestions[task["key"]] = output
    return suggestions


def has_usable_plan(specs, suggestions, goal):
    """Readiness check over native plans already validated by the Banking bridge."""
    found = False
    for task in specs:
        if task["agent_type"] == "savings" and task["tool"] in {
            "get_financial_plan",
            "compare_financial_plans",
            "replan_financial_plan",
        }:
            payload = suggestions[task["key"]]["result"]
            result = payload.get("result") if isinstance(payload, dict) else None
            if (
                not isinstance(result, dict)
                or result.get("currency") != goal["currency"]
                or result.get("period") != goal["period"]
                or result.get("blockers") != []
                or result.get("financial_execution_enabled") is not False
            ):
                return False
            options = result.get("options")
            if not usable_options(options) or not any(
                option.get("feasible") is True for option in options
            ):
                return False
            found = True
        elif task["agent_type"] == "lifestyle" and task["tool"] in {
            "get_lifestyle_plan",
            "compile_lifestyle_plan",
        }:
            payload = suggestions[task["key"]]["result"]
            plan = payload.get("plan") if isinstance(payload, dict) else None
            result = plan.get("result") if isinstance(plan, dict) else None
            if (
                not isinstance(result, dict)
                or type(plan.get("expires_at")) is not int
                # Native lifestyle Plan.ExpiresAt is a Unix millisecond timestamp.
                or plan["expires_at"] <= int(datetime.now(UTC).timestamp() * 1000)
                or result.get("status") != "ready"
                or result.get("missing_information") != []
                or result.get("conflicts") != []
                or result.get("financial_execution_enabled") is not False
                or not usable_options(result.get("options"))
            ):
                return False
            found = True
    return found


def usable_options(options):
    return (
        isinstance(options, list)
        and bool(options)
        and all(
            isinstance(option, dict)
            and isinstance(option.get("name"), str)
            and bool(option["name"].strip())
            for option in options
        )
    )
