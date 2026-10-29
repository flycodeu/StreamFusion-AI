"""One supervised HTTPS PUT. No retries, placement selection or local deletion."""

from __future__ import annotations

import base64
import hashlib
import http.client
import json
import logging
import os
import re
import ssl
import stat
import sys
import threading
import time
from datetime import UTC, datetime
from pathlib import Path
from typing import IO, Any, Literal
from urllib.parse import urlsplit

from pydantic import BaseModel, ConfigDict, Field, ValidationError

from camera_inspector.errors import InspectionError
from camera_inspector.processes import ManagedProcess
from camera_inspector.storage_types import UploadGrant, UploadLimits, UploadResult

_INPUT_LIMIT = 64 * 1024
_OUTPUT_LIMIT = 16 * 1024
_RESPONSE_LIMIT = 16 * 1024
_LOGGER = logging.getLogger(__name__)


class _Receipt(BaseModel):
    model_config = ConfigDict(extra="forbid", hide_input_in_errors=True)

    status: Literal["NOT_SENT", "PUT_ACCEPTED", "REJECTED", "UNKNOWN"]
    reason_code: str = Field(pattern=r"^[A-Z][A-Z0-9_]{0,63}$")
    http_status: int | None = Field(default=None, ge=100, le=599)
    bytes_sent: int | None = Field(default=0, ge=0)
    etag: str | None = Field(default=None, max_length=130)
    object_version: str | None = Field(default=None, max_length=512)


def _wire(grant: UploadGrant, source: Path, limits: UploadLimits, deadline: float) -> bytes:
    data = grant.model_dump(mode="json")
    data["put_url"] = grant.put_url.get_secret_value()
    data["required_headers"] = {
        key: value.get_secret_value() for key, value in grant.required_headers.items()
    }
    payload = json.dumps(
        {
            "grant": data,
            "source": str(source),
            "limits": limits.model_dump(mode="json"),
            "deadline": deadline,
        },
        separators=(",", ":"),
        allow_nan=False,
    ).encode("utf-8")
    if len(payload) > _INPUT_LIMIT:
        raise InspectionError("UPLOAD_GRANT_TOO_LARGE", 422)
    return payload


def upload_once(
    grant: UploadGrant, source_path: Path, limits: UploadLimits, stop_event: threading.Event
) -> UploadResult:
    """Caller durably marks IN_FLIGHT before this call and retains every source file.

    The work deadline includes child startup, file reads, DNS and network I/O. Cleanup
    has a separate bounded grace + kill + shared reader-join budget. A 2xx receipt is
    PUT_ACCEPTED, never platform verification or permission to remove local evidence.
    """
    # Revalidate copies too: Pydantic model_copy(update=...) intentionally skips validation.
    grant = UploadGrant.model_validate(grant.model_dump())
    limits = UploadLimits.model_validate(limits.model_dump())
    if not source_path.is_absolute():
        raise InspectionError("UPLOAD_SOURCE_MUST_BE_ABSOLUTE", 422)
    started_at = time.monotonic()
    deadline = started_at + limits.total_timeout_seconds
    if limits.monotonic_deadline is not None:
        deadline = min(deadline, limits.monotonic_deadline)
    identity = {
        "evidence_id": grant.evidence_id,
        "run_id": grant.run_id,
        "assignment_epoch": grant.assignment_epoch,
        "placement_revision": grant.placement_revision,
        "trace_id": grant.trace_id,
    }
    if stop_event.is_set() or deadline <= time.monotonic():
        return UploadResult(
            **identity,
            status="NOT_SENT",
            reason_code="UPLOAD_NOT_STARTED",
            bytes_sent=0,
            cleanup_confirmed=True,
            execution_elapsed_seconds=0,
            cleanup_elapsed_seconds=0,
        )
    payload = _wire(grant, source_path, limits, deadline)
    process = ManagedProcess(
        [sys.executable, "-u", "-m", "camera_inspector.storage", "--worker"],
        external_supervision=limits.external_supervision,
    )
    output = bytearray()
    pipe_failed = threading.Event()
    overflow = threading.Event()
    threads: list[threading.Thread] = []
    reason = "UPLOAD_WORKER_LOST"
    receipt: _Receipt | None = None
    cleanup_started = time.monotonic()

    def drain(stream: IO[bytes], capture: bool) -> None:
        try:
            while data := stream.read(4096):
                if capture:
                    remaining = _OUTPUT_LIMIT - len(output)
                    output.extend(data[: max(0, remaining)])
                    if len(data) > remaining:
                        overflow.set()
                else:
                    process.record_stderr(data)
        except (OSError, ValueError):
            pipe_failed.set()

    def write_grant() -> None:
        try:
            offset = 0
            while offset < len(payload):
                written = process.stdin.write(payload[offset : offset + 4096])
                if not written:
                    raise OSError("upload input pipe closed")
                offset += written
            process.stdin.close()
        except (OSError, ValueError):
            pipe_failed.set()

    try:
        process.mark_stage("UPLOAD_STARTED")
        for target, args in (
            (drain, (process.stdout, True)),
            (drain, (process.stderr, False)),
            (write_grant, ()),
        ):
            thread = threading.Thread(target=target, args=args, daemon=True)
            thread.start()
            threads.append(thread)
        while process.poll() is None:
            if stop_event.is_set():
                reason = "UPLOAD_STOPPED"
                break
            if time.monotonic() >= deadline:
                reason = "UPLOAD_DEADLINE_EXCEEDED"
                break
            if overflow.is_set() or pipe_failed.is_set():
                reason = "UPLOAD_WORKER_IO_FAILED"
                break
            stop_event.wait(min(0.02, max(0, deadline - time.monotonic())))
    except Exception:
        reason = "UPLOAD_SUPERVISION_FAILED"
    finally:
        cleanup_started = time.monotonic()
        process.mark_stage("UPLOAD_CLEANUP")
        exit_result = process.stop(limits.grace_seconds, limits.kill_seconds)
        join_deadline = time.monotonic() + limits.reader_join_seconds
        for thread in threads:
            thread.join(max(0, join_deadline - time.monotonic()))
        cleanup_confirmed = exit_result.cleanup_confirmed and not any(
            thread.is_alive() for thread in threads
        )
        process.close()
    cleanup_elapsed = time.monotonic() - cleanup_started
    stderr_tail = process.stderr_tail
    phases = re.findall(r"(?m)^UPLOAD_STAGE=([A-Z][A-Z0-9_]{0,63})\r?$", stderr_tail)
    last_stage = phases[-1] if phases else exit_result.stage
    if cleanup_confirmed and exit_result.returncode == 0 and not overflow.is_set():
        try:
            receipt = _Receipt.model_validate_json(bytes(output))
            if receipt.bytes_sent is not None and receipt.bytes_sent > grant.size_bytes:
                receipt = None
            elif receipt.status == "NOT_SENT" and receipt.bytes_sent != 0:
                receipt = None
            elif receipt.status == "PUT_ACCEPTED" and (
                receipt.bytes_sent != grant.size_bytes
                or receipt.http_status is None
                or not 200 <= receipt.http_status < 300
            ):
                receipt = None
        except ValidationError:
            receipt = None
    if receipt is None:
        receipt = _Receipt(
            status="UNKNOWN",
            reason_code=(reason if cleanup_confirmed else "UPLOAD_CLEANUP_UNKNOWN"),
            bytes_sent=None,
        )
    _LOGGER.info(
        "upload receipt traceId=%s runId=%s evidenceId=%s placementRevision=%s "
        "status=%s reason=%s stage=%s",
        grant.trace_id,
        grant.run_id,
        grant.evidence_id,
        grant.placement_revision,
        receipt.status,
        receipt.reason_code,
        last_stage,
    )
    return UploadResult(
        **identity,
        **receipt.model_dump(),
        process_id=process.pid,
        returncode=exit_result.returncode,
        signal=exit_result.signal,
        stage=last_stage,
        stderr_tail=stderr_tail,
        cleanup_confirmed=cleanup_confirmed,
        execution_elapsed_seconds=cleanup_started - started_at,
        cleanup_elapsed_seconds=cleanup_elapsed,
    )


def _remaining(deadline: float, network_timeout: float) -> float:
    remaining = deadline - time.monotonic()
    if remaining <= 0:
        raise TimeoutError
    return min(remaining, network_timeout)


def _safe_header(value: str | None, pattern: str) -> str | None:
    return value if value is not None and re.fullmatch(pattern, value) else None


def _stage(value: str) -> None:
    # Only fixed lifecycle codes are emitted; never a request/URL/driver exception.
    print(f"UPLOAD_STAGE={value}", file=sys.stderr, flush=True)


def _put(grant: UploadGrant, source: Path, limits: UploadLimits, deadline: float) -> _Receipt:
    sent = 0
    request_started = False
    connection: http.client.HTTPSConnection | None = None
    try:
        _stage("UPLOAD_PREFLIGHT")
        _remaining(deadline, limits.network_timeout_seconds)
        if grant.size_bytes > limits.max_object_bytes:
            return _Receipt(status="NOT_SENT", reason_code="UPLOAD_OBJECT_TOO_LARGE")
        if grant.expires_at <= datetime.now(UTC):
            return _Receipt(status="NOT_SENT", reason_code="UPLOAD_GRANT_EXPIRED")
        with source.open("rb", buffering=0) as file:
            info = os.fstat(file.fileno())
            if not stat.S_ISREG(info.st_mode) or info.st_size != grant.size_bytes:
                return _Receipt(status="NOT_SENT", reason_code="UPLOAD_SOURCE_SIZE_MISMATCH")
            digest = hashlib.sha256()
            remaining = grant.size_bytes
            _stage("UPLOAD_HASHING")
            while remaining:
                _remaining(deadline, limits.network_timeout_seconds)
                chunk = file.read(min(remaining, limits.chunk_bytes))
                if not chunk:
                    return _Receipt(status="NOT_SENT", reason_code="UPLOAD_SOURCE_SIZE_MISMATCH")
                digest.update(chunk)
                remaining -= len(chunk)
            if file.read(1) or base64.b64encode(digest.digest()).decode() != grant.checksum_base64:
                return _Receipt(status="NOT_SENT", reason_code="UPLOAD_SOURCE_CHECKSUM_MISMATCH")
            file.seek(0)
            context = ssl.create_default_context(cafile=limits.ca_file)
            context.set_alpn_protocols(["http/1.1"])
            parsed = urlsplit(grant.put_url.get_secret_value())
            assert parsed.hostname is not None
            connection = http.client.HTTPSConnection(
                parsed.hostname,
                parsed.port,
                timeout=_remaining(deadline, limits.network_timeout_seconds),
                context=context,
            )
            _stage("UPLOAD_CONNECTING")
            connection.connect()
            if grant.expires_at <= datetime.now(UTC):
                return _Receipt(status="NOT_SENT", reason_code="UPLOAD_GRANT_EXPIRED")
            target = parsed.path or "/"
            if parsed.query:
                target += "?" + parsed.query
            connection.putrequest("PUT", target, skip_accept_encoding=True)
            for key, value in grant.required_headers.items():
                connection.putheader(key, value.get_secret_value())
            assert connection.sock is not None
            connection.sock.settimeout(_remaining(deadline, limits.network_timeout_seconds))
            request_started = True
            _stage("UPLOAD_SENDING")
            connection.endheaders()
            digest = hashlib.sha256()
            while sent < grant.size_bytes:
                connection.sock.settimeout(_remaining(deadline, limits.network_timeout_seconds))
                chunk = file.read(min(grant.size_bytes - sent, limits.chunk_bytes))
                if not chunk:
                    raise OSError("upload source changed")
                digest.update(chunk)
                connection.send(chunk)
                sent += len(chunk)
            if file.read(1) or base64.b64encode(digest.digest()).decode() != grant.checksum_base64:
                return _Receipt(
                    status="UNKNOWN", reason_code="UPLOAD_SOURCE_CHANGED", bytes_sent=sent
                )
            connection.sock.settimeout(_remaining(deadline, limits.network_timeout_seconds))
            _stage("UPLOAD_RESPONSE")
            response = connection.getresponse()
            if len(response.read(_RESPONSE_LIMIT + 1)) > _RESPONSE_LIMIT:
                return _Receipt(
                    status="UNKNOWN", reason_code="UPLOAD_RESPONSE_TOO_LARGE", bytes_sent=sent
                )
            code = response.status
            common: dict[str, Any] = {"http_status": code, "bytes_sent": sent}
            if 200 <= code < 300:
                return _Receipt(
                    status="PUT_ACCEPTED",
                    reason_code="UPLOAD_AWAITING_CONFIRMATION",
                    **common,
                    etag=_safe_header(response.getheader("ETag"), r'"?[a-fA-F0-9-]{1,128}"?'),
                    object_version=_safe_header(
                        response.getheader("x-amz-version-id"), r"[A-Za-z0-9._+/=-]{1,512}"
                    ),
                )
            if 300 <= code < 400:
                return _Receipt(status="UNKNOWN", reason_code="UPLOAD_REDIRECT_REFUSED", **common)
            if code in {408, 409, 412} or code >= 500:
                return _Receipt(
                    status="UNKNOWN", reason_code="UPLOAD_RECONCILIATION_REQUIRED", **common
                )
            return _Receipt(status="REJECTED", reason_code="UPLOAD_HTTP_REJECTED", **common)
    except ssl.SSLCertVerificationError:
        return _Receipt(
            status="UNKNOWN" if request_started else "NOT_SENT",
            reason_code="UPLOAD_TLS_VERIFICATION_FAILED",
            bytes_sent=sent,
        )
    except (OSError, TimeoutError, ValueError, http.client.HTTPException):
        return _Receipt(
            status="UNKNOWN" if request_started else "NOT_SENT",
            reason_code="UPLOAD_IO_FAILED" if request_started else "UPLOAD_PREFLIGHT_FAILED",
            bytes_sent=sent,
        )
    finally:
        if connection is not None:
            connection.close()


def _worker() -> None:
    try:
        raw = sys.stdin.buffer.read(_INPUT_LIMIT + 1)
        if len(raw) > _INPUT_LIMIT:
            raise ValueError("input limit")
        data = json.loads(raw)
        grant = UploadGrant.model_validate(data["grant"])
        limits = UploadLimits.model_validate(data["limits"])
        source = Path(data["source"])
        if not source.is_absolute():
            raise ValueError("source path")
        deadline = float(data["deadline"])
        result = _put(grant, source, limits, deadline)
    except Exception:
        # Do not expose validation inputs, signed URLs, file paths or driver messages.
        result = _Receipt(status="UNKNOWN", reason_code="UPLOAD_WORKER_FAILED", bytes_sent=None)
    sys.stdout.write(result.model_dump_json())
    sys.stdout.flush()


if __name__ == "__main__":
    if sys.argv[1:] != ["--worker"]:
        raise SystemExit("private upload worker")
    _worker()
