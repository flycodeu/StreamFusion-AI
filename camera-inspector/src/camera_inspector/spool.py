"""Small local upload journal, separate from the platform's remote storage budget.

The directory is private to one coordinator. Only that coordinator writes its
files; upload children open them read-only. No URL, header or credential is saved.
"""

from __future__ import annotations

import os
import shutil
import stat
import threading
from datetime import UTC, datetime
from pathlib import Path
from typing import IO, Literal, Self

from pydantic import AwareDatetime, BaseModel, ConfigDict, Field, ValidationError, model_validator

from camera_inspector.errors import InspectionError
from camera_inspector.storage_types import UploadGrant, UploadResult

_JOURNAL = "uploads.json"
_LOCK = ".uploads.lock"
_TEMP = ".uploads.json.tmp"
_MAX_JOURNAL_BYTES = 8 * 1024 * 1024


def _binding(grant: UploadGrant) -> dict[str, str | int]:
    return {
        "evidence_id": grant.evidence_id,
        "run_id": grant.run_id,
        "assignment_epoch": grant.assignment_epoch,
        "placement_revision": grant.placement_revision,
        "instance_id": grant.instance_id,
        "scope_id": grant.scope_id,
        "bucket": grant.bucket,
        "object_key": grant.object_key,
        "endpoint_revision": grant.endpoint_revision,
        "size_bytes": grant.size_bytes,
        "content_type": grant.content_type,
        "checksum_algorithm": grant.checksum_algorithm,
        "checksum_base64": grant.checksum_base64,
    }


class SpoolEntry(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)

    binding: dict[str, str | int]
    trace_id: str
    filename: str
    file_identity: tuple[int, int, int, int]
    retain_until: AwareDatetime
    state: Literal[
        "READY", "IN_FLIGHT", "PUT_ACCEPTED", "NOT_SENT", "REJECTED", "UNKNOWN", "CONFIRMED"
    ] = "READY"
    local_state: Literal["PRESENT", "RELEASED", "EXPIRED", "LOST"] = "PRESENT"
    cleanup_confirmed: bool | None = None
    reason_code: str | None = None
    confirmation_id: str | None = None
    object_version: str | None = None

    @model_validator(mode="after")
    def valid_record(self) -> Self:
        text_fields = {
            "evidence_id",
            "run_id",
            "instance_id",
            "scope_id",
            "bucket",
            "object_key",
            "content_type",
            "checksum_algorithm",
            "checksum_base64",
        }
        number_fields = {
            "assignment_epoch",
            "placement_revision",
            "endpoint_revision",
            "size_bytes",
        }
        if set(self.binding) != text_fields | number_fields:
            raise ValueError("incomplete persisted upload identity")
        for name in text_fields:
            value = self.binding[name]
            if not isinstance(value, str) or not value or len(value) > 1024:
                raise ValueError("invalid persisted upload identity")
        for name in number_fields:
            value = self.binding[name]
            if type(value) is not int or value < 1:
                raise ValueError("invalid persisted upload identity")
        digest = self.binding["checksum_base64"]
        if self.binding["checksum_algorithm"] != "SHA256" or not isinstance(digest, str):
            raise ValueError("invalid persisted checksum")
        UploadGrant.sha256_digest(digest)
        if self.file_identity[2] != self.binding["size_bytes"]:
            raise ValueError("persisted file size does not match upload")
        if (
            (self.state == "READY" and self.cleanup_confirmed is not None)
            or (self.state == "IN_FLIGHT" and self.cleanup_confirmed is not False)
            or (self.state == "UNKNOWN" and self.cleanup_confirmed is None)
            or (
                self.state in {"PUT_ACCEPTED", "NOT_SENT", "REJECTED", "CONFIRMED"}
                and self.cleanup_confirmed is not True
            )
            or (self.state == "IN_FLIGHT" and self.local_state != "PRESENT")
            or (self.local_state == "RELEASED" and self.state != "CONFIRMED")
            or (self.state == "CONFIRMED" and not self.confirmation_id)
            or (self.state != "CONFIRMED" and self.confirmation_id is not None)
        ):
            raise ValueError("inconsistent persisted upload state")
        return self


class _Journal(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True)

    schema_version: Literal[1] = 1
    revision: int = Field(default=0, ge=0)
    stopped: bool = False
    drain_until: AwareDatetime | None = None
    records: dict[str, SpoolEntry] = Field(default_factory=dict, max_length=4096)


class Spool:
    """Local persistence and admission only; confirmations come from a trusted authority.

    confirm() must be called only after that authority verifies the remote object
    and durably commits its index. A successful PUT is not that confirmation.
    """

    def __init__(
        self, root: Path, *, max_items: int, max_bytes: int, min_free_bytes: int = 0
    ) -> None:
        if (
            any(type(value) is not int for value in (max_items, max_bytes, min_free_bytes))
            or not 1 <= max_items <= 4096
            or max_bytes < 1
            or min_free_bytes < 0
        ):
            raise ValueError("invalid spool limits")
        self.root = root.resolve()
        self.root.mkdir(parents=True, exist_ok=True)
        self.max_items, self.max_bytes, self.min_free_bytes = max_items, max_bytes, min_free_bytes
        self._mutex = threading.RLock()
        self._closed = False
        for name in (_LOCK, _TEMP, _JOURNAL):
            candidate = self.root / name
            if candidate.exists() or candidate.is_symlink():
                self._identity(candidate)
        self._lock_file: IO[bytes] = (self.root / _LOCK).open("a+b", buffering=0)
        try:
            self._lock_file.seek(0, os.SEEK_END)
            if self._lock_file.tell() == 0:
                self._lock_file.write(b"0")
            self._lock_file.seek(0)
            self._os_lock(True)
        except OSError:
            self._lock_file.close()
            raise InspectionError("SPOOL_IN_USE", 409) from None
        try:
            path = self.root / _JOURNAL
            if path.exists():
                if path.is_symlink() or path.stat().st_size > _MAX_JOURNAL_BYTES:
                    raise InspectionError("SPOOL_JOURNAL_INVALID", 409)
                try:
                    self._journal = _Journal.model_validate_json(path.read_bytes())
                except (ValueError, ValidationError):
                    raise InspectionError("SPOOL_JOURNAL_INVALID", 409) from None
                if len(self._journal.records) > max_items:
                    raise InspectionError("SPOOL_LIMIT_BELOW_RETAINED_RECORDS", 409)
                filenames: set[str] = set()
                for key, entry in self._journal.records.items():
                    self._path(entry.filename)
                    if key != self._entry_key(entry) or entry.filename in filenames:
                        raise InspectionError("SPOOL_JOURNAL_INVALID", 409)
                    filenames.add(entry.filename)
                recovered = {
                    key: entry.model_copy(
                        update={
                            "state": "UNKNOWN",
                            "cleanup_confirmed": False,
                            "reason_code": "COORDINATOR_RESTARTED",
                        }
                    )
                    if entry.state == "IN_FLIGHT"
                    else entry
                    for key, entry in self._journal.records.items()
                }
                self._save(records=recovered)
            else:
                self._journal = _Journal()
                self._save()
        except BaseException:
            self.close()
            raise

    def _os_lock(self, acquire: bool) -> None:
        if os.name == "nt":
            import msvcrt

            self._lock_file.seek(0)
            msvcrt.locking(
                self._lock_file.fileno(), msvcrt.LK_NBLCK if acquire else msvcrt.LK_UNLCK, 1
            )
        else:
            import fcntl

            getattr(fcntl, "flock")(
                self._lock_file.fileno(),
                (getattr(fcntl, "LOCK_EX") | getattr(fcntl, "LOCK_NB"))
                if acquire
                else getattr(fcntl, "LOCK_UN"),
            )

    def __enter__(self) -> Self:
        return self

    def __exit__(self, *_args: object) -> None:
        self.close()

    def close(self) -> None:
        with self._mutex:
            if not self._closed:
                try:
                    self._os_lock(False)
                finally:
                    self._lock_file.close()
                    self._closed = True

    def _save(self, **changes: object) -> None:
        self._require_open()
        candidate = self._journal.model_copy(
            update={**changes, "revision": self._journal.revision + 1}
        )
        data = candidate.model_dump_json().encode("utf-8")
        if len(data) > _MAX_JOURNAL_BYTES:
            raise InspectionError("SPOOL_JOURNAL_LIMIT", 409)
        temporary = self.root / _TEMP
        with temporary.open("wb") as stream:
            stream.write(data)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, self.root / _JOURNAL)
        self._journal = candidate

    def _require_open(self) -> None:
        if self._closed:
            raise InspectionError("SPOOL_CLOSED", 409)

    def _path(self, filename: str) -> Path:
        if (
            Path(filename).name != filename
            or filename in {"", ".", "..", _JOURNAL, _LOCK, _TEMP}
            or len(filename) > 255
            or "/" in filename
            or "\\" in filename
            or ":" in filename
        ):
            raise InspectionError("SPOOL_PATH_NOT_OWNED", 422)
        return self.root / filename

    @staticmethod
    def _identity(path: Path) -> tuple[int, int, int, int]:
        info = path.lstat()
        if not stat.S_ISREG(info.st_mode) or getattr(info, "st_file_attributes", 0) & 0x400:
            raise InspectionError("SPOOL_PATH_NOT_OWNED", 422)
        return info.st_dev, info.st_ino, info.st_size, info.st_mtime_ns

    @staticmethod
    def _key(grant: UploadGrant) -> str:
        return f"{grant.evidence_id}:{grant.placement_revision}"

    @staticmethod
    def _entry_key(entry: SpoolEntry) -> str:
        try:
            return f"{entry.binding['evidence_id']}:{entry.binding['placement_revision']}"
        except KeyError:
            raise InspectionError("SPOOL_JOURNAL_INVALID", 409) from None

    def _entry(self, grant: UploadGrant) -> SpoolEntry:
        self._require_open()
        entry = self._journal.records.get(self._key(grant))
        if entry is None or entry.binding != _binding(grant):
            raise InspectionError("SPOOL_IDENTITY_CONFLICT", 409)
        return entry

    def _put(self, entry: SpoolEntry) -> SpoolEntry:
        self._save(records={**self._journal.records, self._entry_key(entry): entry})
        return entry.model_copy(deep=True)

    @staticmethod
    def _now(value: datetime | None) -> datetime:
        now = value or datetime.now(UTC)
        if now.tzinfo is None:
            raise ValueError("spool times must include a timezone")
        return now.astimezone(UTC)

    def entries(self) -> tuple[SpoolEntry, ...]:
        with self._mutex:
            return tuple(item.model_copy(deep=True) for item in self._journal.records.values())

    def register(self, grant: UploadGrant, path: Path, *, retain_until: datetime) -> SpoolEntry:
        with self._mutex:
            self._require_open()
            if self._key(grant) in self._journal.records:
                previous = self._entry(grant)
                if path != self._path(previous.filename):
                    raise InspectionError("SPOOL_IDENTITY_CONFLICT", 409)
                return previous.model_copy(deep=True)
            if self._journal.stopped:
                raise InspectionError("SPOOL_STOPPED", 409)
            if len(self._journal.records) >= self.max_items:
                raise InspectionError("SPOOL_ITEM_LIMIT", 409)
            if path.parent.resolve() != self.root or path != self._path(path.name):
                raise InspectionError("SPOOL_PATH_NOT_OWNED", 422)
            identity = self._identity(path)
            if identity[2] != grant.size_bytes:
                raise InspectionError("SPOOL_FILE_CHANGED", 409)
            usage = 0
            for count, existing in enumerate(self.root.iterdir(), 1):
                if count > self.max_items + 3:
                    raise InspectionError("SPOOL_ITEM_LIMIT", 409)
                if existing.name not in {_JOURNAL, _LOCK, _TEMP}:
                    usage += self._identity(existing)[2]
            if usage > self.max_bytes or shutil.disk_usage(self.root).free < self.min_free_bytes:
                raise InspectionError("SPOOL_BYTE_LIMIT", 409)
            end = self._now(retain_until)
            if end <= self._now(None):
                raise InspectionError("SPOOL_RETENTION_EXPIRED", 409)
            if any(item.filename == path.name for item in self._journal.records.values()):
                raise InspectionError("SPOOL_FILE_ALREADY_REGISTERED", 409)
            return self._put(
                SpoolEntry(
                    binding=_binding(grant),
                    trace_id=grant.trace_id,
                    filename=path.name,
                    file_identity=identity,
                    retain_until=end,
                )
            )

    def begin(self, grant: UploadGrant, *, now: datetime | None = None) -> SpoolEntry:
        with self._mutex:
            current = self._now(now)
            entry = self._entry(grant)
            if self._journal.stopped and (
                self._journal.drain_until is None or current >= self._journal.drain_until
            ):
                raise InspectionError("SPOOL_STOPPED", 409)
            if any(
                item.state == "IN_FLIGHT" or item.cleanup_confirmed is False
                for item in self._journal.records.values()
            ):
                raise InspectionError("SPOOL_RESOURCE_UNCONFIRMED", 409)
            if entry.state != "READY" or entry.local_state != "PRESENT":
                raise InspectionError("SPOOL_REPLAY_REQUIRES_RECONCILIATION", 409)
            if current >= min(entry.retain_until, grant.expires_at):
                raise InspectionError("SPOOL_AUTHORIZATION_EXPIRED", 409)
            if self._identity(self._path(entry.filename)) != entry.file_identity:
                raise InspectionError("SPOOL_FILE_CHANGED", 409)
            return self._put(
                entry.model_copy(update={"state": "IN_FLIGHT", "cleanup_confirmed": False})
            )

    def record(self, result: UploadResult) -> SpoolEntry:
        with self._mutex:
            self._require_open()
            key = f"{result.evidence_id}:{result.placement_revision}"
            entry = self._journal.records.get(key)
            if (
                entry is None
                or entry.binding["run_id"] != result.run_id
                or entry.binding["assignment_epoch"] != result.assignment_epoch
            ):
                raise InspectionError("SPOOL_IDENTITY_CONFLICT", 409)
            if entry.state not in {"IN_FLIGHT", "UNKNOWN"}:
                if (
                    entry.state == result.status
                    and entry.cleanup_confirmed == result.cleanup_confirmed
                ):
                    return entry.model_copy(deep=True)
                raise InspectionError("SPOOL_RESULT_CONFLICT", 409)
            if entry.state == "UNKNOWN" and result.status in {"NOT_SENT", "REJECTED"}:
                raise InspectionError("SPOOL_RESULT_REQUIRES_RECONCILIATION", 409)
            # A late receipt cannot revoke independent OS cleanup evidence.
            cleaned = entry.cleanup_confirmed is True or result.cleanup_confirmed
            state = result.status if cleaned else "UNKNOWN"
            return self._put(
                entry.model_copy(
                    update={
                        "state": state,
                        "cleanup_confirmed": cleaned,
                        "reason_code": result.reason_code,
                    }
                )
            )

    def confirm_cleanup(self, grant: UploadGrant) -> SpoolEntry:
        """Caller must have independent OS evidence that the previous worker is gone."""
        with self._mutex:
            entry = self._entry(grant)
            if entry.state != "UNKNOWN":
                raise InspectionError("SPOOL_CLEANUP_NOT_PENDING", 409)
            return self._put(entry.model_copy(update={"cleanup_confirmed": True}))

    def _remove_local(self, entry: SpoolEntry, state: Literal["RELEASED", "EXPIRED"]) -> SpoolEntry:
        if entry.local_state != "PRESENT":
            return entry.model_copy(deep=True)
        path = self._path(entry.filename)
        try:
            if self._identity(path) != entry.file_identity:
                raise InspectionError("SPOOL_FILE_CHANGED", 409)
            path.unlink()
            local_state: Literal["RELEASED", "EXPIRED", "LOST"] = state
        except FileNotFoundError:
            # A durable confirmation can survive a crash after unlink but before
            # saving RELEASED. Replaying it completes cleanup, not a data-loss event.
            local_state = "RELEASED" if entry.state == "CONFIRMED" else "LOST"
        return self._put(entry.model_copy(update={"local_state": local_state}))

    def confirm(
        self,
        grant: UploadGrant,
        *,
        size_bytes: int,
        checksum_base64: str,
        content_type: str,
        confirmation_id: str,
        object_version: str | None = None,
    ) -> SpoolEntry:
        with self._mutex:
            entry = self._entry(grant)
            if (
                not confirmation_id
                or len(confirmation_id) > 128
                or size_bytes != grant.size_bytes
                or checksum_base64 != grant.checksum_base64
                or content_type != grant.content_type
            ):
                raise InspectionError("SPOOL_CONFIRMATION_MISMATCH", 409)
            if entry.cleanup_confirmed is not True:
                raise InspectionError("SPOOL_RESOURCE_UNCONFIRMED", 409)
            if entry.state == "CONFIRMED":
                if (
                    entry.confirmation_id != confirmation_id
                    or entry.object_version != object_version
                ):
                    raise InspectionError("SPOOL_CONFIRMATION_CONFLICT", 409)
            elif entry.state not in {"PUT_ACCEPTED", "UNKNOWN"}:
                raise InspectionError("SPOOL_CONFIRMATION_UNEXPECTED", 409)
            else:
                # Durable confirmation precedes unlink. Replaying confirm finishes a
                # deletion interrupted between these two small local operations.
                entry = self._put(
                    entry.model_copy(
                        update={
                            "state": "CONFIRMED",
                            "confirmation_id": confirmation_id,
                            "object_version": object_version,
                        }
                    )
                )
            return self._remove_local(entry, "RELEASED")

    def expire(self, *, now: datetime | None = None) -> list[SpoolEntry]:
        with self._mutex:
            self._require_open()
            current = self._now(now)
            changed = []
            for entry in tuple(self._journal.records.values()):
                if entry.local_state == "PRESENT" and current >= entry.retain_until:
                    if entry.state == "IN_FLIGHT" or entry.cleanup_confirmed is False:
                        continue
                    changed.append(self._remove_local(entry, "EXPIRED"))
            return changed

    def stop(self, *, drain_until: datetime | None = None) -> None:
        with self._mutex:
            end = self._now(drain_until) if drain_until is not None else None
            if self._journal.stopped:
                previous = self._journal.drain_until
                if end is not None and (previous is None or end > previous):
                    raise InspectionError("SPOOL_STOP_CANNOT_EXTEND", 409)
            self._save(stopped=True, drain_until=end)
