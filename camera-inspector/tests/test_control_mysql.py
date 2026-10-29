"""ASGI HTTP and real MySQL control chain; synthetic admission, no video detector."""

from collections.abc import Callable
from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import Any

import pytest
from fastapi.testclient import TestClient
from pymysql.connections import Connection

from camera_inspector.app import create_app
from camera_inspector.db import DatabaseConfig
from camera_inspector.errors import InspectionError
from camera_inspector.repository import Repository
from camera_inspector.settings import Settings
from camera_inspector.types import (
    Admission,
    CheckResult,
    CheckSpec,
    ControlCommand,
    ControlSnapshot,
    DetectorKey,
    JobSpec,
)

pytestmark = pytest.mark.mysql
Factory = Callable[[], Connection]
TOKEN = "inspection-integration-test-token-never-used-outside-tests"
BOOT = "http-db-test-boot"
NODE = "http-db-test-node"
CONTROL = "http-db-test-control"
DIGEST = "b" * 64


def execute(factory: Factory, sql: str, args: tuple[object, ...] = ()) -> list[dict[str, Any]]:
    with factory() as connection:
        with connection.cursor() as cursor:
            cursor.execute(sql, args)
            rows = list(cursor.fetchall()) if cursor.description else []
        connection.commit()
        return rows


def sql_time(value: datetime) -> datetime:
    return value.astimezone(UTC).replace(tzinfo=None)


def publish_platform_fixture(
    factory: Factory,
    check_keys: tuple[DetectorKey, ...] = ("STREAM_STABILITY", "BLACK_SCREEN"),
) -> None:
    """Minimal platform publication into the test schema, not a production publisher."""
    current = execute(factory, "SELECT UTC_TIMESTAMP(6) AS now")[0]["now"].replace(tzinfo=UTC)
    execute(
        factory,
        "INSERT INTO inspection_control "
        "(control_id,plan_id,node_id,target_boot_id,control_seq,desired_state,expires_at) "
        "VALUES (%s,'http-db-test-plan',%s,%s,1,'RUNNING',%s)",
        (CONTROL, NODE, BOOT, sql_time(current + timedelta(minutes=5))),
    )
    for key in ("global", f"node:{NODE}", "nvr:http-db-test"):
        execute(
            factory,
            "INSERT INTO inspection_budget (budget_key,capacity) VALUES (%s,2)",
            (key,),
        )
    for index in (1, 2):
        job = JobSpec(
            job_id=f"http-db-test-job-{index}",
            plan_id="http-db-test-plan",
            plan_revision=1,
            cycle_no=1,
            target_id=f"http-db-test-target-{index}",
            profile_id="substream",
            due_at=current - timedelta(seconds=1),
            latest_start_at=current + timedelta(minutes=2),
            deadline_at=current + timedelta(minutes=3),
            checks=tuple(
                CheckSpec(
                    detector_key=key,
                    item_generation=1,
                    config_digest=DIGEST,
                    implementation_version="contract-only-no-detector",
                )
                for key in check_keys
            ),
            budget_keys=("nvr:http-db-test",),
        )
        execute(
            factory,
            "INSERT INTO inspection_job "
            "(job_id,plan_id,due_at,latest_start_at,deadline_at,spec_json,spec_digest) "
            "VALUES (%s,%s,%s,%s,%s,%s,%s)",
            (
                job.job_id,
                job.plan_id,
                sql_time(job.due_at),
                sql_time(job.latest_start_at),
                sql_time(job.deadline_at),
                job.canonical_json(),
                job.digest(),
            ),
        )


def test_http_mysql_start_results_stop_and_release(
    mysql_worker_config: DatabaseConfig,
    mysql_connect: Factory,
    mysql_worker_connect: Factory,
    tmp_path: Path,
) -> None:
    publish_platform_fixture(mysql_connect)
    repo = Repository(mysql_worker_connect, NODE, BOOT)
    settings = Settings(
        _env_file=None,
        node_id=NODE,
        auth_token=TOKEN,
        db_host=mysql_worker_config.host,
        db_port=mysql_worker_config.port,
        db_user=mysql_worker_config.user,
        db_password=mysql_worker_config.password,
        db_database=mysql_worker_config.database,
        db_allow_insecure_loopback=True,
        work_dir=tmp_path,
    )
    admission_calls: list[ControlSnapshot] = []

    def synthetic_admission(command: ControlSnapshot) -> Admission:
        admission_calls.append(command)
        return Admission(ready=True, available_slots=2)

    command = ControlCommand(
        contract_version="1.0",
        request_id="http-db-start",
        control_id=CONTROL,
        control_seq=1,
        expected_boot_id=BOOT,
        action="START",
        trace_id="a" * 32,
    )
    headers = {"Authorization": f"Bearer {TOKEN}"}
    endpoint = "/inspection/v1/control"
    with pytest.raises(InspectionError, match="BOOT_MISMATCH"):
        repo.claim(CONTROL, 1, "claim-before-http")

    with TestClient(create_app(settings, repo, synthetic_admission, boot_id=BOOT)) as client:
        for _ in range(2):
            response = client.post(
                endpoint, json=command.model_dump(by_alias=True), headers=headers
            )
            assert response.status_code == 202
            assert response.json()["data"]["observedState"] == "READY"
            assert response.headers["X-Trace-Id"] == command.trace_id
        assert len(admission_calls) == 1
        # Injecting a test assessment must not advertise actual detector capabilities.
        status = client.get("/inspection/v1/status", headers=headers).json()["data"]
        assert status["detectors"] == []
        assert status["capabilityStatus"] == "NOT_VERIFIED"

        leases = [repo.claim(CONTROL, 1, f"claim-after-http-{index}") for index in (1, 2)]
        assert all(lease is not None for lease in leases)
        assert repo.claim(CONTROL, 1, "claim-over-budget") is None
        results: list[CheckResult] = []
        for index, lease in enumerate(leases):
            assert lease is not None
            assert lease.trace_id == command.trace_id
            repo.start_run(lease.run_id, lease.assignment_epoch)
            repo.renew_run(lease.run_id, lease.assignment_epoch)
            started_at = execute(
                mysql_connect,
                "SELECT started_at FROM inspection_run WHERE run_id=%s",
                (lease.run_id,),
            )[0]["started_at"].replace(tzinfo=UTC)
            for check in lease.job.checks:
                result = CheckResult(
                    detector_key=check.detector_key,
                    item_generation=check.item_generation,
                    config_digest=check.config_digest,
                    implementation_version=check.implementation_version,
                    outcome="UNKNOWN",
                    reason_code="P0_CONTRACT_ONLY",
                    observed_start=started_at,
                    observed_end=started_at,
                    sample_count=0,
                )
                # Finish the first job; leave the second with one unsubmitted item for STOP.
                if index == 1:
                    results.append(result)
                if index == 0 or check.detector_key == "STREAM_STABILITY":
                    repo.submit_result(lease.run_id, lease.assignment_epoch, result)
        assert execute(mysql_connect, "SELECT COUNT(*) AS n FROM inspection_result")[0]["n"] == 3
        assert {
            row["used_slots"] for row in execute(mysql_connect, "SELECT * FROM inspection_budget")
        } == {2}

        execute(
            mysql_connect,
            "UPDATE inspection_control SET control_seq=2,desired_state='STOPPED' "
            "WHERE control_id=%s",
            (CONTROL,),
        )
        pending = leases[1]
        assert pending is not None
        # The persisted STOP fences work even before its HTTP notification arrives.
        with pytest.raises(InspectionError, match="STALE_CONTROL"):
            repo.renew_run(pending.run_id, pending.assignment_epoch)
        stop = command.model_copy(
            update={"request_id": "http-db-stop", "control_seq": 2, "action": "STOP"}
        )
        response = client.post(endpoint, json=stop.model_dump(by_alias=True), headers=headers)
        assert response.status_code == 202
        assert response.json()["data"]["observedState"] == "STOPPING"
        delayed_start = client.post(
            endpoint, json=command.model_dump(by_alias=True), headers=headers
        )
        assert delayed_start.status_code == 409
        assert delayed_start.json()["code"] == "STALE_CONTROL"
        with pytest.raises(InspectionError, match="STALE_CONTROL"):
            repo.submit_result(pending.run_id, pending.assignment_epoch, results[1])
        # A retry acknowledges only an already committed result, not new work after STOP.
        repo.submit_result(pending.run_id, pending.assignment_epoch, results[0])
        repo.release_run(pending.run_id, pending.assignment_epoch, BOOT, resources_released=False)
        assert {
            row["used_slots"] for row in execute(mysql_connect, "SELECT * FROM inspection_budget")
        } == {2}
        for index, lease in enumerate(leases):
            assert lease is not None
            for _ in range(2):
                repo.release_run(
                    lease.run_id, lease.assignment_epoch, BOOT, resources_released=True
                )
            assert {
                row["used_slots"]
                for row in execute(mysql_connect, "SELECT * FROM inspection_budget")
            } == {1 - index}
        assert repo.get_control(CONTROL).observed_state == "STOPPED"
        rows = execute(
            mysql_connect,
            "SELECT state,resources_released FROM inspection_run ORDER BY job_id",
        )
        assert rows == [
            {"state": "FINISHED", "resources_released": 1},
            {"state": "CANCELLED", "resources_released": 1},
        ]
