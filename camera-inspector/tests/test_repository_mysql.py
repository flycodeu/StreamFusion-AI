"""Real isolated MySQL tests. Fixtures emulate the not-yet-integrated platform publisher."""

from concurrent.futures import ThreadPoolExecutor
from dataclasses import dataclass
from datetime import UTC, timedelta
from threading import Barrier, Event

import pytest

from camera_inspector.errors import InspectionError
from camera_inspector.repository import Repository
from camera_inspector.types import Admission, CheckResult, CheckSpec, ControlCommand, JobSpec

pytestmark = pytest.mark.mysql
DIGEST = "a" * 64


def execute(factory, sql, args=()):
    connection = factory()
    try:
        with connection.cursor() as cursor:
            cursor.execute(sql, args)
            rows = cursor.fetchall() if cursor.description else []
        connection.commit()
        return rows
    finally:
        connection.close()


def now(factory):
    return execute(factory, "SELECT UTC_TIMESTAMP(6) AS now")[0]["now"].replace(tzinfo=UTC)


def naive(value):
    return value.astimezone(UTC).replace(tzinfo=None)


@dataclass
class Setup:
    factory: object
    repo: Repository
    command: ControlCommand


def control(factory, node="node-a", boot="boot-a", control_id="control-a", plan="plan-a"):
    current = now(factory)
    execute(
        factory,
        "INSERT INTO inspection_control "
        "(control_id,plan_id,node_id,target_boot_id,control_seq,desired_state,expires_at) "
        "VALUES (%s,%s,%s,%s,1,'RUNNING',%s)",
        (control_id, plan, node, boot, naive(current + timedelta(minutes=5))),
    )
    command = ControlCommand(
        contract_version="1.0",
        request_id="start-request",
        control_id=control_id,
        control_seq=1,
        expected_boot_id=boot,
        action="START",
        trace_id="trace-start",
    )
    return Setup(factory, Repository(factory, node, boot), command)


def budget(factory, key, capacity=4):
    execute(
        factory,
        "INSERT INTO inspection_budget (budget_key,capacity) VALUES (%s,%s)",
        (key, capacity),
    )


def publish(factory, job_id="job-a", plan="plan-a", keys=("nvr:nvr-a",), checks=None):
    current = now(factory)
    spec = JobSpec(
        job_id=job_id,
        plan_id=plan,
        plan_revision=1,
        cycle_no=1,
        target_id=f"target-{job_id}",
        profile_id="profile-sub",
        due_at=current - timedelta(seconds=1),
        latest_start_at=current + timedelta(minutes=2),
        deadline_at=current + timedelta(minutes=3),
        checks=checks
        or (
            CheckSpec(
                detector_key="BLACK_SCREEN",
                item_generation=1,
                config_digest=DIGEST,
                implementation_version="contract-probe",
            ),
        ),
        budget_keys=keys,
    )
    execute(
        factory,
        "INSERT INTO inspection_job "
        "(job_id,plan_id,due_at,latest_start_at,deadline_at,spec_json,spec_digest) "
        "VALUES (%s,%s,%s,%s,%s,%s,%s)",
        (
            spec.job_id,
            spec.plan_id,
            naive(spec.due_at),
            naive(spec.latest_start_at),
            naive(spec.deadline_at),
            spec.canonical_json(),
            spec.digest(),
        ),
    )
    return spec


def ready(setup, slots=4):
    setup.repo.accept_control(setup.command)
    setup.repo.set_admission(setup.command, Admission(ready=True, available_slots=slots))


def prepare(factory, *, with_job=True, slots=4):
    setup = control(factory)
    for key in ("global", "node:node-a", "nvr:nvr-a"):
        budget(factory, key)
    if with_job:
        publish(factory)
    ready(setup, slots)
    return setup


def stop(setup, seq=2):
    execute(
        setup.factory,
        "UPDATE inspection_control SET control_seq=%s,desired_state='STOPPED' WHERE control_id=%s",
        (seq, setup.command.control_id),
    )
    command = setup.command.model_copy(
        update={"action": "STOP", "control_seq": seq, "request_id": "stop-request"}
    )
    return command, setup.repo.accept_control(command)


def claimed(setup, request="claim-a"):
    lease = setup.repo.claim(setup.command.control_id, setup.command.control_seq, request)
    assert lease is not None
    return lease


def result_for(setup, lease, **updates):
    run = execute(
        setup.factory, "SELECT started_at FROM inspection_run WHERE run_id=%s", (lease.run_id,)
    )[0]
    observed = run["started_at"].replace(tzinfo=UTC)
    result = CheckResult(
        detector_key="BLACK_SCREEN",
        item_generation=1,
        config_digest=DIGEST,
        implementation_version="contract-probe",
        outcome="UNKNOWN",
        reason_code="CONTRACT_PROBE_ONLY",
        observed_start=observed,
        observed_end=observed,
        sample_count=0,
    )
    return result.model_copy(update=updates)


def used(factory):
    return {
        row["budget_key"]: row["used_slots"]
        for row in execute(factory, "SELECT * FROM inspection_budget ORDER BY budget_key")
    }


def cursor_job(factory):
    return execute(
        factory, "SELECT claim_after_job_id FROM inspection_control WHERE control_id='control-a'"
    )[0]["claim_after_job_id"]


def replace_job(factory, spec):
    execute(
        factory,
        "UPDATE inspection_job SET plan_id=%s,due_at=%s,latest_start_at=%s,deadline_at=%s,"
        "spec_json=%s,spec_digest=%s WHERE job_id=%s",
        (
            spec.plan_id,
            naive(spec.due_at),
            naive(spec.latest_start_at),
            naive(spec.deadline_at),
            spec.canonical_json(),
            spec.digest(),
            spec.job_id,
        ),
    )


def test_control_replay_preserves_ready_and_rejects_conflicting_action(mysql_factory):
    setup = prepare(mysql_factory, with_job=False)
    resent = setup.command.model_copy(
        update={"request_id": "delivery-retry", "trace_id": "trace-2"}
    )
    assert setup.repo.accept_control(resent).observed_state == "READY"
    with pytest.raises(InspectionError, match="CONTROL_CONFLICT"):
        setup.repo.accept_control(setup.command.model_copy(update={"action": "STOP"}))


def test_stop_fences_late_start_and_late_admission(mysql_factory):
    setup = prepare(mysql_factory, with_job=False)
    _, stopped = stop(setup)
    assert stopped.observed_state == "STOPPED"
    with pytest.raises(InspectionError, match="STALE_CONTROL"):
        setup.repo.accept_control(setup.command)
    with pytest.raises(InspectionError, match="STALE_CONTROL"):
        setup.repo.set_admission(setup.command, Admission(ready=True, available_slots=4))
    assert setup.repo.get_control(setup.command.control_id).observed_state == "STOPPED"


def test_no_claim_before_http_acceptance_and_admission(mysql_factory):
    setup = control(mysql_factory)
    for key in ("global", "node:node-a", "nvr:nvr-a"):
        budget(mysql_factory, key)
    publish(mysql_factory)
    with pytest.raises(InspectionError, match="BOOT_MISMATCH|CONTROL_NOT_READY"):
        setup.repo.claim("control-a", 1, "claim-before-start")
    setup.repo.accept_control(setup.command)
    with pytest.raises(InspectionError, match="CONTROL_NOT_READY"):
        setup.repo.claim("control-a", 1, "claim-before-ready")
    setup.repo.set_admission(
        setup.command,
        Admission(ready=False, available_slots=0, reason_code="EXECUTOR_NOT_IMPLEMENTED"),
    )
    assert setup.repo.get_control("control-a").observed_state == "WAITING_RESOURCE"
    assert set(used(mysql_factory).values()) == {0}


def test_empty_claim_receipt_cannot_allocate_a_later_job(mysql_factory):
    setup = prepare(mysql_factory, with_job=False)
    assert setup.repo.claim("control-a", 1, "empty-request") is None
    publish(mysql_factory)
    assert setup.repo.claim("control-a", 1, "empty-request") is None
    assert claimed(setup, "new-request").job.job_id == "job-a"


def test_claim_replay_preserves_run_and_budget(mysql_factory):
    setup = prepare(mysql_factory)
    first = claimed(setup)
    second = claimed(setup)
    assert first == second
    assert set(used(mysql_factory).values()) == {1}
    assert execute(mysql_factory, "SELECT COUNT(*) AS n FROM inspection_run")[0]["n"] == 1


def test_same_request_different_control_is_rejected(mysql_factory):
    setup = prepare(mysql_factory)
    claimed(setup)
    other = control(mysql_factory, control_id="control-other", plan="plan-b")
    ready(other)
    with pytest.raises(InspectionError, match="CLAIM_REQUEST_CONFLICT"):
        other.repo.claim("control-other", 1, "claim-a")


def test_missing_mandatory_node_budget_fails_closed(mysql_factory):
    setup = control(mysql_factory)
    budget(mysql_factory, "global")
    budget(mysql_factory, "nvr:nvr-a")
    publish(mysql_factory)
    ready(setup)
    with pytest.raises(InspectionError, match="MISSING_BUDGET"):
        claimed(setup)
    assert set(used(mysql_factory).values()) == {0}
    assert execute(mysql_factory, "SELECT COUNT(*) AS n FROM inspection_run")[0]["n"] == 0


def test_admission_slots_are_consumed_until_reassessment(mysql_factory):
    setup = prepare(mysql_factory, slots=1)
    publish(mysql_factory, "job-b")
    lease = claimed(setup)
    assert setup.repo.claim("control-a", 1, "no-admission-left") is None
    setup.repo.release_run(lease.run_id, 1, "boot-a", resources_released=True)
    assert setup.repo.claim("control-a", 1, "not-reassessed") is None
    setup.repo.set_admission(setup.command, Admission(ready=True, available_slots=1))
    assert claimed(setup, "reassessed").job.job_id == "job-b"


def test_two_nodes_cannot_claim_one_job_or_oversell_global_budget(mysql_factory):
    first = control(mysql_factory)
    second = control(mysql_factory, node="node-b", boot="boot-b", control_id="control-b")
    for key in ("global", "node:node-a", "node:node-b", "nvr:nvr-a"):
        budget(mysql_factory, key, 1)
    publish(mysql_factory)
    ready(first)
    ready(second)
    barrier = Barrier(2)

    def take(setup):
        barrier.wait(timeout=5)
        return setup.repo.claim(setup.command.control_id, 1, "competing-claim")

    with ThreadPoolExecutor(max_workers=2) as executor:
        results = list(executor.map(take, (first, second)))
    assert sum(result is not None for result in results) == 1
    counts = used(mysql_factory)
    assert counts["global"] == counts["nvr:nvr-a"] == 1
    assert counts["node:node-a"] + counts["node:node-b"] == 1


def test_shared_nvr_budget_limits_distinct_jobs_across_nodes(mysql_factory):
    first = control(mysql_factory)
    second = control(mysql_factory, node="node-b", boot="boot-b", control_id="control-b")
    for key in ("global", "node:node-a", "node:node-b"):
        budget(mysql_factory, key, 4)
    budget(mysql_factory, "nvr:nvr-a", 1)
    publish(mysql_factory)
    publish(mysql_factory, "job-b")
    ready(first)
    ready(second)
    with ThreadPoolExecutor(max_workers=2) as executor:
        futures = [
            executor.submit(item.repo.claim, item.command.control_id, 1, "claim")
            for item in (first, second)
        ]
        results = [future.result(timeout=10) for future in futures]
    assert sum(item is not None for item in results) == 1
    assert used(mysql_factory)["nvr:nvr-a"] == 1


def test_concurrent_same_claim_request_has_one_receipt(mysql_factory):
    setup = prepare(mysql_factory)
    with ThreadPoolExecutor(max_workers=2) as executor:
        futures = [executor.submit(setup.repo.claim, "control-a", 1, "same") for _ in range(2)]
        leases = [future.result(timeout=10) for future in futures]
    assert leases[0] == leases[1]
    assert set(used(mysql_factory).values()) == {1}
    assert execute(mysql_factory, "SELECT COUNT(*) AS n FROM inspection_claim_receipt")[0]["n"] == 1


def test_claim_rechecks_committed_receipt_after_control_lock(mysql_factory):
    setup = prepare(mysql_factory)
    lease = claimed(setup)
    publish(mysql_factory, job_id="job-b")
    first_read = Event()

    def repeatable_read():
        connection = mysql_factory()
        with connection.cursor() as cursor:
            cursor.execute("SET SESSION TRANSACTION ISOLATION LEVEL REPEATABLE READ")
        connection.commit()
        return connection

    repo = Repository(repeatable_read, "node-a", "boot-a")
    original_receipt = repo._receipt

    def observed_receipt(connection, request_id, *, lock=False):
        row = original_receipt(connection, request_id, lock=lock)
        if not lock:
            first_read.set()
        return row

    repo._receipt = observed_receipt
    writer = mysql_factory()
    try:
        writer.begin()
        with writer.cursor() as cursor:
            cursor.execute(
                "SELECT control_id FROM inspection_control WHERE control_id='control-a' FOR UPDATE"
            )
        with ThreadPoolExecutor(max_workers=1) as executor:
            replay = executor.submit(repo.claim, "control-a", 1, "late-receipt")
            assert first_read.wait(timeout=3)
            with writer.cursor() as cursor:
                cursor.execute(
                    "INSERT INTO inspection_claim_receipt "
                    "(node_id,boot_id,request_id,control_id,control_seq,run_id,created_at) "
                    "VALUES ('node-a','boot-a','late-receipt','control-a',1,%s,UTC_TIMESTAMP(6))",
                    (lease.run_id,),
                )
            writer.commit()
            assert replay.result(timeout=10) == lease
        assert set(used(mysql_factory).values()) == {1}
    finally:
        writer.rollback()
        writer.close()


def test_result_rechecks_committed_receipt_after_control_lock(mysql_factory):
    setup = prepare(mysql_factory)
    lease = claimed(setup)
    setup.repo.start_run(lease.run_id, 1)
    result = result_for(setup, lease)
    first_read = Event()

    def repeatable_read():
        connection = mysql_factory()
        with connection.cursor() as cursor:
            cursor.execute("SET SESSION TRANSACTION ISOLATION LEVEL REPEATABLE READ")
        connection.commit()
        return connection

    repo = Repository(repeatable_read, "node-a", "boot-a")
    original_run = repo._run

    def observed_run(connection, run_id, *, lock=False):
        row = original_run(connection, run_id, lock=lock)
        if not lock:
            first_read.set()
        return row

    repo._run = observed_run
    writer = mysql_factory()
    try:
        writer.begin()
        with writer.cursor() as cursor:
            cursor.execute(
                "SELECT control_id FROM inspection_control WHERE control_id='control-a' FOR UPDATE"
            )
        with ThreadPoolExecutor(max_workers=1) as executor:
            replay = executor.submit(repo.submit_result, lease.run_id, 1, result)
            assert first_read.wait(timeout=3)
            with writer.cursor() as cursor:
                cursor.execute(
                    "INSERT INTO inspection_result "
                    "(run_id,detector_key,result_digest,payload_json,submitted_at) "
                    "VALUES (%s,%s,%s,%s,UTC_TIMESTAMP(6))",
                    (lease.run_id, result.detector_key, result.digest(), result.canonical_json()),
                )
            writer.commit()
            submitted = replay.result(timeout=10)
        stored = execute(
            mysql_factory,
            "SELECT submitted_at FROM inspection_result WHERE run_id=%s",
            (lease.run_id,),
        )[0]["submitted_at"].replace(tzinfo=UTC)
        assert submitted == stored
        assert execute(mysql_factory, "SELECT COUNT(*) AS n FROM inspection_result")[0]["n"] == 1
    finally:
        writer.rollback()
        writer.close()


def test_unknown_cleanup_keeps_budget_and_confirmed_release_is_idempotent(mysql_factory):
    setup = prepare(mysql_factory)
    lease = claimed(setup)
    stop(setup)
    setup.repo.release_run(lease.run_id, 1, "boot-a", resources_released=False)
    assert set(used(mysql_factory).values()) == {1}
    assert setup.repo.get_control("control-a").observed_state == "STOPPING"
    setup.repo.release_run(lease.run_id, 1, "boot-a", resources_released=True)
    setup.repo.release_run(lease.run_id, 1, "boot-a", resources_released=True)
    assert set(used(mysql_factory).values()) == {0}
    assert setup.repo.get_control("control-a").observed_state == "STOPPED"


def test_waiting_resource_pauses_claims_but_keeps_reserved_run_authorized(mysql_factory):
    setup = prepare(mysql_factory)
    lease = claimed(setup)
    publish(mysql_factory, job_id="job-b")
    setup.repo.set_admission(
        setup.command,
        Admission(ready=False, available_slots=0, reason_code="NO_FREE_SLOT"),
    )
    with pytest.raises(InspectionError, match="CONTROL_NOT_READY"):
        setup.repo.claim("control-a", 1, "no-new-claim")
    setup.repo.start_run(lease.run_id, 1)
    setup.repo.renew_run(lease.run_id, 1)
    result = result_for(setup, lease)
    assert setup.repo.submit_result(lease.run_id, 1, result) is not None
    assert set(used(mysql_factory).values()) == {1}
    setup.repo.release_run(lease.run_id, 1, "boot-a", resources_released=True)
    assert set(used(mysql_factory).values()) == {0}
    assert setup.repo.get_control("control-a").observed_state == "WAITING_RESOURCE"


def test_stop_still_revokes_reserved_run_while_admission_is_waiting(mysql_factory):
    setup = prepare(mysql_factory)
    lease = claimed(setup)
    setup.repo.start_run(lease.run_id, 1)
    setup.repo.set_admission(
        setup.command,
        Admission(ready=False, available_slots=0, reason_code="NO_FREE_SLOT"),
    )
    result = result_for(setup, lease)
    stop(setup)
    with pytest.raises(InspectionError, match="STALE_CONTROL|CONTROL_STOPPED"):
        setup.repo.renew_run(lease.run_id, 1)
    with pytest.raises(InspectionError, match="STALE_CONTROL|CONTROL_STOPPED"):
        setup.repo.submit_result(lease.run_id, 1, result)
    assert set(used(mysql_factory).values()) == {1}


def test_new_boot_rejects_old_start_but_can_reconcile_old_run(mysql_factory):
    setup = prepare(mysql_factory)
    lease = claimed(setup)
    fresh = Repository(mysql_factory, "node-a", "boot-new")
    with pytest.raises(InspectionError, match="BOOT_MISMATCH"):
        fresh.accept_control(setup.command)
    execute(
        mysql_factory,
        "UPDATE inspection_control SET target_boot_id='boot-new',control_seq=2 "
        "WHERE control_id='control-a'",
    )
    new_command = setup.command.model_copy(
        update={"expected_boot_id": "boot-new", "control_seq": 2}
    )
    fresh.accept_control(new_command)
    fresh.set_admission(new_command, Admission(ready=True, available_slots=4))
    with pytest.raises(InspectionError, match="STALE_CONTROL|BOOT_MISMATCH"):
        setup.repo.renew_run(lease.run_id, 1)
    fresh.release_run(lease.run_id, 1, "boot-a", resources_released=True)
    assert set(used(mysql_factory).values()) == {0}
    assert fresh.get_control("control-a").observed_state == "READY"


@pytest.mark.parametrize("cleanup_by_old_boot", [False, True])
def test_current_boot_stop_finishes_after_old_run_cleanup(mysql_factory, cleanup_by_old_boot):
    setup = prepare(mysql_factory)
    lease = claimed(setup)
    fresh = Repository(mysql_factory, "node-a", "boot-new")
    execute(
        mysql_factory,
        "UPDATE inspection_control SET target_boot_id='boot-new',control_seq=2,"
        "desired_state='STOPPED' WHERE control_id='control-a'",
    )
    command = setup.command.model_copy(
        update={"expected_boot_id": "boot-new", "control_seq": 2, "action": "STOP"}
    )
    assert fresh.accept_control(command).observed_state == "STOPPING"
    cleaner = setup.repo if cleanup_by_old_boot else fresh
    cleaner.release_run(lease.run_id, 1, "boot-a", resources_released=True)
    state = fresh.get_control("control-a").observed_state
    assert state == ("STOPPING" if cleanup_by_old_boot else "STOPPED")
    # A repeated STOP on the current boot reconciles cleanup without releasing twice.
    assert fresh.accept_control(command).observed_state == "STOPPED"
    fresh.release_run(lease.run_id, 1, "boot-a", resources_released=True)
    assert set(used(mysql_factory).values()) == {0}


def test_expired_control_stops_claim_without_freeing_active_budget(mysql_factory):
    setup = prepare(mysql_factory)
    lease = claimed(setup)
    execute(
        mysql_factory,
        "UPDATE inspection_control SET expires_at=UTC_TIMESTAMP(6)-INTERVAL 1 SECOND "
        "WHERE control_id='control-a'",
    )
    with pytest.raises(InspectionError, match="CONTROL_EXPIRED"):
        setup.repo.claim("control-a", 1, "new")
    with pytest.raises(InspectionError, match="CONTROL_EXPIRED"):
        setup.repo.renew_run(lease.run_id, 1)
    assert set(used(mysql_factory).values()) == {1}
    setup.repo.release_run(lease.run_id, 1, "boot-a", resources_released=True)
    assert set(used(mysql_factory).values()) == {0}


def test_renewal_is_capped_by_control_and_hard_deadline(mysql_factory):
    setup = prepare(mysql_factory)
    lease = claimed(setup)
    setup.repo.start_run(lease.run_id, 1)
    end = now(mysql_factory) + timedelta(seconds=5)
    execute(
        mysql_factory,
        "UPDATE inspection_control SET expires_at=%s WHERE control_id='control-a'",
        (naive(end),),
    )
    renewed = setup.repo.renew_run(lease.run_id, 1)
    assert renewed.lease_expires_at == end
    assert renewed.lease_expires_at <= renewed.job.deadline_at


def test_result_submission_uses_db_time_and_survives_late_platform_processing(mysql_factory):
    setup = prepare(mysql_factory)
    lease = claimed(setup)
    setup.repo.start_run(lease.run_id, 1)
    result = result_for(setup, lease)
    before = now(mysql_factory)
    submitted = setup.repo.submit_result(lease.run_id, 1, result)
    after = now(mysql_factory)
    assert before <= submitted <= after
    execute(
        mysql_factory,
        "UPDATE inspection_control SET expires_at=UTC_TIMESTAMP(6)-INTERVAL 1 SECOND "
        "WHERE control_id='control-a'",
    )
    assert setup.repo.submit_result(lease.run_id, 1, result) == submitted
    assert execute(mysql_factory, "SELECT COUNT(*) AS n FROM inspection_result")[0]["n"] == 1
    setup.repo.release_run(lease.run_id, 1, "boot-a", resources_released=True)
    assert set(used(mysql_factory).values()) == {0}


def test_concurrent_identical_result_returns_first_submission(mysql_factory):
    setup = prepare(mysql_factory)
    lease = claimed(setup)
    setup.repo.start_run(lease.run_id, 1)
    result = result_for(setup, lease)
    with ThreadPoolExecutor(max_workers=2) as executor:
        futures = [
            executor.submit(setup.repo.submit_result, lease.run_id, 1, result) for _ in range(2)
        ]
        submitted = [future.result(timeout=10) for future in futures]
    assert submitted[0] == submitted[1]
    assert execute(mysql_factory, "SELECT COUNT(*) AS n FROM inspection_result")[0]["n"] == 1


def test_changed_result_replay_conflicts(mysql_factory):
    setup = prepare(mysql_factory)
    lease = claimed(setup)
    setup.repo.start_run(lease.run_id, 1)
    result = result_for(setup, lease)
    setup.repo.submit_result(lease.run_id, 1, result)
    with pytest.raises(InspectionError, match="RESULT_CONFLICT"):
        setup.repo.submit_result(
            lease.run_id, 1, result.model_copy(update={"reason_code": "DIFFERENT"})
        )


@pytest.mark.parametrize(
    ("updates", "code"),
    [
        ({"item_generation": 2}, "RESULT_CONFIG_MISMATCH"),
        ({"config_digest": "b" * 64}, "RESULT_CONFIG_MISMATCH"),
        ({"implementation_version": "different"}, "RESULT_CONFIG_MISMATCH"),
        ({"detector_key": "BLUE_SCREEN"}, "RESULT_CONFIG_MISMATCH"),
    ],
)
def test_result_must_match_its_enabled_check(mysql_factory, updates, code):
    setup = prepare(mysql_factory)
    lease = claimed(setup)
    setup.repo.start_run(lease.run_id, 1)
    with pytest.raises(InspectionError, match=code):
        setup.repo.submit_result(lease.run_id, 1, result_for(setup, lease, **updates))
    assert execute(mysql_factory, "SELECT COUNT(*) AS n FROM inspection_result")[0]["n"] == 0


def test_result_cannot_claim_future_or_pre_start_observations(mysql_factory):
    setup = prepare(mysql_factory)
    lease = claimed(setup)
    setup.repo.start_run(lease.run_id, 1)
    result = result_for(setup, lease)
    for invalid in (
        result.model_copy(update={"observed_end": now(mysql_factory) + timedelta(hours=1)}),
        result.model_copy(update={"observed_start": result.observed_start - timedelta(seconds=1)}),
    ):
        with pytest.raises(InspectionError, match="INVALID_OBSERVATION_WINDOW"):
            setup.repo.submit_result(lease.run_id, 1, invalid)


def test_new_result_rejected_after_stop(mysql_factory):
    setup = prepare(mysql_factory)
    lease = claimed(setup)
    setup.repo.start_run(lease.run_id, 1)
    result = result_for(setup, lease)
    stop(setup)
    with pytest.raises(InspectionError, match="STALE_CONTROL|CONTROL_STOPPED"):
        setup.repo.submit_result(lease.run_id, 1, result)
    assert execute(mysql_factory, "SELECT COUNT(*) AS n FROM inspection_result")[0]["n"] == 0


def test_old_attempt_cannot_submit_new_result(mysql_factory):
    setup = prepare(mysql_factory)
    lease = claimed(setup)
    setup.repo.start_run(lease.run_id, 1)
    execute(mysql_factory, "UPDATE inspection_job SET assignment_epoch=2 WHERE job_id='job-a'")
    with pytest.raises(InspectionError, match="STALE_RUN"):
        setup.repo.submit_result(lease.run_id, 1, result_for(setup, lease))


def test_stop_and_claim_race_is_serialized(mysql_factory):
    setup = prepare(mysql_factory)
    barrier = Barrier(2)

    def take():
        barrier.wait(timeout=5)
        try:
            return setup.repo.claim("control-a", 1, "racing")
        except InspectionError as error:
            assert error.code in {"STALE_CONTROL", "CONTROL_STOPPED"}
            return None

    def halt():
        barrier.wait(timeout=5)
        return stop(setup)

    with ThreadPoolExecutor(max_workers=2) as executor:
        claim_future = executor.submit(take)
        stop_future = executor.submit(halt)
        lease = claim_future.result(timeout=10)
        stop_future.result(timeout=10)
    with pytest.raises(InspectionError, match="STALE_CONTROL"):
        setup.repo.claim("control-a", 1, "after-stop")
    counts = used(mysql_factory)
    assert set(counts.values()) == ({1} if lease else {0})


def test_corrupt_snapshot_is_rejected_without_allocating(mysql_factory):
    setup = prepare(mysql_factory)
    execute(mysql_factory, "UPDATE inspection_job SET spec_digest=%s", ("b" * 64,))
    with pytest.raises(InspectionError, match="JOB_SNAPSHOT_CONFLICT"):
        claimed(setup)
    assert set(used(mysql_factory).values()) == {0}


def test_maximum_node_and_prefixed_scope_identifiers_can_claim(mysql_factory):
    node_id = "n" * 64
    scope = "source:" + "s" * 121
    setup = control(mysql_factory, node=node_id)
    for key in ("global", f"node:{node_id}", scope):
        budget(mysql_factory, key)
    publish(mysql_factory, keys=(scope,))
    ready(setup)
    lease = claimed(setup)
    assert lease.node_id == node_id
    assert set(used(mysql_factory).values()) == {1}
    setup.repo.release_run(lease.run_id, 1, "boot-a", resources_released=True)
    assert set(used(mysql_factory).values()) == {0}


def test_bounded_cursor_reaches_other_source_and_wraps_without_replaying(mysql_factory):
    setup = prepare(mysql_factory, with_job=False)
    setup.repo = Repository(mysql_factory, "node-a", "boot-a", candidate_limit=2)
    budget(mysql_factory, "nvr:hot", capacity=0)
    budget(mysql_factory, "nvr:cool")
    first = publish(mysql_factory, "job-a", keys=("nvr:hot",))
    second = publish(mysql_factory, "job-b", keys=("nvr:hot",))
    third = publish(mysql_factory, "job-c", keys=("nvr:cool",))
    # Equal times exercise the stable jobId tie-breaker, not incidental clock ordering.
    for spec in (second, third):
        replace_job(
            mysql_factory,
            spec.model_copy(
                update={
                    "due_at": first.due_at,
                    "latest_start_at": first.latest_start_at,
                    "deadline_at": first.deadline_at,
                }
            ),
        )
    assert setup.repo.claim("control-a", 1, "page-one") is None
    assert cursor_job(mysql_factory) == "job-b"
    assert setup.repo.claim("control-a", 1, "page-one") is None
    assert cursor_job(mysql_factory) == "job-b"
    resumed = Repository(mysql_factory, "node-a", "boot-a", candidate_limit=2)
    lease = resumed.claim("control-a", 1, "page-two")
    assert lease is not None and lease.job.job_id == "job-c"
    assert cursor_job(mysql_factory) is None
    execute(mysql_factory, "UPDATE inspection_budget SET capacity=2 WHERE budget_key='nvr:hot'")
    next_lease = resumed.claim("control-a", 1, "wrapped")
    assert next_lease is not None and next_lease.job.job_id == "job-a"


def test_new_control_sequence_resets_cursor_but_start_replay_does_not(mysql_factory):
    setup = prepare(mysql_factory)
    setup.repo = Repository(mysql_factory, "node-a", "boot-a", candidate_limit=1)
    execute(mysql_factory, "UPDATE inspection_budget SET capacity=0 WHERE budget_key='nvr:nvr-a'")
    assert setup.repo.claim("control-a", 1, "blocked-page") is None
    assert cursor_job(mysql_factory) == "job-a"
    setup.repo.accept_control(setup.command)
    assert cursor_job(mysql_factory) == "job-a"
    execute(
        mysql_factory, "UPDATE inspection_control SET control_seq=2 WHERE control_id='control-a'"
    )
    setup.repo.accept_control(setup.command.model_copy(update={"control_seq": 2}))
    assert cursor_job(mysql_factory) is None


def test_scope_change_waits_for_claim_transaction(mysql_factory, monkeypatch):
    setup = prepare(mysql_factory)
    candidates_read = Event()
    continue_claim = Event()
    update_started = Event()
    update_finished = Event()
    original = setup.repo._candidates

    def paused_candidates(connection, control):
        rows = original(connection, control)
        candidates_read.set()
        assert continue_claim.wait(timeout=5)
        return rows

    def change_scope():
        update_started.set()
        execute(
            mysql_factory,
            "UPDATE inspection_control SET plan_id='plan-new',control_seq=2 "
            "WHERE control_id='control-a'",
        )
        update_finished.set()

    monkeypatch.setattr(setup.repo, "_candidates", paused_candidates)
    with ThreadPoolExecutor(max_workers=2) as executor:
        claim_future = executor.submit(setup.repo.claim, "control-a", 1, "before-scope-change")
        assert candidates_read.wait(timeout=5)
        update_future = executor.submit(change_scope)
        try:
            assert update_started.wait(timeout=5)
            assert not update_finished.wait(timeout=0.1)
        finally:
            continue_claim.set()
        lease = claim_future.result(timeout=5)
        update_future.result(timeout=5)
    assert lease is not None and lease.job.plan_id == "plan-a"
    with pytest.raises(InspectionError, match="STALE_CONTROL"):
        setup.repo.start_run(lease.run_id, 1)
    setup.repo.release_run(lease.run_id, 1, "boot-a", resources_released=True)
    assert set(used(mysql_factory).values()) == {0}


@pytest.mark.parametrize("change", ["scope", "budget", "column"])
def test_locked_candidate_is_revalidated_after_concurrent_change(
    mysql_factory, monkeypatch, change
):
    setup = prepare(mysql_factory)
    original = setup.repo._budget_rows
    changed = False

    def change_then_lock(connection, keys):
        nonlocal changed
        if not changed:
            changed = True
            if change == "column":
                execute(
                    mysql_factory,
                    "UPDATE inspection_job SET deadline_at=deadline_at+INTERVAL 1 SECOND",
                )
            else:
                row = execute(mysql_factory, "SELECT spec_json FROM inspection_job")[0]
                spec = JobSpec.model_validate_json(row["spec_json"])
                updates = (
                    {"plan_id": "plan-other"}
                    if change == "scope"
                    else {"budget_keys": ("nvr:new",)}
                )
                replace_job(mysql_factory, spec.model_copy(update=updates))
        return original(connection, keys)

    monkeypatch.setattr(setup.repo, "_budget_rows", change_then_lock)
    if change == "column":
        with pytest.raises(InspectionError, match="JOB_SNAPSHOT_CONFLICT"):
            setup.repo.claim("control-a", 1, "changed-candidate")
    else:
        assert setup.repo.claim("control-a", 1, "changed-candidate") is None
    assert execute(mysql_factory, "SELECT COUNT(*) AS n FROM inspection_run")[0]["n"] == 0
    assert set(used(mysql_factory).values()) == {0}


def test_new_result_after_lease_expiry_is_rejected_without_freeing_budget(mysql_factory):
    setup = prepare(mysql_factory)
    lease = claimed(setup)
    setup.repo.start_run(lease.run_id, 1)
    result = result_for(setup, lease)
    execute(
        mysql_factory,
        "UPDATE inspection_run SET lease_expires_at=claimed_at+INTERVAL 1 MICROSECOND",
    )
    with pytest.raises(InspectionError, match="LEASE_EXPIRED"):
        setup.repo.submit_result(lease.run_id, 1, result)
    assert execute(mysql_factory, "SELECT COUNT(*) AS n FROM inspection_result")[0]["n"] == 0
    assert set(used(mysql_factory).values()) == {1}


def test_one_changed_item_does_not_block_another_item_result(mysql_factory):
    setup = prepare(mysql_factory, with_job=False)
    checks = (
        CheckSpec(
            detector_key="BLACK_SCREEN",
            item_generation=1,
            config_digest=DIGEST,
            implementation_version="contract-probe",
        ),
        CheckSpec(
            detector_key="BLUR",
            item_generation=5,
            config_digest="b" * 64,
            implementation_version="contract-probe",
        ),
    )
    publish(mysql_factory, checks=checks)
    lease = claimed(setup)
    setup.repo.start_run(lease.run_id, 1)
    with pytest.raises(InspectionError, match="RESULT_CONFIG_MISMATCH"):
        setup.repo.submit_result(lease.run_id, 1, result_for(setup, lease, item_generation=2))
    blur = result_for(setup, lease, detector_key="BLUR", item_generation=5, config_digest="b" * 64)
    setup.repo.submit_result(lease.run_id, 1, blur)
    assert execute(mysql_factory, "SELECT state FROM inspection_run")[0]["state"] == "RUNNING"
    setup.repo.submit_result(lease.run_id, 1, result_for(setup, lease))
    assert execute(mysql_factory, "SELECT state FROM inspection_run")[0]["state"] == "FINISHED"
    assert execute(mysql_factory, "SELECT COUNT(*) AS n FROM inspection_result")[0]["n"] == 2
    assert set(used(mysql_factory).values()) == {1}


def test_old_attempt_cleanup_only_releases_its_budget(mysql_factory):
    setup = prepare(mysql_factory)
    old = claimed(setup)
    # Explicit publisher recovery creates a new attempt using remaining real capacity.
    execute(mysql_factory, "UPDATE inspection_job SET state='QUEUED' WHERE job_id='job-a'")
    current = claimed(setup, request="second-attempt")
    assert current.assignment_epoch == 2
    assert set(used(mysql_factory).values()) == {2}
    setup.repo.release_run(old.run_id, 1, "boot-a", resources_released=False)
    assert execute(mysql_factory, "SELECT state FROM inspection_job")[0]["state"] == "CLAIMED"
    setup.repo.release_run(old.run_id, 1, "boot-a", resources_released=True)
    setup.repo.release_run(old.run_id, 1, "boot-a", resources_released=True)
    assert set(used(mysql_factory).values()) == {1}
    assert execute(mysql_factory, "SELECT state FROM inspection_job")[0]["state"] == "CLAIMED"
    setup.repo.start_run(current.run_id, 2)
    setup.repo.release_run(current.run_id, 2, "boot-a", resources_released=True)
    assert set(used(mysql_factory).values()) == {0}


def test_expired_observed_control_cannot_revive_by_extending_same_sequence(mysql_factory):
    setup = prepare(mysql_factory)
    execute(
        mysql_factory,
        "UPDATE inspection_control SET "
        "accepted_expires_at=UTC_TIMESTAMP(6)-INTERVAL 1 SECOND,"
        "expires_at=UTC_TIMESTAMP(6)+INTERVAL 2 MINUTE WHERE control_id='control-a'",
    )
    with pytest.raises(InspectionError, match="CONTROL_RESTART_REQUIRED"):
        setup.repo.claim("control-a", 1, "stale-extension")
    with pytest.raises(InspectionError, match="CONTROL_RESTART_REQUIRED"):
        setup.repo.accept_control(setup.command)
    execute(
        mysql_factory,
        "UPDATE inspection_control SET control_seq=2 WHERE control_id='control-a'",
    )
    command = setup.command.model_copy(update={"control_seq": 2})
    setup.repo.accept_control(command)
    setup.repo.set_admission(command, Admission(ready=True, available_slots=1))
    assert setup.repo.claim("control-a", 2, "fresh-start") is not None


def test_live_control_can_observe_platform_extension_before_old_deadline(mysql_factory):
    setup = prepare(mysql_factory)
    deadline = now(mysql_factory) + timedelta(minutes=10)
    execute(
        mysql_factory,
        "UPDATE inspection_control SET expires_at=%s WHERE control_id='control-a'",
        (naive(deadline),),
    )
    assert setup.repo.get_control("control-a").observed_state == "READY"
    row = execute(mysql_factory, "SELECT accepted_expires_at FROM inspection_control")[0]
    assert row["accepted_expires_at"].replace(tzinfo=UTC) == deadline
