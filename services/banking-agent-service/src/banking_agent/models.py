from enum import StrEnum
from typing import Any
from zoneinfo import ZoneInfo, ZoneInfoNotFoundError

from pydantic import BaseModel, ConfigDict, Field, field_validator, model_validator


class StrictInput(BaseModel):
    model_config = ConfigDict(extra="forbid", strict=True)


class ResourceKind(StrEnum):
    account = "account"
    transaction = "transaction"
    subscription = "subscription"
    budget = "budget"
    lifestyle_goal = "lifestyle_goal"
    lifestyle_plan = "lifestyle_plan"
    operation = "operation"


class ResourceRef(StrictInput):
    # Literal string values allow strict JSON inputs without coercing numeric IDs.
    kind: str = Field(
        pattern="^(account|transaction|subscription|budget|lifestyle_goal|lifestyle_plan|operation)$"
    )
    source_id: str = Field(min_length=1, max_length=64, pattern="^[A-Za-z0-9][A-Za-z0-9-]*$")


class RefreshInput(StrictInput):
    resources: list[ResourceRef] = Field(min_length=1, max_length=20)

    @model_validator(mode="after")
    def unique_resources(self):
        if len({(r.kind, r.source_id) for r in self.resources}) != len(self.resources):
            raise ValueError("duplicate resource references")
        return self


class ProfileInput(StrictInput):
    expected_version: int = Field(ge=0, le=9_223_372_036_854_775_806)
    timezone: str = Field(min_length=1, max_length=64)
    safety_reserve_minor: int = Field(ge=0, le=1_000_000_000_000)

    @field_validator("timezone")
    @classmethod
    def valid_timezone(cls, value):
        try:
            ZoneInfo(value)
        except (ZoneInfoNotFoundError, ValueError) as e:
            raise ValueError("known IANA timezone required") from e
        return value


class Envelope(BaseModel):
    code: str
    message: str
    request_id: str
    trace_id: str
    data: Any = None


class ProfileView(BaseModel):
    user_id: str
    currency: str
    timezone: str
    safety_reserve_minor: int = Field(ge=0, le=1_000_000_000_000)
    revision: int = Field(ge=0)


class ProfileEnvelope(Envelope):
    data: ProfileView


class EvidenceRef(BaseModel):
    kind: str
    source_id: str


class ProjectionView(BaseModel):
    kind: str
    source_id: str
    source_version: int = Field(ge=1)
    source_digest: str = Field(pattern="^[0-9a-f]{64}$")
    observed_at: str
    audit_id: str | None
    payload: dict[str, Any]
    evidence: list[EvidenceRef]


class ProjectionEnvelope(Envelope):
    data: ProjectionView


class ProjectionListEnvelope(Envelope):
    data: list[ProjectionView]


class EvidenceView(BaseModel):
    source_id: str
    source_version: int = Field(ge=1)
    source_digest: str = Field(pattern="^[0-9a-f]{64}$")
    observed_at: str
    evidence: list[EvidenceRef]


class EvidenceEnvelope(Envelope):
    data: EvidenceView


class RefreshOutcome(BaseModel):
    kind: str
    source_id: str
    source_version: int = Field(ge=1, description="Retained current projection version")
    status: str
    # Pre-release receipts created during development replay without being mutated.
    incoming_source_version: int | None = None
    source_digest: str | None = None
    observed_at: str | None = None


class RefreshView(BaseModel):
    event_id: str
    applied: int = Field(ge=0)
    duplicates: int = Field(ge=0)
    stale: int = Field(ge=0)
    resources: list[RefreshOutcome]


class RefreshEnvelope(Envelope):
    data: RefreshView
