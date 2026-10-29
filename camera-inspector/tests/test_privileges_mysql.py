"""Least-privilege DB tests and an explanatory F01 acceptance oracle, not a consumer."""

import time
from datetime import UTC
from typing import Any

import pymysql
import pytest
from test_control_mysql import (
    BOOT,
    CONTROL,
    NODE,
    Factory,
    execute,
    publish_platform_fixture,
)

from camera_inspector.db import DatabaseConfig
from camera_inspector.errors import InspectionError
from camera_inspector.repository import Repository
from camera_inspector.types import Admission, CheckResult, ControlCommand, JobSpec, RunLease

pytestmark = pytest.mark.mysql


@pytest.mark.parametrize(
    ("table", "column", "expression"),
    [
        ("inspection_control", "control_id", "'other-control'"),
        ("inspection_control", "plan_id", "'other-plan'"),
        ("inspection_control", "node_id", "'other-node'"),
        ("inspection_control", "target_boot_id", "'other-boot'"),
        ("inspection_control", "control_seq", "2"),
        ("inspection_control", "desired_state", "'RUNNING'"),
        ("inspection_control", "expires_at", "UTC_TIMESTAMP(6) + INTERVAL 1 DAY"),
        ("inspection_budget", "capacity", "4096"),
        ("inspection_job", "plan_id", "'other-plan'"),
        ("inspection_job", "spec_json", "JSON_OBJECT()"),
        ("inspection_job", "spec_digest", "REPEAT('a',64)"),
        ("inspection_job", "due_at", "UTC_TIMESTAMP(6)"),
        ("inspection_job", "latest_start_at", "UTC_TIMESTAMP(6)"),
        ("inspection_job", "deadline_at", "UTC_TIMESTAMP(6) + INTERVAL 1 DAY"),
        ("inspection_run", "spec_json", "JSON_OBJECT()"),
        ("inspection_run", "boot_id", "'other-boot'"),
        ("inspection_run", "assignment_epoch", "2"),
        ("inspection_result", "payload_json", "JSON_OBJECT()"),
        ("inspection_result", "submitted_at", "UTC_TIMESTAMP(6)"),
        ("inspection_claim_receipt", "request_id", "'other-request'"),
    ],
)
def test_worker_cannot_change_platform_or_immutable_columns(
    mysql_worker_connect: Factory, table: str, column: str, expression: str
) -> None:
    # Identifiers and expressions are fixed test literals, never application input.
    with pytest.raises(pymysql.OperationalError) as rejected:
        execute(mysql_worker_connect, f"UPDATE {table} SET {column}={expression} WHERE 1=0")
    assert rejected.value.args[0] in {1142, 1143}


@pytest.mark.parametrize(
    "statement",
    [
        "DELETE FROM inspection_result",
        "DELETE FROM inspection_run",
        "DROP TABLE inspection_job",
        "ALTER TABLE inspection_control ADD COLUMN forbidden_column INT NULL",
        "INSERT INTO inspection_control (control_id) VALUES ('forbidden-control')",
        "INSERT INTO inspection_budget (budget_key,capacity) VALUES ('forbidden-budget',1)",
        "INSERT INTO inspection_job (job_id) VALUES ('forbidden-job')",
        "SELECT * FROM inspection_evidence",
        "INSERT INTO inspection_evidence (id) VALUES (1)",
        "SELECT * FROM mysql.user",
    ],
)
def test_worker_cannot_delete_publish_or_access_system_tables(
    mysql_worker_connect: Factory, statement: str
) -> None:
    with pytest.raises(pymysql.OperationalError) as rejected:
        execute(mysql_worker_connect, statement)
    assert rejected.value.args[0] in {1142, 1143}


def test_worker_cannot_read_a_non_exchange_table(
    mysql_connect: Factory, mysql_worker_connect: Factory
) -> None:
    execute(mysql_connect, "CREATE TABLE non_exchange_fixture (id INT PRIMARY KEY)")
    execute(mysql_connect, "INSERT INTO non_exchange_fixture VALUES (1)")
    assert execute(mysql_connect, "SELECT id FROM non_exchange_fixture") == [{"id": 1}]
    with pytest.raises(pymysql.OperationalError) as rejected:
        execute(mysql_worker_connect, "SELECT * FROM non_exchange_fixture")
    assert rejected.value.args[0] == 1142


def start_fixture(
    publisher: Factory, worker: Factory, *, lease_seconds: int = 30
) -> tuple[Repository, RunLease, ControlCommand]:
    publish_platform_fixture(publisher, ("BLACK_SCREEN", "OCCLUSION"))
    command = ControlCommand(
        contract_version="1.0",
        request_id="privilege-start",
        control_id=CONTROL,
        control_seq=1,
        expected_boot_id=BOOT,
        action="START",
        trace_id="b" * 32,
    )
    repo = Repository(worker, NODE, BOOT, lease_seconds=lease_seconds)
    repo.accept_control(command)
    repo.set_admission(command, Admission(ready=True, available_slots=2))
    lease = repo.claim(CONTROL, 1, "privilege-claim")
    assert lease is not None
    repo.start_run(lease.run_id, lease.assignment_epoch)
    return repo, lease, command


def result_fixtures(worker: Factory, lease: RunLease) -> list[CheckResult]:
    started_at = execute(
        worker, "SELECT started_at FROM inspection_run WHERE run_id=%s", (lease.run_id,)
    )[0]["started_at"].replace(tzinfo=UTC)
    return [
        CheckResult(
            detector_key=check.detector_key,
            item_generation=check.item_generation,
            config_digest=check.config_digest,
            implementation_version=check.implementation_version,
            outcome="UNKNOWN",
            reason_code="F01_CONTRACT_FIXTURE_ONLY",
            observed_start=started_at,
            observed_end=started_at,
            sample_count=0,
        )
        for check in lease.job.checks
    ]


def fixture_latest_items(
    submitted_job: JobSpec,
    committed_rows: list[dict[str, Any]],
    current_job: JobSpec,
    *,
    authorized: bool,
) -> set[str]:
    """F01 scope/item example only; excludes epoch and latest-cycle ordering."""
    if not authorized or (
        submitted_job.plan_id != current_job.plan_id
        or submitted_job.target_id != current_job.target_id
        or submitted_job.profile_id != current_job.profile_id
    ):
        return set()
    current_checks = {check.detector_key: check for check in current_job.checks}
    accepted: set[str] = set()
    for row in committed_rows:
        result = CheckResult.model_validate_json(row["payload_json"])
        assert row["submitted_at"] is not None and row["result_digest"] == result.digest()
        check = current_checks.get(result.detector_key)
        if check is not None and (
            check.item_generation,
            check.config_digest,
            check.implementation_version,
        ) == (result.item_generation, result.config_digest, result.implementation_version):
            accepted.add(result.detector_key)
    return accepted


def committed_rows(worker: Factory, lease: RunLease) -> list[dict[str, Any]]:
    return execute(
        worker,
        "SELECT * FROM inspection_result WHERE run_id=%s ORDER BY detector_key",
        (lease.run_id,),
    )


def test_committed_result_can_be_accepted_after_lease_expiry(
    mysql_connect: Factory, mysql_worker_connect: Factory
) -> None:
    repo, lease, _ = start_fixture(mysql_connect, mysql_worker_connect, lease_seconds=2)
    results = result_fixtures(mysql_worker_connect, lease)
    for result in results:
        repo.submit_result(lease.run_id, lease.assignment_epoch, result)
    rows = committed_rows(mysql_worker_connect, lease)
    assert len(rows) == 2
    for row in rows:
        assert row["submitted_at"].replace(tzinfo=UTC) <= lease.lease_expires_at
    deadline = time.monotonic() + 5
    expired = 0
    while time.monotonic() < deadline:
        expired = execute(
            mysql_worker_connect,
            "SELECT lease_expires_at < UTC_TIMESTAMP(6) AS expired "
            "FROM inspection_run WHERE run_id=%s",
            (lease.run_id,),
        )[0]["expired"]
        if expired:
            break
        time.sleep(0.1)
    assert expired == 1
    assert fixture_latest_items(lease.job, rows, lease.job, authorized=True) == {
        "BLACK_SCREEN",
        "OCCLUSION",
    }
    # A committed retry remains the same receipt after expiry; it inserts no new result.
    submitted_at = repo.submit_result(lease.run_id, lease.assignment_epoch, results[0])
    assert submitted_at == rows[0]["submitted_at"].replace(tzinfo=UTC)
    assert len(committed_rows(mysql_worker_connect, lease)) == 2
    repo.release_run(lease.run_id, lease.assignment_epoch, BOOT, resources_released=True)


def test_one_changed_check_does_not_discard_an_unchanged_check(
    mysql_connect: Factory, mysql_worker_connect: Factory
) -> None:
    repo, lease, _ = start_fixture(mysql_connect, mysql_worker_connect)
    for result in result_fixtures(mysql_worker_connect, lease):
        repo.submit_result(lease.run_id, lease.assignment_epoch, result)
    current = lease.job.model_copy(
        update={
            "plan_revision": 2,
            "checks": tuple(
                check.model_copy(update={"item_generation": 2, "config_digest": "c" * 64})
                if check.detector_key == "BLACK_SCREEN"
                else check
                for check in lease.job.checks
            ),
        }
    )
    rows = committed_rows(mysql_worker_connect, lease)
    assert fixture_latest_items(lease.job, rows, current, authorized=True) == {"OCCLUSION"}
    assert len(rows) == 2
    new_profile = current.model_copy(update={"profile_id": "mainstream"})
    assert fixture_latest_items(lease.job, rows, new_profile, authorized=True) == set()
    assert (
        execute(
            mysql_worker_connect,
            "SELECT spec_digest FROM inspection_job WHERE job_id=%s",
            (lease.job.job_id,),
        )[0]["spec_digest"]
        == lease.job.digest()
    )
    repo.release_run(lease.run_id, lease.assignment_epoch, BOOT, resources_released=True)


def test_current_state_fixture_does_not_accept_another_plan(
    mysql_connect: Factory, mysql_worker_connect: Factory
) -> None:
    repo, lease, _ = start_fixture(mysql_connect, mysql_worker_connect)
    first = result_fixtures(mysql_worker_connect, lease)[0]
    repo.submit_result(lease.run_id, lease.assignment_epoch, first)
    rows = committed_rows(mysql_worker_connect, lease)
    other_plan = lease.job.model_copy(update={"plan_id": "another-plan"})
    assert fixture_latest_items(lease.job, rows, other_plan, authorized=True) == set()
    assert fixture_latest_items(lease.job, rows, lease.job, authorized=True) == {"BLACK_SCREEN"}
    repo.release_run(lease.run_id, lease.assignment_epoch, BOOT, resources_released=True)


def test_revoked_plan_control_fences_new_work_and_preserves_history(
    mysql_connect: Factory, mysql_worker_connect: Factory
) -> None:
    repo, lease, start = start_fixture(mysql_connect, mysql_worker_connect)
    first, unsubmitted = result_fixtures(mysql_worker_connect, lease)
    submitted_at = repo.submit_result(lease.run_id, lease.assignment_epoch, first)
    # Platform fixture revokes the affected plan/node before delivering its STOP notice.
    execute(
        mysql_connect,
        "UPDATE inspection_control SET control_seq=2,desired_state='STOPPED' WHERE control_id=%s",
        (CONTROL,),
    )
    for operation in (
        lambda: repo.claim(CONTROL, 1, "claim-after-revoke"),
        lambda: repo.start_run(lease.run_id, lease.assignment_epoch),
        lambda: repo.renew_run(lease.run_id, lease.assignment_epoch),
        lambda: repo.submit_result(lease.run_id, lease.assignment_epoch, unsubmitted),
    ):
        with pytest.raises(InspectionError, match="STALE_CONTROL"):
            operation()
    stop = start.model_copy(
        update={"control_seq": 2, "request_id": "privilege-stop", "action": "STOP"}
    )
    assert repo.accept_control(stop).observed_state == "STOPPING"
    assert repo.submit_result(lease.run_id, lease.assignment_epoch, first) == submitted_at
    rows = committed_rows(mysql_worker_connect, lease)
    assert len(rows) == 1
    assert fixture_latest_items(lease.job, rows, lease.job, authorized=False) == set()
    repo.release_run(lease.run_id, lease.assignment_epoch, BOOT, resources_released=True)
    assert repo.get_control(CONTROL).observed_state == "STOPPED"


def test_revoked_database_write_grant_rolls_back_claim(
    mysql_connect: Factory,
    mysql_worker_config: DatabaseConfig,
    mysql_worker_connect: Factory,
) -> None:
    repo, lease, _ = start_fixture(mysql_connect, mysql_worker_connect)
    repo.release_run(lease.run_id, lease.assignment_epoch, BOOT, resources_released=True)
    execute(
        mysql_connect,
        f"REVOKE UPDATE (used_slots) ON `{mysql_worker_config.database}`.inspection_budget "
        "FROM %s@'127.0.0.1'",
        (mysql_worker_config.user,),
    )
    with pytest.raises(InspectionError, match="DATABASE_UNAVAILABLE"):
        repo.claim(CONTROL, 1, "claim-after-grant-revoked")
    assert execute(mysql_connect, "SELECT COUNT(*) AS n FROM inspection_run")[0]["n"] == 1
    assert {
        row["used_slots"] for row in execute(mysql_connect, "SELECT * FROM inspection_budget")
    } == {0}
    assert (
        execute(
            mysql_connect,
            "SELECT COUNT(*) AS n FROM inspection_claim_receipt WHERE request_id=%s",
            ("claim-after-grant-revoked",),
        )[0]["n"]
        == 0
    )
