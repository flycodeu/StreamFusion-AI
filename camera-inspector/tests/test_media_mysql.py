"""F02 experiment assembly: actual FFmpeg exit gates the isolated MySQL budget.

This is deliberately test-side assembly. The production publisher/consumer is P2.
"""

import json
import shutil
import socket
import threading
import time
from concurrent.futures import ThreadPoolExecutor
from dataclasses import replace
from datetime import UTC, datetime
from pathlib import Path

import pytest
from test_control_mysql import BOOT, CONTROL, NODE, execute, publish_platform_fixture

from camera_inspector.db import DatabaseConfig, make_connection_factory
from camera_inspector.errors import InspectionError
from camera_inspector.media import MediaSession
from camera_inspector.media_types import MediaSpec
from camera_inspector.processes import ManagedProcess
from camera_inspector.repository import Repository
from camera_inspector.types import Admission, ControlCommand

pytestmark = [pytest.mark.mysql, pytest.mark.media]


@pytest.fixture
def bounded_video(tmp_path: Path) -> Path:
    ffmpeg = shutil.which("ffmpeg")
    if ffmpeg is None:
        pytest.fail("F02 media/MySQL verification requires FFmpeg on PATH")
    path = tmp_path / "synthetic.mp4"
    process = ManagedProcess(
        [
            ffmpeg,
            "-hide_banner",
            "-loglevel",
            "error",
            "-f",
            "lavfi",
            "-i",
            "testsrc2=size=128x72:rate=8",
            "-t",
            "20",
            "-c:v",
            "mpeg4",
            "-threads",
            "1",
            str(path),
        ]
    )
    try:
        assert process.wait(10) == 0
        assert path.stat().st_size > 0
    finally:
        process.close()
    return path


def prepare_run(mysql_connect, mysql_worker_connect, *, lease_seconds=10):
    publish_platform_fixture(mysql_connect, ("STREAM_STABILITY",))
    repo = Repository(mysql_worker_connect, NODE, BOOT, lease_seconds=lease_seconds)
    start = ControlCommand(
        contract_version="1.0",
        request_id="media-start",
        control_id=CONTROL,
        control_seq=1,
        expected_boot_id=BOOT,
        action="START",
        trace_id="f02-media-mysql",
    )
    repo.accept_control(start)
    repo.set_admission(start, Admission(ready=True, available_slots=1))
    lease = repo.claim(CONTROL, 1, "media-claim")
    assert lease is not None
    return repo, start, repo.start_run(lease.run_id, lease.assignment_epoch)


def occupied(mysql_connect):
    return {row["used_slots"] for row in execute(mysql_connect, "SELECT * FROM inspection_budget")}


def test_real_media_stop_releases_only_after_exit(
    mysql_connect,
    mysql_worker_connect,
    bounded_video,
    tmp_path,
    record_testsuite_property,
):
    repo, start, lease = prepare_run(mysql_connect, mysql_worker_connect)
    stop = threading.Event()
    session = MediaSession(
        MediaSpec(
            source=str(bounded_video),
            output_dir=tmp_path / "stop-session",
            run_id=lease.run_id,
            trace_id=lease.trace_id,
            observation_seconds=20,
            hard_timeout_seconds=8,
            record_original=False,
            grace_seconds=0.3,
            kill_seconds=0.7,
        )
    )
    with ThreadPoolExecutor(max_workers=1) as pool:
        future = pool.submit(session.run, stop)
        try:
            sample = session.poll_sample(5)
            if sample is None and future.done():
                future.result()
            assert sample is not None
            execute(
                mysql_connect,
                "UPDATE inspection_control SET control_seq=2,desired_state='STOPPED' "
                "WHERE control_id=%s",
                (CONTROL,),
            )
            command = start.model_copy(
                update={"request_id": "media-stop", "control_seq": 2, "action": "STOP"}
            )
            assert repo.accept_control(command).observed_state == "STOPPING"
            with pytest.raises(InspectionError, match="STALE_CONTROL"):
                repo.renew_run(lease.run_id, lease.assignment_epoch)
            repo.release_run(lease.run_id, lease.assignment_epoch, BOOT, resources_released=False)
            assert occupied(mysql_connect) == {1}
        finally:
            stop.set()
        report = future.result(timeout=8)
    assert report.cleanup_confirmed
    assert report.status == "STOPPED"
    assert report.sampled_frames > 0
    repo.release_run(
        lease.run_id, lease.assignment_epoch, BOOT, resources_released=report.cleanup_confirmed
    )
    assert occupied(mysql_connect) == {0}
    assert repo.get_control(CONTROL).observed_state == "STOPPED"
    assert execute(mysql_connect, "SELECT COUNT(*) AS n FROM inspection_result")[0]["n"] == 0
    record_testsuite_property("mysql_media_stop_report", report.model_dump_json())


def test_db_network_failure_keeps_budget_until_exit_and_release_retry(
    mysql_connect,
    mysql_worker_connect,
    mysql_worker_config: DatabaseConfig,
    bounded_video,
    tmp_path,
    record_testsuite_property,
):
    repo, _, lease = prepare_run(mysql_connect, mysql_worker_connect, lease_seconds=6)
    # No HTTP notification/renewal can extend this locally frozen deadline.
    remaining = (lease.lease_expires_at - datetime.now(UTC)).total_seconds()
    lease_deadline = time.monotonic() + remaining
    local_deadline = lease_deadline - 2
    stop = threading.Event()
    session = MediaSession(
        MediaSpec(
            source=str(bounded_video),
            output_dir=tmp_path / "disconnected-session",
            run_id=lease.run_id,
            trace_id=lease.trace_id,
            observation_seconds=20,
            hard_timeout_seconds=15,
            monotonic_deadline=local_deadline,
            record_original=False,
            grace_seconds=0.3,
            kill_seconds=0.7,
        )
    )
    # A bound, non-listening loopback socket produces a real TCP refusal. It never
    # redirects a query to another database or touches the existing business instance.
    with socket.socket() as refused, ThreadPoolExecutor(max_workers=1) as pool:
        refused.bind(("127.0.0.1", 0))
        disconnected = Repository(
            make_connection_factory(
                replace(
                    mysql_worker_config,
                    port=refused.getsockname()[1],
                    timeout_seconds=1,
                )
            ),
            NODE,
            BOOT,
        )
        future = pool.submit(session.run, stop)
        try:
            sample = session.poll_sample(3)
            if sample is None and future.done():
                future.result()
            assert sample is not None
            with pytest.raises(InspectionError, match="DATABASE_UNAVAILABLE"):
                disconnected.renew_run(lease.run_id, lease.assignment_epoch)
            assert occupied(mysql_connect) == {1}
            report = future.result(timeout=8)
            assert report.cleanup_confirmed
            assert time.monotonic() < lease_deadline
            assert report.status != "COMPLETED"
            with pytest.raises(InspectionError, match="DATABASE_UNAVAILABLE"):
                disconnected.release_run(
                    lease.run_id, lease.assignment_epoch, BOOT, resources_released=True
                )
            assert occupied(mysql_connect) == {1}
        finally:
            stop.set()
        # Recovery retries the same release, backed by the saved local exit fact.
        repo.release_run(
            lease.run_id, lease.assignment_epoch, BOOT, resources_released=report.cleanup_confirmed
        )
        repo.release_run(
            lease.run_id, lease.assignment_epoch, BOOT, resources_released=report.cleanup_confirmed
        )
    assert occupied(mysql_connect) == {0}
    assert execute(mysql_connect, "SELECT COUNT(*) AS n FROM inspection_result")[0]["n"] == 0
    record_testsuite_property(
        "mysql_media_disconnect_report", json.dumps(report.model_dump(mode="json"))
    )
