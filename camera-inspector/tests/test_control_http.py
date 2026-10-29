"""HTTP boundary tests; database atomicity is separately exercised against real MySQL."""

import asyncio
import logging
from datetime import UTC, datetime, timedelta
from pathlib import Path
from threading import Event, Lock
from time import monotonic

import httpx
import pytest
from fastapi.testclient import TestClient
from pydantic import ValidationError

from camera_inspector import settings as settings_module
from camera_inspector.app import create_app
from camera_inspector.control import ControlService
from camera_inspector.errors import InspectionError
from camera_inspector.settings import Settings
from camera_inspector.types import Admission, ControlCommand, ControlSnapshot

TOKEN = "inspection-unit-test-token-never-used-outside-tests"
BOOT = "boot-1"
HEADERS = {"Authorization": f"Bearer {TOKEN}"}
CONTROL = "/inspection/v1/control"
STATUS = "/inspection/v1/status"


def settings(tmp_path: Path, **overrides: object) -> Settings:
    return Settings(
        _env_file=None,
        node_id="node-1",
        auth_token=TOKEN,
        db_user="not-connected",
        db_password="never-used",
        db_database="inspection_test",
        work_dir=tmp_path,
        **overrides,
    )


def command(seq: int = 1, action: str = "START", boot_id: str = BOOT) -> dict[str, object]:
    return {
        "contractVersion": "1.0",
        "requestId": f"request-{seq}",
        "controlId": "control-1",
        "controlSeq": seq,
        "expectedBootId": boot_id,
        "action": action,
        "traceId": "a" * 32,
    }


class MemoryControl:
    """Test fixture for HTTP callbacks, not an alternative runtime repository."""

    def __init__(self) -> None:
        self.lock = Lock()
        self.calls = 0
        self.admissions = 0
        self.last_trace_id: str | None = None
        self.snapshot = ControlSnapshot(
            control_id="control-1",
            control_seq=1,
            plan_id="plan-1",
            node_id="node-1",
            target_boot_id=BOOT,
            desired_state="RUNNING",
            expires_at=datetime.now(UTC) + timedelta(minutes=1),
        )

    def publish_stop(self) -> None:
        with self.lock:
            self.snapshot = self.snapshot.model_copy(
                update={"control_seq": 2, "desired_state": "STOPPED", "accepted_seq": None}
            )

    def accept_control(self, value: ControlCommand) -> ControlSnapshot:
        with self.lock:
            self.calls += 1
            self.last_trace_id = value.trace_id
            if value.control_seq != self.snapshot.control_seq:
                raise InspectionError("STALE_CONTROL")
            expected = "START" if self.snapshot.desired_state == "RUNNING" else "STOP"
            if value.action != expected:
                raise InspectionError("CONTROL_CONFLICT")
            if self.snapshot.accepted_seq != value.control_seq:
                self.snapshot = self.snapshot.model_copy(
                    update={
                        "accepted_seq": value.control_seq,
                        "observed_state": "PREPARING" if value.action == "START" else "STOPPED",
                        "reason_code": None,
                    }
                )
            return self.snapshot

    def set_admission(self, value: ControlCommand, admission: Admission) -> ControlSnapshot:
        with self.lock:
            if value.control_seq != self.snapshot.control_seq:
                raise InspectionError("STALE_CONTROL")
            self.admissions += 1
            self.snapshot = self.snapshot.model_copy(
                update={
                    "observed_state": "READY" if admission.ready else "WAITING_RESOURCE",
                    "reason_code": admission.reason_code,
                }
            )
            return self.snapshot


def test_authentication_precedes_json_and_status_has_no_fake_detectors(tmp_path: Path) -> None:
    repo = MemoryControl()
    with TestClient(create_app(settings(tmp_path), repo, boot_id=BOOT)) as client:
        rejected = client.post(CONTROL, content="{invalid-secret-input")
        assert rejected.status_code == 401
        assert repo.calls == 0
        response = client.get(STATUS, headers=HEADERS)
        assert response.status_code == 200
        assert response.json()["data"] == {
            "contractVersion": "1.0",
            "serviceVersion": "0.1.0",
            "nodeId": "node-1",
            "bootId": BOOT,
            "mode": "P0_CONTRACT",
            "capabilityStatus": "NOT_VERIFIED",
            "detectors": [],
            "availableSlots": 0,
        }
        assert client.get("/openapi.json", headers=HEADERS).status_code == 404


@pytest.mark.parametrize(
    "payload",
    [
        {**command(), "contractVersion": "9.0"},
        {**command(), "password": "not-for-error-output"},
        {key: value for key, value in command().items() if key != "contractVersion"},
        {**command(), "controlSeq": "1"},
    ],
)
def test_invalid_contract_does_not_touch_db_or_echo_input(
    tmp_path: Path, payload: dict[str, object]
) -> None:
    repo = MemoryControl()
    with TestClient(create_app(settings(tmp_path), repo, boot_id=BOOT)) as client:
        response = client.post(CONTROL, headers=HEADERS, json=payload)
        assert response.status_code == 422
        assert response.json()["code"] == "VALIDATION_ERROR"
        assert "not-for-error-output" not in response.text
        assert repo.calls == 0


def test_default_admission_never_claims_video_readiness_and_start_replay_is_idempotent(
    tmp_path: Path,
) -> None:
    repo = MemoryControl()
    with TestClient(create_app(settings(tmp_path), repo, boot_id=BOOT)) as client:
        first = client.post(CONTROL, headers=HEADERS, json=command())
        assert first.status_code == 202
        assert first.json()["data"]["observedState"] == "WAITING_RESOURCE"
        assert first.json()["data"]["reasonCode"] == "EXECUTOR_NOT_IMPLEMENTED"
        repeated = client.post(CONTROL, headers=HEADERS, json=command())
        assert repeated.json()["data"] == first.json()["data"]
        assert repo.admissions == 2  # Reassesses unavailable resources; no jobs are created.
        repo.publish_stop()
        assert client.post(CONTROL, headers=HEADERS, json=command(2, "STOP")).status_code == 202
        late = client.post(CONTROL, headers=HEADERS, json=command())
        assert late.status_code == 409
        assert late.json()["code"] == "STALE_CONTROL"


def test_restart_requires_command_for_new_boot(tmp_path: Path) -> None:
    repo = MemoryControl()
    with TestClient(create_app(settings(tmp_path), repo, boot_id="new-boot")) as client:
        response = client.post(CONTROL, headers=HEADERS, json=command())
        assert response.status_code == 409
        assert response.json()["code"] == "BOOT_MISMATCH"
        assert repo.calls == 0


def test_body_limit_is_enforced_before_repository(tmp_path: Path) -> None:
    repo = MemoryControl()
    with TestClient(create_app(settings(tmp_path, request_max_bytes=512), repo)) as client:
        response = client.post(CONTROL, headers=HEADERS, content=b"x" * 513)
        assert response.status_code == 413
        assert repo.calls == 0


def test_slow_stream_is_bounded_without_declared_length(tmp_path: Path) -> None:
    async def exercise() -> None:
        repo = MemoryControl()
        app = create_app(settings(tmp_path, request_timeout_seconds=0.1), repo)

        async def slow_body():
            yield b"{"
            await asyncio.sleep(1)
            yield b"}"

        async with app.router.lifespan_context(app):
            async with httpx.AsyncClient(transport=httpx.ASGITransport(app=app)) as client:
                response = await client.post(
                    "http://test" + CONTROL, headers=HEADERS, content=slow_body()
                )
                assert response.status_code == 408
                assert repo.calls == 0

    asyncio.run(exercise())


def test_chunked_body_cannot_bypass_byte_limit(tmp_path: Path) -> None:
    async def exercise() -> None:
        repo = MemoryControl()
        app = create_app(settings(tmp_path, request_max_bytes=512), repo)

        async def oversized_body():
            yield b"x" * 300
            yield b"y" * 300

        async with app.router.lifespan_context(app):
            async with httpx.AsyncClient(transport=httpx.ASGITransport(app=app)) as client:
                response = await client.post(
                    "http://test" + CONTROL, headers=HEADERS, content=oversized_body()
                )
                assert response.status_code == 413
                assert repo.calls == 0

    asyncio.run(exercise())


def test_stop_does_not_wait_for_slow_assessment_and_late_ready_cannot_overwrite(
    tmp_path: Path,
) -> None:
    started, release = Event(), Event()

    def assess(_snapshot: ControlSnapshot) -> Admission:
        started.set()
        assert release.wait(3)
        return Admission(ready=True, available_slots=1)

    async def exercise() -> None:
        repo = MemoryControl()
        app = create_app(settings(tmp_path), repo, assess, boot_id=BOOT)
        async with app.router.lifespan_context(app):
            async with httpx.AsyncClient(transport=httpx.ASGITransport(app=app)) as client:
                pending = asyncio.create_task(
                    client.post("http://test" + CONTROL, headers=HEADERS, json=command())
                )
                try:
                    assert await asyncio.to_thread(started.wait, 2)
                    repo.publish_stop()
                    stopped = await asyncio.wait_for(
                        client.post(
                            "http://test" + CONTROL, headers=HEADERS, json=command(2, "STOP")
                        ),
                        timeout=1,
                    )
                    assert stopped.status_code == 202
                    assert stopped.json()["data"]["observedState"] == "STOPPED"
                finally:
                    release.set()
                late = await pending
                assert late.status_code == 409
                assert repo.snapshot.observed_state == "STOPPED"

    asyncio.run(exercise())


def test_timed_out_db_call_keeps_its_slot_and_stop_has_an_independent_lane(tmp_path: Path) -> None:
    started, release = Event(), Event()

    class SlowControl(MemoryControl):
        def accept_control(self, value: ControlCommand) -> ControlSnapshot:
            if value.action == "START":
                started.set()
                assert release.wait(3)
            return super().accept_control(value)

    repo = SlowControl()
    with TestClient(
        create_app(settings(tmp_path, io_timeout_seconds=0.05), repo, boot_id=BOOT)
    ) as c:
        try:
            first = c.post(CONTROL, headers=HEADERS, json=command())
            assert started.is_set()
            assert first.status_code == 503
            assert first.json()["code"] == "CONTROL_TIMEOUT"
            second = c.post(CONTROL, headers=HEADERS, json=command())
            assert second.status_code == 503
            assert second.json()["code"] == "CONTROL_BUSY"
            repo.publish_stop()
            stopped = c.post(CONTROL, headers=HEADERS, json=command(2, "STOP"))
            assert stopped.status_code == 202
            assert repo.calls == 1  # Only STOP reached the fixture's atomic operation.
        finally:
            release.set()


def test_driver_failure_is_safe_and_trace_is_preserved(
    tmp_path: Path, caplog: pytest.LogCaptureFixture
) -> None:
    class BrokenControl(MemoryControl):
        def accept_control(self, _value: ControlCommand) -> ControlSnapshot:
            raise RuntimeError("password=driver-secret rtsp://user:camera-secret@camera")

    headers = {**HEADERS, "X-Trace-Id": "b" * 32}
    with caplog.at_level(logging.INFO, logger="camera_inspector.control"):
        with TestClient(create_app(settings(tmp_path), BrokenControl(), boot_id=BOOT)) as client:
            response = client.post(CONTROL, headers=headers, json=command())
    assert response.status_code == 503
    assert response.json()["code"] == "DATABASE_UNAVAILABLE"
    assert response.json()["traceId"] == response.headers["X-Trace-Id"] == "a" * 32
    for secret in ("driver-secret", "camera-secret", TOKEN):
        assert secret not in response.text
        assert secret not in caplog.text


def test_settings_are_independent_validated_and_do_not_create_work_dir(tmp_path: Path) -> None:
    target = tmp_path / "not-created"
    configured = settings(target)
    assert configured.host == "0.0.0.0"
    assert configured.port == 8120
    assert configured.work_dir == target
    assert not target.exists()
    with pytest.raises(ValidationError):
        settings(tmp_path, tls_keyfile=tmp_path / "only-key")
    with pytest.raises(ValidationError):
        settings(tmp_path, request_max_bytes=0)


def test_settings_use_launch_directory_independently_of_install_layout(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    launch = tmp_path / "launch"
    launch.mkdir()
    monkeypatch.chdir(launch)
    monkeypatch.setattr(
        settings_module, "__file__", str(tmp_path / "venv/site-packages/settings.py")
    )
    (launch / ".env.local").write_text(
        "SF_INSPECTOR_NODE_ID=installed-node\n"
        f"SF_INSPECTOR_AUTH_TOKEN={TOKEN}\n"
        "SF_INSPECTOR_DB_USER=test-user\n"
        "SF_INSPECTOR_DB_PASSWORD=never-used\n"
        "SF_INSPECTOR_DB_DATABASE=inspection_test\n"
        "SF_INSPECTOR_DB_SSL_CA=certs/ca.pem\n",
        encoding="utf-8",
    )
    configured = Settings()
    assert configured.node_id == "installed-node"
    assert configured.work_dir == launch / ".run"
    assert configured.db_ssl_ca == launch / "certs/ca.pem"
    assert not configured.work_dir.exists()


def test_valid_command_trace_is_authoritative_in_response_header_db_and_log(
    tmp_path: Path, caplog: pytest.LogCaptureFixture
) -> None:
    repo = MemoryControl()
    with caplog.at_level(logging.INFO, logger="camera_inspector.control"):
        with TestClient(create_app(settings(tmp_path), repo, boot_id=BOOT)) as client:
            headers = {**HEADERS, "X-Trace-Id": "transport-trace"}
            status = client.get(STATUS, headers=headers)
            assert status.json()["traceId"] == "transport-trace"
            response = client.post(
                CONTROL, headers=headers, json={**command(), "traceId": "command:trace-17"}
            )
    assert response.status_code == 202
    assert response.json()["traceId"] == response.headers["X-Trace-Id"] == "command:trace-17"
    assert repo.last_trace_id == "command:trace-17"
    assert "traceId=command:trace-17" in caplog.text


@pytest.mark.parametrize("failure", ["resource", "exception"])
def test_unavailable_start_retries_assessment_but_ready_replay_does_not_reset_slots(
    tmp_path: Path, failure: str
) -> None:
    attempts = 0

    def assess(_snapshot: ControlSnapshot) -> Admission:
        nonlocal attempts
        attempts += 1
        if attempts == 1:
            if failure == "exception":
                raise RuntimeError("secret must not escape")
            return Admission(ready=False, available_slots=0, reason_code="RESOURCE_BUSY")
        return Admission(ready=True, available_slots=1)

    repo = MemoryControl()
    with TestClient(create_app(settings(tmp_path), repo, assess, boot_id=BOOT)) as client:
        first = client.post(CONTROL, headers=HEADERS, json=command())
        assert first.json()["data"]["desiredState"] == "RUNNING"
        assert first.json()["data"]["observedState"] == "WAITING_RESOURCE"
        assert "secret" not in first.text
        recovered = client.post(CONTROL, headers=HEADERS, json=command())
        assert recovered.json()["data"]["observedState"] == "READY"
        repeated = client.post(CONTROL, headers=HEADERS, json=command())
        assert repeated.json()["data"] == recovered.json()["data"]
        assert attempts == repo.admissions == 2


@pytest.mark.parametrize("failed_stage", ["accept", "admission"])
def test_start_can_retry_transient_database_failure(tmp_path: Path, failed_stage: str) -> None:
    class OnceBrokenControl(MemoryControl):
        failed = False

        def accept_control(self, value: ControlCommand) -> ControlSnapshot:
            if failed_stage == "accept" and not self.failed:
                self.failed = True
                raise RuntimeError("unavailable")
            return super().accept_control(value)

        def set_admission(self, value: ControlCommand, admission: Admission) -> ControlSnapshot:
            if failed_stage == "admission" and not self.failed:
                self.failed = True
                raise RuntimeError("unavailable")
            return super().set_admission(value, admission)

    repo = OnceBrokenControl()
    with TestClient(
        create_app(
            settings(tmp_path),
            repo,
            lambda _: Admission(ready=True, available_slots=1),
            boot_id=BOOT,
        )
    ) as client:
        first = client.post(CONTROL, headers=HEADERS, json=command())
        assert first.status_code == 503
        assert first.json()["code"] == "DATABASE_UNAVAILABLE"
        recovered = client.post(CONTROL, headers=HEADERS, json=command())
        assert recovered.status_code == 202
        assert recovered.json()["data"]["observedState"] == "READY"
        assert repo.admissions == 1


def test_cancelled_http_start_completes_and_replays_share_one_workflow(tmp_path: Path) -> None:
    started, release = Event(), Event()
    assessments = 0

    def assess(_snapshot: ControlSnapshot) -> Admission:
        nonlocal assessments
        assessments += 1
        started.set()
        assert release.wait(3)
        return Admission(ready=True, available_slots=1)

    async def exercise() -> None:
        repo = MemoryControl()
        app = create_app(settings(tmp_path), repo, assess, boot_id=BOOT)
        async with app.router.lifespan_context(app):
            async with httpx.AsyncClient(transport=httpx.ASGITransport(app=app)) as client:
                first = asyncio.create_task(
                    client.post("http://test" + CONTROL, headers=HEADERS, json=command())
                )
                try:
                    assert await asyncio.to_thread(started.wait, 2)
                    first.cancel()
                    with pytest.raises(asyncio.CancelledError):
                        await first
                    assert repo.snapshot.observed_state == "PREPARING"
                    other = await client.post(
                        "http://test" + CONTROL,
                        headers=HEADERS,
                        json={**command(), "controlId": "other-control"},
                    )
                    assert other.status_code == 503
                    assert other.json()["code"] == "CONTROL_BUSY"
                    duplicate = asyncio.create_task(
                        client.post(
                            "http://test" + CONTROL,
                            headers=HEADERS,
                            json={**command(), "requestId": "retry", "traceId": "retry-trace"},
                        )
                    )
                    # Allow the duplicate to reach the service while the assessment is blocked.
                    await asyncio.sleep(0.02)
                    assert not duplicate.done()
                    assert repo.calls == assessments == 1
                finally:
                    release.set()
                response = await asyncio.wait_for(duplicate, 1)
                assert response.status_code == 202
                assert response.json()["data"]["observedState"] == "READY"
                assert response.json()["traceId"] == "retry-trace"
                assert repo.calls == repo.admissions == assessments == 1

    asyncio.run(exercise())


def test_assessment_timeout_is_recoverable_after_original_worker_exits(tmp_path: Path) -> None:
    release, finished = Event(), Event()
    attempts = 0

    def assess(_snapshot: ControlSnapshot) -> Admission:
        nonlocal attempts
        attempts += 1
        if attempts == 1:
            assert release.wait(3)
            finished.set()
        return Admission(ready=True, available_slots=1)

    repo = MemoryControl()
    with TestClient(
        create_app(settings(tmp_path, admission_timeout_seconds=0.05), repo, assess, boot_id=BOOT)
    ) as client:
        try:
            first = client.post(CONTROL, headers=HEADERS, json=command())
            assert first.json()["data"]["reasonCode"] == "CONTROL_TIMEOUT"
            busy = client.post(CONTROL, headers=HEADERS, json=command())
            assert busy.json()["data"]["reasonCode"] == "CONTROL_BUSY"
            assert attempts == 1
        finally:
            release.set()
        assert finished.wait(1)
        recovered = client.post(CONTROL, headers=HEADERS, json=command())
        assert recovered.json()["data"]["observedState"] == "READY"
        assert attempts == 2


@pytest.mark.parametrize("length", ["9" * 5000, "-1", "bad", "65537"])
def test_invalid_content_length_is_bounded_and_never_parsed_as_json(
    tmp_path: Path, length: str
) -> None:
    repo = MemoryControl()
    with TestClient(create_app(settings(tmp_path), repo, boot_id=BOOT)) as client:
        response = client.post(
            CONTROL, headers={**HEADERS, "Content-Length": length}, content=b"{}"
        )
        assert response.status_code == 413
        assert response.json()["code"] == "REQUEST_TOO_LARGE"
        assert repo.calls == 0


def test_duplicate_authorization_is_rejected_before_body_is_read(tmp_path: Path) -> None:
    async def exercise() -> None:
        repo = MemoryControl()
        app = create_app(settings(tmp_path), repo, boot_id=BOOT)
        headers = [("Authorization", f"Bearer {TOKEN}"), ("Authorization", "Bearer invalid")]

        async def unread_body():
            raise AssertionError("unauthenticated body must not be read")
            yield b""  # Makes this an async iterator without producing a body.

        async with app.router.lifespan_context(app):
            async with httpx.AsyncClient(transport=httpx.ASGITransport(app=app)) as client:
                response = await client.post(
                    "http://test" + CONTROL, headers=headers, content=unread_body()
                )
                assert response.status_code == 401
                assert repo.calls == 0

    asyncio.run(exercise())


def test_shutdown_does_not_wait_for_slow_assessment_and_rejects_new_work() -> None:
    started, release = Event(), Event()

    def assess(_snapshot: ControlSnapshot) -> Admission:
        started.set()
        assert release.wait(3)
        return Admission(ready=True, available_slots=1)

    async def exercise() -> None:
        repo = MemoryControl()
        service = ControlService(repo, "node-1", BOOT, assessor=assess)
        value = ControlCommand.model_validate(command())
        pending = asyncio.create_task(service.apply(value))
        try:
            assert await asyncio.to_thread(started.wait, 2)
            before = monotonic()
            service.close()
            assert monotonic() - before < 0.5
            with pytest.raises(asyncio.CancelledError):
                await asyncio.wait_for(pending, 1)
            with pytest.raises(InspectionError, match="NODE_STOPPING"):
                await service.apply(value)
            assert repo.admissions == 0
        finally:
            release.set()
            service.close()

    asyncio.run(exercise())
