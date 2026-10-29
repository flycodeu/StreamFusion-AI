"""Inspection-only MySQL transactions; no media, model or platform network I/O."""

from __future__ import annotations

import hashlib
import json
import logging
import time
from collections.abc import Callable, Sequence
from datetime import UTC, datetime, timedelta
from typing import Any, TypeVar
from uuid import uuid4

import pymysql
from pydantic import ValidationError
from pymysql.connections import Connection
from pymysql.cursors import DictCursor

from camera_inspector.errors import InspectionError
from camera_inspector.types import (
    Admission,
    CheckResult,
    ControlCommand,
    ControlSnapshot,
    JobSpec,
    RunLease,
)

Row = dict[str, Any]
T = TypeVar("T")
LOGGER = logging.getLogger("camera_inspector.repository")


def _utc(value: datetime) -> datetime:
    return value.replace(tzinfo=UTC) if value.tzinfo is None else value.astimezone(UTC)


def _sql_time(value: datetime) -> datetime:
    return value.astimezone(UTC).replace(tzinfo=None)


def _rows(connection: Connection[DictCursor], sql: str, args: Sequence[object] = ()) -> list[Row]:
    with connection.cursor() as cursor:
        cursor.execute(sql, args)
        return list(cursor.fetchall())


def _one(connection: Connection[DictCursor], sql: str, args: Sequence[object] = ()) -> Row | None:
    with connection.cursor() as cursor:
        cursor.execute(sql, args)
        row: Row | None = cursor.fetchone()
        return row


def _write(connection: Connection[DictCursor], sql: str, args: Sequence[object] = ()) -> int:
    with connection.cursor() as cursor:
        return cursor.execute(sql, args)


def _now(connection: Connection[DictCursor]) -> datetime:
    row = _one(connection, "SELECT UTC_TIMESTAMP(6) AS now")
    assert row is not None
    return _utc(row["now"])


def _rollback(connection: Connection[DictCursor] | None) -> None:
    if connection is not None:
        try:
            connection.rollback()
        except pymysql.MySQLError:
            pass  # A lost socket must not replace the safe, classified public error.


def _command_digest(command: ControlCommand) -> str:
    # Delivery request/trace IDs may differ when the same durable control is resent.
    content = [
        command.contract_version,
        command.control_id,
        command.control_seq,
        command.expected_boot_id,
        command.action,
    ]
    return hashlib.sha256(json.dumps(content, separators=(",", ":")).encode()).hexdigest()


def _snapshot(row: Row) -> ControlSnapshot:
    return ControlSnapshot(
        control_id=row["control_id"],
        control_seq=row["control_seq"],
        plan_id=row["plan_id"],
        node_id=row["node_id"],
        target_boot_id=row["target_boot_id"],
        desired_state=row["desired_state"],
        expires_at=_utc(row["expires_at"]),
        accepted_seq=row["accepted_seq"],
        observed_state=row["observed_state"],
        reason_code=row["reason_code"],
    )


def _job(row: Row) -> JobSpec:
    try:
        job = JobSpec.model_validate_json(row["spec_json"])
    except ValidationError:
        raise InspectionError("INVALID_JOB_SNAPSHOT", 422) from None
    if "spec_digest" in row and (
        row["spec_digest"] != job.digest()
        or row["job_id"] != job.job_id
        or row["plan_id"] != job.plan_id
        or _utc(row["due_at"]) != job.due_at
        or _utc(row["latest_start_at"]) != job.latest_start_at
        or _utc(row["deadline_at"]) != job.deadline_at
    ):
        raise InspectionError("JOB_SNAPSHOT_CONFLICT", 409)
    return job


def _lease(row: Row) -> RunLease:
    return RunLease(
        run_id=row["run_id"],
        assignment_epoch=row["assignment_epoch"],
        claim_request_id=row["claim_request_id"],
        control_id=row["control_id"],
        control_seq=row["control_seq"],
        node_id=row["node_id"],
        boot_id=row["boot_id"],
        lease_expires_at=_utc(row["lease_expires_at"]),
        job=_job(row),
        trace_id=row["trace_id"],
    )


def _log_run(action: str, lease: RunLease) -> None:
    LOGGER.info(
        "inspection %s traceId=%s taskId=%s runId=%s assignmentEpoch=%s nodeId=%s",
        action,
        lease.trace_id or "-",
        lease.job.job_id,
        lease.run_id,
        lease.assignment_epoch,
        lease.node_id,
    )


class Repository:
    def __init__(
        self,
        connect: Callable[[], Connection[DictCursor]],
        node_id: str,
        boot_id: str,
        lease_seconds: int = 30,
        candidate_limit: int = 32,
    ) -> None:
        if not 1 <= lease_seconds <= 300 or not 1 <= candidate_limit <= 256:
            raise ValueError("invalid lease or candidate limit")
        self._connect = connect
        self.node_id = node_id
        self.boot_id = boot_id
        self._lease_seconds = lease_seconds
        self._candidate_limit = candidate_limit

    def _transaction(self, operation: Callable[[Connection[DictCursor]], T]) -> T:
        for attempt in range(3):
            connection: Connection[DictCursor] | None = None
            try:
                connection = self._connect()
                connection.begin()
                result = operation(connection)
                connection.commit()
                return result
            except pymysql.OperationalError as error:
                _rollback(connection)
                if error.args[0] in {1205, 1213} and attempt < 2:
                    time.sleep(0.01 * (attempt + 1))
                    continue
                raise InspectionError("DATABASE_UNAVAILABLE", 503) from None
            except pymysql.IntegrityError:
                _rollback(connection)
                raise InspectionError("DATABASE_CONFLICT", 409) from None
            except pymysql.MySQLError:
                _rollback(connection)
                raise InspectionError("DATABASE_UNAVAILABLE", 503) from None
            except BaseException:
                _rollback(connection)
                raise
            finally:
                if connection is not None:
                    connection.close()
        raise AssertionError("bounded retry exhausted")

    def _control(
        self, connection: Connection[DictCursor], control_id: str, *, lock: bool = False
    ) -> Row:
        row = _one(
            connection,
            "SELECT * FROM inspection_control WHERE control_id=%s AND node_id=%s"
            + (" FOR UPDATE" if lock else ""),
            (control_id, self.node_id),
        )
        if row is None:
            raise InspectionError("CONTROL_NOT_FOUND", 404)
        return row

    def _validate_command(
        self, connection: Connection[DictCursor], row: Row, command: ControlCommand
    ) -> None:
        if command.expected_boot_id != self.boot_id or row["target_boot_id"] != self.boot_id:
            raise InspectionError("BOOT_MISMATCH")
        if row["control_seq"] != command.control_seq:
            raise InspectionError("STALE_CONTROL")
        expected = "RUNNING" if command.action == "START" else "STOPPED"
        if row["desired_state"] != expected:
            raise InspectionError("CONTROL_CONFLICT")
        if command.action == "START":
            now = _now(connection)
            if _utc(row["expires_at"]) <= now:
                raise InspectionError("CONTROL_EXPIRED")
            self._observe_deadline(connection, row, now)

    def _observe_deadline(
        self, connection: Connection[DictCursor], row: Row, now: datetime
    ) -> None:
        if row["accepted_seq"] == row["control_seq"] and row["accepted_boot_id"] == self.boot_id:
            previous = row["accepted_expires_at"]
            if previous is None or _utc(previous) <= now:
                raise InspectionError("CONTROL_RESTART_REQUIRED")
            _write(
                connection,
                "UPDATE inspection_control SET accepted_expires_at=%s WHERE control_id=%s",
                (row["expires_at"], row["control_id"]),
            )
            row["accepted_expires_at"] = row["expires_at"]

    def _authorized(self, connection: Connection[DictCursor], row: Row, seq: int) -> datetime:
        now = _now(connection)
        if row["control_seq"] != seq:
            raise InspectionError("STALE_CONTROL")
        if row["target_boot_id"] != self.boot_id or row["accepted_boot_id"] != self.boot_id:
            raise InspectionError("BOOT_MISMATCH")
        if row["desired_state"] != "RUNNING":
            raise InspectionError("CONTROL_STOPPED")
        if row["accepted_seq"] != seq:
            raise InspectionError("CONTROL_NOT_ACCEPTED")
        if _utc(row["expires_at"]) <= now:
            raise InspectionError("CONTROL_EXPIRED")
        self._observe_deadline(connection, row, now)
        return now

    def _ready(self, connection: Connection[DictCursor], row: Row, seq: int) -> datetime:
        now = self._authorized(connection, row, seq)
        if row["observed_state"] != "READY":
            raise InspectionError("CONTROL_NOT_READY")
        return now

    def get_control(self, control_id: str) -> ControlSnapshot:
        def inspect(connection: Connection[DictCursor]) -> ControlSnapshot:
            row = self._control(connection, control_id, lock=True)
            if (
                row["desired_state"] == "RUNNING"
                and row["target_boot_id"] == self.boot_id
                and row["accepted_boot_id"] == self.boot_id
                and row["accepted_seq"] == row["control_seq"]
            ):
                now = _now(connection)
                try:
                    if _utc(row["expires_at"]) <= now:
                        raise InspectionError("CONTROL_EXPIRED")
                    self._observe_deadline(connection, row, now)
                except InspectionError as error:
                    _write(
                        connection,
                        "UPDATE inspection_control SET observed_state='DISARMED',"
                        "available_slots=0,reason_code=%s WHERE control_id=%s",
                        (error.code, control_id),
                    )
                    row = self._control(connection, control_id)
            return _snapshot(row)

        return self._transaction(inspect)

    def accept_control(self, command: ControlCommand) -> ControlSnapshot:
        def accept(connection: Connection[DictCursor]) -> ControlSnapshot:
            row = self._control(connection, command.control_id, lock=True)
            self._validate_command(connection, row, command)
            digest = _command_digest(command)
            if row["accepted_seq"] == command.control_seq:
                if row["accepted_boot_id"] != self.boot_id or row["command_digest"] != digest:
                    raise InspectionError("CONTROL_CONFLICT")
                if command.action == "STOP":
                    self._complete_stop(connection, row)
                return _snapshot(row)
            state = "PREPARING"
            if command.action == "STOP":
                active = _one(
                    connection,
                    "SELECT COUNT(*) AS n FROM inspection_run "
                    "WHERE control_id=%s AND resources_released=0",
                    (command.control_id,),
                )
                assert active is not None
                state = "STOPPING" if active["n"] else "STOPPED"
            _write(
                connection,
                "UPDATE inspection_control SET accepted_seq=%s,accepted_boot_id=%s,"
                "accepted_expires_at=%s,"
                "command_digest=%s,observed_state=%s,available_slots=0,reason_code=NULL,"
                "trace_id=%s,claim_after_deadline=NULL,claim_after_due=NULL,"
                "claim_after_job_id=NULL WHERE control_id=%s",
                (
                    command.control_seq,
                    self.boot_id,
                    row["expires_at"],
                    digest,
                    state,
                    command.trace_id,
                    command.control_id,
                ),
            )
            return _snapshot(self._control(connection, command.control_id))

        return self._transaction(accept)

    def set_admission(self, command: ControlCommand, admission: Admission) -> ControlSnapshot:
        def update(connection: Connection[DictCursor]) -> ControlSnapshot:
            row = self._control(connection, command.control_id, lock=True)
            self._validate_command(connection, row, command)
            if command.action != "START":
                raise InspectionError("CONTROL_STOPPED")
            if (
                row["accepted_seq"] != command.control_seq
                or row["accepted_boot_id"] != self.boot_id
                or row["command_digest"] != _command_digest(command)
            ):
                raise InspectionError("CONTROL_NOT_ACCEPTED")
            _write(
                connection,
                "UPDATE inspection_control SET observed_state=%s,available_slots=%s,"
                "reason_code=%s WHERE control_id=%s",
                (
                    "READY" if admission.ready else "WAITING_RESOURCE",
                    admission.available_slots if admission.ready else 0,
                    admission.reason_code,
                    command.control_id,
                ),
            )
            return _snapshot(self._control(connection, command.control_id))

        return self._transaction(update)

    def _receipt(
        self, connection: Connection[DictCursor], request_id: str, *, lock: bool = False
    ) -> Row | None:
        return _one(
            connection,
            "SELECT * FROM inspection_claim_receipt "
            "WHERE node_id=%s AND boot_id=%s AND request_id=%s" + (" FOR SHARE" if lock else ""),
            (self.node_id, self.boot_id, request_id),
        )

    def _receipt_lease(
        self, connection: Connection[DictCursor], receipt: Row, control_id: str, seq: int
    ) -> RunLease | None:
        if receipt["control_id"] != control_id or receipt["control_seq"] != seq:
            raise InspectionError("CLAIM_REQUEST_CONFLICT")
        if receipt["run_id"] is None:
            return None
        # The receipt is the idempotency fence; reread its run under the same transaction lock
        # before returning a replay so a concurrent stop/release cannot change the lease snapshot.
        return _lease(self._run(connection, receipt["run_id"], lock=True))

    def _budget_rows(
        self, connection: Connection[DictCursor], keys: Sequence[str]
    ) -> dict[str, Row]:
        ordered = sorted(set(keys))
        if not ordered:
            raise InspectionError("MISSING_BUDGET", 422)
        placeholders = ",".join(["%s"] * len(ordered))
        rows = _rows(
            connection,
            f"SELECT * FROM inspection_budget WHERE budget_key IN ({placeholders}) "
            "ORDER BY budget_key FOR UPDATE",
            ordered,
        )
        if len(rows) != len(ordered):
            raise InspectionError("MISSING_BUDGET", 422)
        return {row["budget_key"]: row for row in rows}

    def _job_keys(self, job: JobSpec) -> tuple[str, ...]:
        node_key = f"node:{self.node_id}"
        if any(key.startswith("node:") and key != node_key for key in job.budget_keys):
            raise InspectionError("INVALID_BUDGET_SCOPE", 422)
        return tuple(sorted({"global", node_key, *job.budget_keys}))

    def _candidates(self, connection: Connection[DictCursor], control: Row) -> list[Row]:
        after = ""
        args: list[object] = [control["plan_id"]]
        if control["claim_after_job_id"] is not None:
            after = " AND (deadline_at,due_at,job_id) > (%s,%s,%s)"
            args.extend(
                (
                    control["claim_after_deadline"],
                    control["claim_after_due"],
                    control["claim_after_job_id"],
                )
            )
        args.append(self._candidate_limit)
        return _rows(
            connection,
            "SELECT * FROM inspection_job WHERE plan_id=%s AND state='QUEUED' "
            "AND due_at<=UTC_TIMESTAMP(6) AND latest_start_at>UTC_TIMESTAMP(6)"
            + after
            + " ORDER BY deadline_at,due_at,job_id LIMIT %s",
            args,
        )

    def _advance_cursor(
        self,
        connection: Connection[DictCursor],
        control_id: str,
        candidates: list[Row],
        scanned: int,
    ) -> None:
        # Wrap only after this bounded page is exhausted; replayed receipts never get here.
        cursor = (
            candidates[scanned - 1]
            if scanned and (scanned < len(candidates) or len(candidates) == self._candidate_limit)
            else None
        )
        _write(
            connection,
            "UPDATE inspection_control SET claim_after_deadline=%s,claim_after_due=%s,"
            "claim_after_job_id=%s WHERE control_id=%s",
            (
                cursor["deadline_at"] if cursor else None,
                cursor["due_at"] if cursor else None,
                cursor["job_id"] if cursor else None,
                control_id,
            ),
        )

    def claim(self, control_id: str, control_seq: int, request_id: str) -> RunLease | None:
        def claim_one(connection: Connection[DictCursor]) -> tuple[RunLease | None, bool]:
            prior = self._receipt(connection, request_id)
            if prior is not None:
                return self._receipt_lease(connection, prior, control_id, control_seq), False
            # Control serializes the cursor and scope. Candidate reads take no job locks.
            control = self._control(connection, control_id, lock=True)
            prior = self._receipt(connection, request_id, lock=True)
            if prior is not None:
                return self._receipt_lease(connection, prior, control_id, control_seq), False
            now = self._ready(connection, control, control_seq)
            candidates = self._candidates(connection, control)
            specs = [(candidate, _job(candidate)) for candidate in candidates]
            keys = sorted({key for _, job in specs for key in self._job_keys(job)})
            budgets = self._budget_rows(connection, keys) if keys else {}
            selected: RunLease | None = None
            scanned = 0
            for candidate, spec in specs:
                scanned += 1
                job = _one(
                    connection,
                    "SELECT * FROM inspection_job WHERE job_id=%s FOR UPDATE SKIP LOCKED",
                    (spec.job_id,),
                )
                if (
                    job is None
                    or job["state"] != "QUEUED"
                    or job["plan_id"] != control["plan_id"]
                    or job["spec_digest"] != candidate["spec_digest"]
                ):
                    continue
                # Revalidate denormalized columns too, not just a copied digest.
                locked_spec = _job(job)
                if locked_spec != spec:
                    continue
                now = self._ready(connection, control, control_seq)
                if not spec.due_at <= now < spec.latest_start_at:
                    continue
                run_keys = self._job_keys(spec)
                if control["available_slots"] < 1 or any(
                    budgets[key]["used_slots"] >= budgets[key]["capacity"] for key in run_keys
                ):
                    continue
                run_id = uuid4().hex
                epoch = job["assignment_epoch"] + 1
                lease_end = min(
                    now + timedelta(seconds=self._lease_seconds),
                    spec.deadline_at,
                    _utc(control["expires_at"]),
                )
                _write(
                    connection,
                    "INSERT INTO inspection_run (run_id,job_id,assignment_epoch,"
                    "claim_request_id,control_id,control_seq,node_id,boot_id,spec_json,"
                    "budget_keys_json,claimed_at,lease_expires_at,trace_id) "
                    "VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,%s)",
                    (
                        run_id,
                        spec.job_id,
                        epoch,
                        request_id,
                        control_id,
                        control_seq,
                        self.node_id,
                        self.boot_id,
                        spec.canonical_json(),
                        json.dumps(run_keys),
                        _sql_time(now),
                        _sql_time(lease_end),
                        control["trace_id"],
                    ),
                )
                for key in run_keys:
                    _write(
                        connection,
                        "UPDATE inspection_budget SET used_slots=used_slots+1 WHERE budget_key=%s",
                        (key,),
                    )
                _write(
                    connection,
                    "UPDATE inspection_job SET state='CLAIMED',assignment_epoch=%s WHERE job_id=%s",
                    (epoch, spec.job_id),
                )
                _write(
                    connection,
                    "UPDATE inspection_control SET available_slots=available_slots-1 "
                    "WHERE control_id=%s",
                    (control_id,),
                )
                selected = _lease(self._run(connection, run_id))
                break
            self._advance_cursor(connection, control_id, candidates, scanned)
            _write(
                connection,
                "INSERT INTO inspection_claim_receipt "
                "(node_id,boot_id,request_id,control_id,control_seq,run_id,created_at) "
                "VALUES (%s,%s,%s,%s,%s,%s,%s)",
                (
                    self.node_id,
                    self.boot_id,
                    request_id,
                    control_id,
                    control_seq,
                    selected.run_id if selected else None,
                    _sql_time(now),
                ),
            )
            return selected, selected is not None

        lease, created = self._transaction(claim_one)
        if created and lease is not None:
            _log_run("claimed", lease)
        return lease

    def _run(self, connection: Connection[DictCursor], run_id: str, *, lock: bool = False) -> Row:
        row = _one(
            connection,
            "SELECT * FROM inspection_run WHERE run_id=%s AND node_id=%s"
            + (" FOR UPDATE" if lock else ""),
            (run_id, self.node_id),
        )
        if row is None:
            raise InspectionError("RUN_NOT_FOUND", 404)
        return row

    def _active_run(
        self, connection: Connection[DictCursor], run_id: str, epoch: int
    ) -> tuple[Row, Row, datetime]:
        initial = self._run(connection, run_id)
        control = self._control(connection, initial["control_id"], lock=True)
        job = _one(
            connection,
            "SELECT * FROM inspection_job WHERE job_id=%s FOR UPDATE",
            (initial["job_id"],),
        )
        run = self._run(connection, run_id, lock=True)
        # Admission can pause new claims without revoking an already reserved run.
        now = self._authorized(connection, control, run["control_seq"])
        if run["boot_id"] != self.boot_id:
            raise InspectionError("BOOT_MISMATCH")
        if run["assignment_epoch"] != epoch or job is None or job["assignment_epoch"] != epoch:
            raise InspectionError("STALE_RUN")
        if run["resources_released"] or run["state"] not in {"CLAIMED", "RUNNING"}:
            raise InspectionError("RUN_NOT_ACTIVE")
        if _utc(run["lease_expires_at"]) <= now or _job(run).deadline_at <= now:
            raise InspectionError("LEASE_EXPIRED")
        return run, control, now

    def start_run(self, run_id: str, epoch: int) -> RunLease:
        def start(connection: Connection[DictCursor]) -> tuple[RunLease, bool]:
            run, _, now = self._active_run(connection, run_id, epoch)
            if run["state"] == "RUNNING":
                return _lease(run), False
            if _job(run).latest_start_at <= now:
                raise InspectionError("START_DEADLINE_EXCEEDED")
            _write(
                connection,
                "UPDATE inspection_run SET state='RUNNING',started_at=%s WHERE run_id=%s",
                (_sql_time(now), run_id),
            )
            _write(
                connection,
                "UPDATE inspection_job SET state='RUNNING' WHERE job_id=%s",
                (run["job_id"],),
            )
            return _lease(self._run(connection, run_id)), True

        lease, changed = self._transaction(start)
        if changed:
            _log_run("started", lease)
        return lease

    def renew_run(self, run_id: str, epoch: int) -> RunLease:
        def renew(connection: Connection[DictCursor]) -> RunLease:
            run, control, now = self._active_run(connection, run_id, epoch)
            spec = _job(run)
            end = min(
                now + timedelta(seconds=self._lease_seconds),
                spec.deadline_at,
                _utc(control["expires_at"]),
            )
            if run["state"] == "CLAIMED":
                end = min(end, spec.latest_start_at)
            if end <= now:
                raise InspectionError("START_DEADLINE_EXCEEDED")
            _write(
                connection,
                "UPDATE inspection_run SET lease_expires_at=%s WHERE run_id=%s",
                (_sql_time(end), run_id),
            )
            return _lease(self._run(connection, run_id))

        return self._transaction(renew)

    def submit_result(self, run_id: str, epoch: int, result: CheckResult) -> datetime:
        def submit(connection: Connection[DictCursor]) -> tuple[datetime, RunLease | None]:
            initial = self._run(connection, run_id)
            if initial["boot_id"] != self.boot_id or initial["assignment_epoch"] != epoch:
                raise InspectionError("STALE_RUN")
            self._control(connection, initial["control_id"], lock=True)
            previous = _one(
                connection,
                "SELECT * FROM inspection_result WHERE run_id=%s AND detector_key=%s FOR SHARE",
                (run_id, result.detector_key),
            )
            if previous is not None:
                if previous["result_digest"] != result.digest():
                    raise InspectionError("RESULT_CONFLICT")
                return _utc(previous["submitted_at"]), None
            run, _, now = self._active_run(connection, run_id, epoch)
            if run["state"] != "RUNNING" or run["started_at"] is None:
                raise InspectionError("RUN_NOT_STARTED")
            spec = _job(run)
            check = next(
                (check for check in spec.checks if check.detector_key == result.detector_key), None
            )
            if check is None or (
                check.item_generation != result.item_generation
                or check.config_digest != result.config_digest
                or check.implementation_version != result.implementation_version
            ):
                raise InspectionError("RESULT_CONFIG_MISMATCH", 422)
            if not _utc(run["started_at"]) <= result.observed_start <= result.observed_end <= now:
                raise InspectionError("INVALID_OBSERVATION_WINDOW", 422)
            _write(
                connection,
                "INSERT INTO inspection_result "
                "(run_id,detector_key,result_digest,payload_json,submitted_at) "
                "VALUES (%s,%s,%s,%s,%s)",
                (
                    run_id,
                    result.detector_key,
                    result.digest(),
                    result.canonical_json(),
                    _sql_time(now),
                ),
            )
            count = _one(
                connection,
                "SELECT COUNT(*) AS n FROM inspection_result WHERE run_id=%s",
                (run_id,),
            )
            assert count is not None
            if count["n"] == len(spec.checks):
                _write(
                    connection,
                    "UPDATE inspection_run SET state='FINISHED' WHERE run_id=%s",
                    (run_id,),
                )
                _write(
                    connection,
                    "UPDATE inspection_job SET state='FINISHED' WHERE job_id=%s",
                    (run["job_id"],),
                )
            return now, _lease(run)

        submitted_at, lease = self._transaction(submit)
        if lease is not None:
            _log_run("result_submitted", lease)
        return submitted_at

    def _complete_stop(self, connection: Connection[DictCursor], control: Row) -> None:
        # Only the current boot may reconcile its STOP after old runs are cleaned up.
        if (
            control["desired_state"] != "STOPPED"
            or control["accepted_seq"] != control["control_seq"]
            or control["accepted_boot_id"] != self.boot_id
            or control["target_boot_id"] != self.boot_id
        ):
            return
        remaining = _one(
            connection,
            "SELECT COUNT(*) AS n FROM inspection_run WHERE control_id=%s AND resources_released=0",
            (control["control_id"],),
        )
        if remaining is not None and remaining["n"] == 0:
            _write(
                connection,
                "UPDATE inspection_control SET observed_state='STOPPED' WHERE control_id=%s",
                (control["control_id"],),
            )
            control["observed_state"] = "STOPPED"

    def release_run(
        self, run_id: str, epoch: int, owner_boot_id: str, *, resources_released: bool
    ) -> None:
        def release(connection: Connection[DictCursor]) -> RunLease | None:
            initial = self._run(connection, run_id)
            control = self._control(connection, initial["control_id"], lock=True)
            keys: list[str] = json.loads(initial["budget_keys_json"])
            budgets = self._budget_rows(connection, keys)
            job = _one(
                connection,
                "SELECT * FROM inspection_job WHERE job_id=%s FOR UPDATE",
                (initial["job_id"],),
            )
            run = self._run(connection, run_id, lock=True)
            if run["assignment_epoch"] != epoch or run["boot_id"] != owner_boot_id:
                raise InspectionError("STALE_RUN")
            if run["resources_released"]:
                self._complete_stop(connection, control)
                return None
            if not resources_released:
                _write(
                    connection,
                    "UPDATE inspection_run SET state='CLEANUP_PENDING' WHERE run_id=%s",
                    (run_id,),
                )
                if job is not None and job["assignment_epoch"] == epoch:
                    _write(
                        connection,
                        "UPDATE inspection_job SET state='CLEANUP_PENDING' WHERE job_id=%s",
                        (run["job_id"],),
                    )
                return None
            if any(budgets[key]["used_slots"] < 1 for key in keys):
                raise InspectionError("BUDGET_LEDGER_CONFLICT")
            for key in sorted(keys):
                _write(
                    connection,
                    "UPDATE inspection_budget SET used_slots=used_slots-1 WHERE budget_key=%s",
                    (key,),
                )
            count = _one(
                connection, "SELECT COUNT(*) AS n FROM inspection_result WHERE run_id=%s", (run_id,)
            )
            assert count is not None
            state = "FINISHED" if count["n"] == len(_job(run).checks) else "CANCELLED"
            _write(
                connection,
                "UPDATE inspection_run SET resources_released=1,released_at=%s,state=%s "
                "WHERE run_id=%s",
                (_sql_time(_now(connection)), state, run_id),
            )
            if job is not None and job["assignment_epoch"] == epoch:
                _write(
                    connection,
                    "UPDATE inspection_job SET state=%s WHERE job_id=%s",
                    (state, run["job_id"]),
                )
            self._complete_stop(connection, control)
            return _lease(run)

        lease = self._transaction(release)
        if lease is not None:
            _log_run("resources_released", lease)
