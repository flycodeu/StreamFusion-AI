"""Versioned inspection-only contracts, independent of generic algorithm TaskSpec."""

import hashlib
import json
from datetime import UTC, datetime
from typing import Annotated, Literal, Self

from pydantic import (
    AwareDatetime,
    BaseModel,
    ConfigDict,
    Field,
    FiniteFloat,
    field_validator,
    model_validator,
)
from pydantic.alias_generators import to_camel

Identifier = Annotated[str, Field(pattern=r"^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$")]
BudgetKey = Annotated[str, Field(pattern=r"^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$")]
PositiveInt = Annotated[int, Field(strict=True, ge=1, le=2**31 - 1)]
Digest = Annotated[str, Field(pattern=r"^[a-f0-9]{64}$")]
DetectorKey = Literal[
    "STREAM_STABILITY", "BLACK_SCREEN", "BLUE_SCREEN", "FREEZE", "BLUR", "OCCLUSION"
]
ObservedState = Literal["DISARMED", "PREPARING", "READY", "WAITING_RESOURCE", "STOPPING", "STOPPED"]


class Contract(BaseModel):
    model_config = ConfigDict(
        alias_generator=to_camel,
        populate_by_name=True,
        extra="forbid",
        hide_input_in_errors=True,
        frozen=True,
    )

    def canonical_json(self) -> str:
        return json.dumps(
            self.model_dump(mode="json", by_alias=True),
            ensure_ascii=False,
            sort_keys=True,
            separators=(",", ":"),
            allow_nan=False,
        )

    def digest(self) -> str:
        return hashlib.sha256(self.canonical_json().encode("utf-8")).hexdigest()


class ControlCommand(Contract):
    contract_version: Literal["1.0"]
    request_id: Identifier
    control_id: Identifier
    control_seq: PositiveInt
    expected_boot_id: Identifier
    action: Literal["START", "STOP"]
    trace_id: Identifier


class ControlSnapshot(Contract):
    control_id: Identifier
    control_seq: PositiveInt
    plan_id: Identifier
    node_id: Identifier
    target_boot_id: Identifier
    desired_state: Literal["RUNNING", "STOPPED"]
    expires_at: AwareDatetime
    accepted_seq: int | None = None
    observed_state: ObservedState = "DISARMED"
    reason_code: str | None = None


class Admission(Contract):
    ready: bool
    available_slots: Annotated[int, Field(strict=True, ge=0, le=4096)]
    reason_code: Identifier | None = None

    @model_validator(mode="after")
    def consistent(self) -> Self:
        if self.ready and self.available_slots == 0:
            raise ValueError("ready admission requires an available slot")
        if not self.ready and self.reason_code is None:
            raise ValueError("unavailable admission requires a reason code")
        return self


class CheckSpec(Contract):
    detector_key: DetectorKey
    item_generation: PositiveInt
    config_digest: Digest
    implementation_version: Identifier
    parameter_version: Identifier = "1.0"
    model_ref: Identifier | None = None


class JobSpec(Contract):
    """One pre-grouped session; the future platform publisher owns grouping/due scheduling."""

    contract_version: Literal["1.0"] = "1.0"
    job_id: Identifier
    plan_id: Identifier
    plan_revision: PositiveInt
    cycle_no: PositiveInt
    target_id: Identifier
    profile_id: Identifier
    due_at: AwareDatetime
    latest_start_at: AwareDatetime
    deadline_at: AwareDatetime
    checks: Annotated[tuple[CheckSpec, ...], Field(min_length=1, max_length=6)]
    budget_keys: Annotated[tuple[BudgetKey, ...], Field(min_length=1, max_length=8)]

    @field_validator("due_at", "latest_start_at", "deadline_at")
    @classmethod
    def utc_time(cls, value: datetime) -> datetime:
        return value.astimezone(UTC)

    @model_validator(mode="after")
    def consistent(self) -> Self:
        if not self.due_at <= self.latest_start_at < self.deadline_at:
            raise ValueError("due/latest start/deadline must be ordered")
        keys = [check.detector_key for check in self.checks]
        if len(set(keys)) != len(keys) or len(set(self.budget_keys)) != len(self.budget_keys):
            raise ValueError("duplicate check or budget")
        return self


class RunLease(Contract):
    run_id: Identifier
    assignment_epoch: PositiveInt
    claim_request_id: Identifier
    control_id: Identifier
    control_seq: PositiveInt
    node_id: Identifier
    boot_id: Identifier
    lease_expires_at: AwareDatetime
    job: JobSpec
    trace_id: Identifier | None = None


class CheckResult(Contract):
    detector_key: DetectorKey
    item_generation: PositiveInt
    config_digest: Digest
    implementation_version: Identifier
    outcome: Literal["NORMAL", "ANOMALY", "SUSPECTED", "UNKNOWN", "NOT_APPLICABLE", "SKIPPED"]
    reason_code: Identifier
    observed_start: AwareDatetime
    observed_end: AwareDatetime
    sample_count: Annotated[int, Field(strict=True, ge=0, le=2**31 - 1)]
    metrics: dict[Identifier, Annotated[FiniteFloat, Field(strict=True)] | None] = Field(
        default_factory=dict, max_length=32
    )

    @field_validator("observed_start", "observed_end")
    @classmethod
    def utc_time(cls, value: datetime) -> datetime:
        return value.astimezone(UTC)

    @model_validator(mode="after")
    def consistent(self) -> Self:
        if self.observed_end < self.observed_start:
            raise ValueError("observation window is reversed")
        if self.outcome in {"NORMAL", "ANOMALY", "SUSPECTED"} and self.sample_count == 0:
            raise ValueError("a judgement needs observed samples")
        return self
