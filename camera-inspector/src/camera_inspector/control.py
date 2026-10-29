"""HTTP control is admission only; task claims and detector execution are not HTTP jobs."""

import asyncio
import logging
from collections.abc import Callable
from concurrent.futures import ThreadPoolExecutor
from threading import BoundedSemaphore
from typing import Protocol, TypeVar

from camera_inspector.errors import InspectionError
from camera_inspector.types import Admission, ControlCommand, ControlSnapshot

T = TypeVar("T")
Assessor = Callable[[ControlSnapshot], Admission]
_LOGGER = logging.getLogger("camera_inspector.control")


class ControlRepository(Protocol):
    def accept_control(self, command: ControlCommand) -> ControlSnapshot: ...

    def set_admission(self, command: ControlCommand, admission: Admission) -> ControlSnapshot: ...


class _CallLane:
    """One in-flight call, no queue; timeout does not free a still-running DB thread."""

    def __init__(self, name: str, timeout: float) -> None:
        self._executor = ThreadPoolExecutor(max_workers=1, thread_name_prefix=name)
        self._slot = BoundedSemaphore(1)
        self._timeout = timeout

    async def run(self, operation: Callable[[], T]) -> T:
        if not self._slot.acquire(blocking=False):
            raise InspectionError("CONTROL_BUSY", 503)
        try:
            submitted = self._executor.submit(operation)
        except RuntimeError:
            self._slot.release()
            raise InspectionError("NODE_STOPPING", 503) from None
        submitted.add_done_callback(lambda _future: self._slot.release())
        future = asyncio.wrap_future(submitted)
        # Consume a late failure after HTTP timed out; never log its raw exception text.
        future.add_done_callback(lambda done: None if done.cancelled() else done.exception())
        try:
            return await asyncio.wait_for(asyncio.shield(future), self._timeout)
        except TimeoutError:
            raise InspectionError("CONTROL_TIMEOUT", 503) from None

    def close(self) -> None:
        self._executor.shutdown(wait=False, cancel_futures=True)


def unverified_executor(_snapshot: ControlSnapshot) -> Admission:
    """P0 verifies contracts, not video capabilities; production cannot claim ready detectors."""
    return Admission(ready=False, available_slots=0, reason_code="EXECUTOR_NOT_IMPLEMENTED")


class ControlService:
    def __init__(
        self,
        repository: ControlRepository,
        node_id: str,
        boot_id: str,
        *,
        assessor: Assessor | None = None,
        io_timeout_seconds: float = 5,
        admission_timeout_seconds: float = 10,
    ) -> None:
        self.repository = repository
        self.node_id = node_id
        self.boot_id = boot_id
        self._assessor = assessor or unverified_executor
        self._normal = _CallLane("inspection-control", io_timeout_seconds)
        self._stop = _CallLane("inspection-stop", io_timeout_seconds)
        self._assessment = _CallLane("inspection-admission", admission_timeout_seconds)
        self._start_task: asyncio.Task[ControlSnapshot] | None = None
        self._start_key: tuple[str, int] | None = None
        self._closed = False

    async def apply(self, command: ControlCommand) -> ControlSnapshot:
        if self._closed:
            raise InspectionError("NODE_STOPPING", 503)
        if command.expected_boot_id != self.boot_id:
            raise InspectionError("BOOT_MISMATCH", 409)
        if command.action == "STOP":
            return await self._database(self._stop, lambda: self.repository.accept_control(command))
        key = (command.control_id, command.control_seq)
        pending = self._start_task
        if pending is not None and not pending.done():
            if key != self._start_key:
                raise InspectionError("CONTROL_BUSY", 503)
        else:
            pending = asyncio.create_task(self._start(command))
            pending.add_done_callback(lambda done: self._start_finished(command, done))
            self._start_task = pending
            self._start_key = key
        # Disconnecting a caller must not strand an accepted command in PREPARING.
        # There is at most one retained START workflow; repeated delivery shares it.
        return await asyncio.shield(pending)

    async def _start(self, command: ControlCommand) -> ControlSnapshot:
        snapshot = await self._database(
            self._normal, lambda: self.repository.accept_control(command)
        )
        if snapshot.observed_state not in {"PREPARING", "WAITING_RESOURCE"}:
            return snapshot
        try:
            admission = await self._assessment.run(lambda: self._assessor(snapshot))
        except InspectionError as error:
            admission = Admission(ready=False, available_slots=0, reason_code=error.code)
        except Exception:
            admission = Admission(ready=False, available_slots=0, reason_code="ADMISSION_FAILED")
        # The repository CAS must reject an assessment overtaken by STOP/new boot/expiry.
        return await self._database(
            self._normal, lambda: self.repository.set_admission(command, admission)
        )

    def _start_finished(self, command: ControlCommand, task: asyncio.Task[ControlSnapshot]) -> None:
        if not task.cancelled():
            error = task.exception()
            if error is not None:
                code = error.code if isinstance(error, InspectionError) else "INTERNAL_ERROR"
                _LOGGER.warning(
                    "start failed traceId=%s controlId=%s controlSeq=%s code=%s",
                    command.trace_id,
                    command.control_id,
                    command.control_seq,
                    code,
                )
        if self._start_task is task:
            self._start_task = None
            self._start_key = None

    async def _database(self, lane: _CallLane, operation: Callable[[], T]) -> T:
        try:
            return await lane.run(operation)
        except InspectionError:
            raise
        except Exception:
            raise InspectionError("DATABASE_UNAVAILABLE", 503) from None

    def close(self) -> None:
        self._closed = True
        if self._start_task is not None:
            self._start_task.cancel()
        for lane in (self._normal, self._stop, self._assessment):
            lane.close()
