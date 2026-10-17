import pytest
from pydantic import ValidationError


def spec(**changes):
    from banking_agent.agent_models import AgentTaskSpec

    raw = {
        "key": "billing",
        "agent_type": "billing",
        "tool": "analyze_spending",
        "skill_id": "billing_analysis",
        "skill_version": 1,
        "execution_mode": "read_only",
        "parameters": {"from": "2026-09-01", "to": "2026-10-01"},
        "dependencies": [],
    }
    return AgentTaskSpec.model_validate(raw | changes)


def test_skill_role_tool_and_mode_are_bound():
    assert spec().agent_type == "billing"
    for change in (
        {"agent_type": "savings"},
        {"tool": "execute"},
        {"skill_version": 2},
        {"execution_mode": "intent"},
        {"key": "foreign:node"},
    ):
        with pytest.raises(ValidationError):
            spec(**change)


def test_plan_rejects_cycles_and_mixed_instruction_tasks():
    from banking_agent.agent_models import AgentSubmit

    a = spec(key="a", dependencies=["b"]).model_dump()
    b = spec(key="b", dependencies=["a"]).model_dump()
    with pytest.raises(ValidationError):
        AgentSubmit.model_validate({"tasks": [a, b]})
    with pytest.raises(ValidationError):
        AgentSubmit.model_validate({"tasks": [spec().model_dump()], "instruction": "plan"})


def test_financial_parameters_do_not_accept_float_or_self_authorization():
    for parameters in (
        {"amount_minor": 1.5},
        {"confirmed": True},
        {"user_id": "other"},
        {"nested": {"authorization_id": "forged"}},
    ):
        with pytest.raises(ValidationError):
            spec(parameters=parameters)


def test_valid_plan_does_not_accept_owner_engine_override():
    from banking_agent.agent_models import AgentSubmit

    assert len(AgentSubmit.model_validate({"tasks": [spec().model_dump()]}).tasks) == 1
    with pytest.raises(ValidationError):
        AgentSubmit.model_validate({"tasks": [spec().model_dump()], "owner_engine": "go"})


@pytest.mark.parametrize(
    "key",
    [
        "api_key",
        "password",
        "access_token",
        "refresh_token",
        "agent_token",
        "X-Banking-Agent-Key",
        "API-KEY",
        "secret",
    ],
)
def test_credentials_cannot_be_persisted_in_task_parameters_or_bindings(key):
    from banking_agent.agent_models import AgentSubmit

    with pytest.raises(ValidationError):
        spec(parameters={"nested": {key: "unrelated-private-credential"}})
    with pytest.raises(ValidationError):
        AgentSubmit.model_validate({"instruction": "analyse", "bindings": {key: "private"}})
