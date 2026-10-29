"""Protocol boundary checks; these do not claim real detector capability."""

import json
from datetime import UTC, datetime, timedelta

import pytest
from pydantic import ValidationError

from camera_inspector.types import Admission, CheckResult, ControlCommand, JobSpec


def command_data() -> dict[str, object]:
    return {
        "contractVersion": "1.0",
        "requestId": "request-1",
        "controlId": "control-1",
        "controlSeq": 1,
        "expectedBootId": "boot-1",
        "action": "START",
        "traceId": "trace-1",
    }


def job_data() -> dict[str, object]:
    now = datetime.now(UTC)
    return {
        "jobId": "job-1",
        "planId": "plan-1",
        "planRevision": 1,
        "cycleNo": 1,
        "targetId": "channel-1",
        "profileId": "sub-1",
        "dueAt": now,
        "latestStartAt": now + timedelta(seconds=30),
        "deadlineAt": now + timedelta(seconds=90),
        "checks": [
            {
                "detectorKey": "BLACK_SCREEN",
                "itemGeneration": 1,
                "configDigest": "a" * 64,
                "implementationVersion": "test-fixture-v1",
            }
        ],
        "budgetKeys": ["source:1"],
    }


def result_data() -> dict[str, object]:
    now = datetime.now(UTC)
    return {
        "detectorKey": "BLACK_SCREEN",
        "itemGeneration": 1,
        "configDigest": "a" * 64,
        "implementationVersion": "test-fixture-v1",
        "outcome": "UNKNOWN",
        "reasonCode": "CONTRACT_TEST_ONLY",
        "observedStart": now,
        "observedEnd": now,
        "sampleCount": 0,
        "metrics": {"frameCount": 0.0, "packetLoss": None},
    }


@pytest.mark.parametrize("version", [None, "0.9", "2.0", 1])
def test_control_requires_exact_contract_version(version: object) -> None:
    data = command_data()
    if version is None:
        del data["contractVersion"]
    else:
        data["contractVersion"] = version
    with pytest.raises(ValidationError):
        ControlCommand.model_validate(data)


@pytest.mark.parametrize("sequence", [0, -1, True, "1", 1.5])
def test_control_sequence_is_a_positive_integer(sequence: object) -> None:
    with pytest.raises(ValidationError):
        ControlCommand.model_validate({**command_data(), "controlSeq": sequence})


def test_control_does_not_accept_injected_tasks_or_urls() -> None:
    for field in ("tasks", "callbackUrl", "streamUrl", "sql"):
        with pytest.raises(ValidationError):
            ControlCommand.model_validate({**command_data(), field: "not-permitted"})


def test_validation_summary_hides_secret_input() -> None:
    with pytest.raises(ValidationError) as error:
        ControlCommand.model_validate({**command_data(), "traceId": "secret/password?"})
    assert "secret/password?" not in str(error.value)


def test_budget_scope_has_room_for_prefix_and_full_identifier() -> None:
    scope = "source:" + "a" * 64
    assert JobSpec.model_validate({**job_data(), "budgetKeys": [scope]}).budget_keys == (scope,)
    with pytest.raises(ValidationError):
        JobSpec.model_validate({**job_data(), "budgetKeys": ["a" * 129]})


def test_contract_serialization_has_stable_aliases_and_digest() -> None:
    command = ControlCommand.model_validate(command_data())
    restored = ControlCommand.model_validate_json(command.canonical_json())
    assert restored == command
    assert restored.digest() == command.digest()
    assert set(json.loads(command.canonical_json())) == set(command_data())


def test_rules_do_not_require_a_model_or_generic_algorithm_fields() -> None:
    job = JobSpec.model_validate(job_data())
    assert job.checks[0].model_ref is None
    with pytest.raises(ValidationError):
        JobSpec.model_validate({**job_data(), "scenarios": []})


def test_job_rejects_duplicate_checks_and_scopes() -> None:
    data = job_data()
    checks = data["checks"]
    assert isinstance(checks, list)
    with pytest.raises(ValidationError):
        JobSpec.model_validate({**data, "checks": checks * 2})
    with pytest.raises(ValidationError):
        JobSpec.model_validate({**data, "budgetKeys": ["source:1", "source:1"]})


def test_job_rejects_reversed_deadlines_and_naive_time() -> None:
    data = job_data()
    with pytest.raises(ValidationError):
        JobSpec.model_validate({**data, "latestStartAt": data["deadlineAt"]})
    with pytest.raises(ValidationError):
        JobSpec.model_validate({**data, "dueAt": datetime(2026, 1, 1)})


def test_datetime_offsets_normalize_before_hashing() -> None:
    data = job_data()
    first = JobSpec.model_validate(data)
    second = JobSpec.model_validate_json(first.canonical_json())
    assert first.digest() == second.digest()
    assert first.due_at.utcoffset() == timedelta(0)


@pytest.mark.parametrize("value", [float("nan"), float("inf"), "0", True])
def test_metrics_reject_nonfinite_and_coerced_measurements(value: object) -> None:
    with pytest.raises(ValidationError):
        CheckResult.model_validate({**result_data(), "metrics": {"score": value}})


def test_no_samples_cannot_prove_a_normal_or_abnormal_video() -> None:
    assert CheckResult.model_validate(result_data()).outcome == "UNKNOWN"
    for outcome in ("NORMAL", "ANOMALY", "SUSPECTED"):
        with pytest.raises(ValidationError):
            CheckResult.model_validate({**result_data(), "outcome": outcome})


def test_result_has_bounded_metrics_and_an_ordered_window() -> None:
    data = result_data()
    with pytest.raises(ValidationError):
        CheckResult.model_validate({**data, "metrics": {f"m{i}": 1.0 for i in range(33)}})
    with pytest.raises(ValidationError):
        CheckResult.model_validate({**data, "observedEnd": datetime(2000, 1, 1, tzinfo=UTC)})


def test_readiness_needs_capacity_and_unavailability_needs_a_reason() -> None:
    with pytest.raises(ValidationError):
        Admission(ready=True, available_slots=0)
    with pytest.raises(ValidationError):
        Admission(ready=False, available_slots=0)
    assert not Admission(
        ready=False, available_slots=0, reason_code="EXECUTOR_NOT_IMPLEMENTED"
    ).ready
