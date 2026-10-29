"""Bounded upload behavior; real storage capability is verified separately in F03."""

import base64
import hashlib
import json
import os
import ssl
import sys
import threading
import time
from datetime import UTC, datetime, timedelta

import pytest
from pydantic import ValidationError

from camera_inspector import storage
from camera_inspector.processes import ManagedProcess, ProcessExit
from camera_inspector.storage_types import UploadGrant, UploadLimits


def grant_for(data: bytes = b"evidence", **changes) -> UploadGrant:
    checksum = base64.b64encode(hashlib.sha256(data).digest()).decode()
    expiry = changes.get("expires_at", datetime.now(UTC) + timedelta(minutes=5))
    signed_at = (expiry - timedelta(minutes=5)).strftime("%Y%m%dT%H%M%SZ")
    values = dict(
        evidence_id="evidence-1",
        run_id="run-1",
        assignment_epoch=3,
        placement_revision=2,
        trace_id="trace-1",
        instance_id="storage-1",
        scope_id="disk-1",
        bucket="evidence",
        object_key="tests/run-1/image.png",
        endpoint_revision=1,
        expires_at=datetime.now(UTC) + timedelta(minutes=5),
        size_bytes=len(data),
        content_type="image/png",
        checksum_base64=checksum,
        put_url=(
            "https://localhost:9443/evidence/tests/run-1/image.png?"
            "X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-SignedHeaders="
            "content-length%3Bcontent-type%3Bhost%3Bif-none-match%3Bx-amz-checksum-sha256"
            f"&X-Amz-Date={signed_at}&X-Amz-Expires=300&X-Amz-Signature=private-signed-value"
        ),
        required_headers={
            "If-None-Match": "*",
            "Content-Length": str(len(data)),
            "Content-Type": "image/png",
            "x-amz-checksum-sha256": checksum,
        },
    )
    values.update(changes)
    return UploadGrant(**values)


def limits(**changes) -> UploadLimits:
    return UploadLimits(
        max_object_bytes=1024 * 1024, external_supervision=os.name != "nt", **changes
    )


def test_single_put_limit_cannot_exceed_verified_64_mib() -> None:
    assert UploadLimits(max_object_bytes=64 * 1024**2).max_object_bytes == 64 * 1024**2
    with pytest.raises(ValidationError):
        UploadLimits(max_object_bytes=64 * 1024**2 + 1)


class FakeSocket:
    def settimeout(self, value):
        assert 0 < value <= 60


class FakeResponse:
    def __init__(self, status=200, body=b""):
        self.status = status
        self.body = body

    def read(self, count):
        return self.body[:count]

    def getheader(self, key):
        return '"0123456789abcdef0123456789abcdef"' if key == "ETag" else None


class FakeConnection:
    def __init__(self, status=200, body=b"", error=None):
        self.sock = FakeSocket()
        self.status = status
        self.body = body
        self.error = error
        self.headers = {}
        self.chunks = []
        self.requests = []
        self.closed = False

    def connect(self):
        if isinstance(self.error, ssl.SSLCertVerificationError):
            raise self.error

    def putrequest(self, *args, **kwargs):
        self.requests.append(args)

    def putheader(self, key, value):
        self.headers[key.lower()] = value

    def endheaders(self):
        pass

    def send(self, data):
        self.chunks.append(data)

    def getresponse(self):
        if self.error:
            raise self.error
        return FakeResponse(self.status, self.body)

    def close(self):
        self.closed = True


def invoke(monkeypatch, tmp_path, data=b"evidence", connection=None, **limit_changes):
    path = tmp_path / "evidence.png"
    path.write_bytes(data)
    connection = connection or FakeConnection()
    monkeypatch.setattr(storage.http.client, "HTTPSConnection", lambda *a, **kw: connection)
    result = storage._put(grant_for(data), path, limits(**limit_changes), time.monotonic() + 5)
    return result, connection, path


def test_streaming_headers_and_accepted_receipt_preserve_file(monkeypatch, tmp_path):
    data = bytes(range(256)) * 270
    result, connection, path = invoke(monkeypatch, tmp_path, data, chunk_bytes=4096)
    assert result.status == "PUT_ACCEPTED"
    assert result.reason_code == "UPLOAD_AWAITING_CONFIRMATION"
    assert result.bytes_sent == len(data)
    assert b"".join(connection.chunks) == data
    assert max(map(len, connection.chunks)) == 4096
    assert connection.headers["content-length"] == str(len(data))
    assert connection.headers["if-none-match"] == "*"
    assert len(connection.requests) == 1
    assert path.read_bytes() == data
    assert connection.closed


@pytest.mark.parametrize(
    ("code", "status", "reason"),
    [
        (403, "REJECTED", "UPLOAD_HTTP_REJECTED"),
        (307, "UNKNOWN", "UPLOAD_REDIRECT_REFUSED"),
        (409, "UNKNOWN", "UPLOAD_RECONCILIATION_REQUIRED"),
        (412, "UNKNOWN", "UPLOAD_RECONCILIATION_REQUIRED"),
        (500, "UNKNOWN", "UPLOAD_RECONCILIATION_REQUIRED"),
    ],
)
def test_http_responses_never_retry_or_delete(monkeypatch, tmp_path, code, status, reason):
    result, connection, path = invoke(monkeypatch, tmp_path, connection=FakeConnection(code))
    assert (result.status, result.reason_code) == (status, reason)
    assert len(connection.requests) == 1
    assert path.is_file()


def test_lost_response_and_oversize_response_are_unknown(monkeypatch, tmp_path):
    for connection in (
        FakeConnection(error=ConnectionResetError("private-signed-value")),
        FakeConnection(body=b"x" * 20000),
    ):
        result, _, path = invoke(monkeypatch, tmp_path, connection=connection)
        assert result.status == "UNKNOWN"
        assert "private-signed-value" not in result.model_dump_json()
        assert len(connection.requests) == 1
        assert path.is_file()


def test_tls_failure_is_not_sent(monkeypatch, tmp_path):
    result, connection, path = invoke(
        monkeypatch, tmp_path, connection=FakeConnection(error=ssl.SSLCertVerificationError())
    )
    assert result.status == "NOT_SENT"
    assert result.reason_code == "UPLOAD_TLS_VERIFICATION_FAILED"
    assert not connection.requests
    assert path.is_file()


def test_bad_local_checksum_is_rejected_before_network(monkeypatch, tmp_path):
    path = tmp_path / "evidence.png"
    path.write_bytes(b"replaced")
    monkeypatch.setattr(
        storage.http.client, "HTTPSConnection", lambda *a, **k: pytest.fail("network")
    )
    result = storage._put(grant_for(), path, limits(), time.monotonic() + 5)
    assert (result.status, result.reason_code) == ("NOT_SENT", "UPLOAD_SOURCE_CHECKSUM_MISMATCH")


def test_expired_and_oversize_grants_never_open_network(monkeypatch, tmp_path):
    monkeypatch.setattr(
        storage.http.client, "HTTPSConnection", lambda *a, **k: pytest.fail("network")
    )
    expired = grant_for(expires_at=datetime.now(UTC) - timedelta(seconds=1))
    path = tmp_path / "missing"
    result = storage._put(expired, path, limits(), time.monotonic() + 5)
    assert result.reason_code == "UPLOAD_GRANT_EXPIRED"
    result = storage._put(grant_for(), path, UploadLimits(max_object_bytes=1), time.monotonic() + 5)
    assert result.reason_code == "UPLOAD_OBJECT_TOO_LARGE"


@pytest.mark.parametrize(
    "url",
    [
        "http://localhost/a",
        "https://user:secret@localhost/a",
        "https://localhost/a#fragment",
        "https://localhost:\n443/a",
    ],
)
def test_unverified_or_malformed_urls_hide_inputs(url):
    with pytest.raises(ValidationError) as error:
        grant_for(put_url=url)
    assert url not in str(error.value)


def test_required_headers_and_signed_constraints_cannot_be_removed_or_changed():
    original = grant_for()
    for name, value in (
        ("Content-Type", "video/mp4"),
        ("If-None-Match", "other"),
        ("Authorization", "long-term-secret"),
        ("Host", "other-host"),
    ):
        changed = {key: item.get_secret_value() for key, item in original.required_headers.items()}
        changed[name] = value
        with pytest.raises(ValidationError):
            grant_for(required_headers=changed)
    with pytest.raises(ValidationError):
        grant_for(put_url=original.put_url.get_secret_value().replace("%3Bif-none-match", ""))
    assert "private-signed-value" not in repr(original)
    assert "private-signed-value" not in original.model_dump_json()


@pytest.mark.parametrize(
    "replacement",
    [
        "/different/image.png",
        "/evidence/../image.png",
        "/evidence/tests/run-1/%2569mage.png",
        "/evidence/tests/run-1/%69mage.png",
    ],
)
def test_exact_key_and_canonical_path_are_bound(replacement):
    url = grant_for().put_url.get_secret_value()
    with pytest.raises(ValidationError, match="exact path-style"):
        grant_for(put_url=url.replace("/evidence/tests/run-1/image.png", replacement))


def test_expiry_is_bound_to_signature_and_short_ttl():
    original = grant_for()
    for changed in (
        original.put_url.get_secret_value().replace("X-Amz-Expires=300", "X-Amz-Expires=901"),
        original.put_url.get_secret_value().replace("X-Amz-Expires=300", "X-Amz-Expires=0"),
        original.put_url.get_secret_value().replace("X-Amz-Expires=300", "X-Amz-Expires=600"),
    ):
        with pytest.raises(ValidationError, match="bounded signed expiry"):
            grant_for(put_url=changed)
    assert original.expires_at.microsecond == 0


def test_signed_expiry_overflow_is_a_validation_error():
    original = grant_for()
    query = original.put_url.get_secret_value()
    signed_at = query.split("X-Amz-Date=", 1)[1].split("&", 1)[0]
    with pytest.raises(ValidationError, match="bounded signed expiry"):
        grant_for(put_url=query.replace(signed_at, "99991231T235959Z"))


def test_file_change_after_preflight_stays_unknown(monkeypatch, tmp_path):
    path = tmp_path / "evidence.png"
    path.write_bytes(b"evidence")
    connection = FakeConnection()
    connection.endheaders = lambda: path.write_bytes(b"replaced")
    monkeypatch.setattr(storage.http.client, "HTTPSConnection", lambda *a, **kw: connection)
    result = storage._put(grant_for(), path, limits(), time.monotonic() + 5)
    assert result.status == "UNKNOWN" and result.reason_code == "UPLOAD_SOURCE_CHANGED"
    assert len(connection.requests) == 1 and path.read_bytes() == b"replaced"


def test_exception_after_request_started_can_never_claim_not_sent(monkeypatch, tmp_path):
    result, connection, path = invoke(
        monkeypatch, tmp_path, connection=FakeConnection(error=TimeoutError("secret detail"))
    )
    assert result.status == "UNKNOWN" and result.bytes_sent == 8
    assert "secret" not in result.model_dump_json()
    assert path.is_file() and connection.closed


def test_model_copy_does_not_bypass_parent_validation(tmp_path):
    grant = grant_for().model_copy(update={"size_bytes": 100})
    with pytest.raises(ValidationError):
        storage.upload_once(grant, tmp_path / "unused", limits(), threading.Event())


def test_real_child_reports_not_sent_and_keeps_source(tmp_path):
    path = tmp_path / "evidence.png"
    path.write_bytes(b"replaced")
    result = storage.upload_once(
        grant_for(), path, limits(total_timeout_seconds=5), threading.Event()
    )
    assert result.status == "NOT_SENT"
    assert result.reason_code == "UPLOAD_SOURCE_CHECKSUM_MISMATCH"
    assert result.cleanup_confirmed
    assert result.returncode == 0
    assert result.stage == "UPLOAD_HASHING"
    assert "UPLOAD_STAGE=UPLOAD_HASHING" in result.stderr_tail
    assert "private-signed-value" not in result.stderr_tail
    assert path.read_bytes() == b"replaced"


def test_stop_before_start_allocates_nothing(monkeypatch, tmp_path):
    monkeypatch.setattr(storage, "ManagedProcess", lambda *a, **k: pytest.fail("started"))
    stop = threading.Event()
    stop.set()
    result = storage.upload_once(grant_for(), tmp_path / "unused", limits(), stop)
    assert result.status == "NOT_SENT" and result.process_id is None
    assert result.cleanup_confirmed


@pytest.mark.parametrize("blocked_operation", ["DNS", "FILE"])
def test_total_deadline_kills_real_blocked_child(monkeypatch, tmp_path, blocked_operation):
    processes = []
    code = (
        "import socket,time,sys; from pathlib import Path; "
        "from camera_inspector.storage import _worker; "
        'exec("def blocked(*args,**kwargs):\\n '
        f"print('BLOCKED_{blocked_operation}',file=sys.stderr,flush=True); time.sleep(60)\"); "
        + ("socket.getaddrinfo=blocked; " if blocked_operation == "DNS" else "Path.open=blocked; ")
        + "_worker()"
    )

    def start(*args, **kwargs):
        process = ManagedProcess([sys.executable, "-u", "-c", code], **kwargs)
        processes.append(process)
        return process

    monkeypatch.setattr(storage, "ManagedProcess", start)
    path = tmp_path / "evidence.png"
    path.write_bytes(b"evidence")
    before = time.monotonic()
    result = storage.upload_once(
        grant_for(), path, limits(total_timeout_seconds=1.5), threading.Event()
    )
    assert time.monotonic() - before < 4
    assert f"BLOCKED_{blocked_operation}" in processes[0].stderr_tail
    assert result.status == "UNKNOWN"
    assert result.reason_code == "UPLOAD_DEADLINE_EXCEEDED"
    assert result.bytes_sent is None
    assert result.cleanup_confirmed and processes[0].poll() is not None
    assert path.is_file()


def test_stop_during_blocking_child_is_bounded(monkeypatch, tmp_path):
    processes = []

    def start(*args, **kwargs):
        process = ManagedProcess(
            [sys.executable, "-u", "-c", "import time; time.sleep(60)"], **kwargs
        )
        processes.append(process)
        return process

    monkeypatch.setattr(storage, "ManagedProcess", start)
    stop = threading.Event()
    timer = threading.Timer(0.3, stop.set)
    timer.start()
    try:
        before = time.monotonic()
        result = storage.upload_once(grant_for(), tmp_path / "unused", limits(), stop)
        assert time.monotonic() - before < 3
        assert result.status == "UNKNOWN" and result.reason_code == "UPLOAD_STOPPED"
        assert result.cleanup_confirmed
    finally:
        timer.cancel()
    assert processes[0].poll() is not None


def test_thread_start_failure_still_cleans_started_process(monkeypatch, tmp_path):
    original = threading.Thread.start
    count = 0

    def start(thread):
        nonlocal count
        count += 1
        if count == 2:
            raise RuntimeError("thread unavailable")
        original(thread)

    monkeypatch.setattr(threading.Thread, "start", start)
    result = storage.upload_once(grant_for(), tmp_path / "unused", limits(), threading.Event())
    assert result.status == "UNKNOWN" and result.reason_code == "UPLOAD_SUPERVISION_FAILED"
    assert result.cleanup_confirmed


def test_unconfirmed_cleanup_cannot_become_not_sent(monkeypatch, tmp_path):
    def start(*args, **kwargs):
        process = ManagedProcess(*args, **kwargs)
        original = process.stop

        def stop(*args, **kwargs):
            observed = original(*args, **kwargs)
            return ProcessExit(observed.returncode, observed.signal, observed.stage, "", False)

        process.stop = stop
        return process

    monkeypatch.setattr(storage, "ManagedProcess", start)
    result = storage.upload_once(grant_for(), tmp_path / "missing", limits(), threading.Event())
    assert result.status == "UNKNOWN" and result.reason_code == "UPLOAD_CLEANUP_UNKNOWN"
    assert not result.cleanup_confirmed


@pytest.mark.parametrize(
    "receipt",
    [
        {"status": "PUT_ACCEPTED", "http_status": 200, "bytes_sent": 7},
        {"status": "PUT_ACCEPTED", "http_status": 403, "bytes_sent": 8},
        {"status": "NOT_SENT", "bytes_sent": 8},
    ],
)
def test_contradictory_child_receipts_are_unknown(monkeypatch, tmp_path, receipt):
    receipt["reason_code"] = "TEST_RECEIPT"
    code = f"print({json.dumps(json.dumps(receipt))})"

    def start(*args, **kwargs):
        return ManagedProcess([sys.executable, "-u", "-c", code], **kwargs)

    monkeypatch.setattr(storage, "ManagedProcess", start)
    result = storage.upload_once(grant_for(), tmp_path / "unused", limits(), threading.Event())
    assert result.status == "UNKNOWN" and result.bytes_sent is None
    assert result.cleanup_confirmed


def test_wire_has_secrets_only_on_stdin_and_no_long_lived_credentials(tmp_path, monkeypatch):
    monkeypatch.setenv("AWS_SECRET_ACCESS_KEY", "long-lived-secret")
    seen = []
    code = (
        "import json,sys,os; data=json.load(sys.stdin); "
        "assert 'AWS_SECRET_ACCESS_KEY' not in os.environ; "
        "assert 'private-signed-value' in data['grant']['put_url']; "
        "print(json.dumps({'status':'NOT_SENT','reason_code':'TEST_NO_NETWORK'}))"
    )

    def start(args, **kwargs):
        assert all("private-signed-value" not in item for item in args)
        seen.append(args)
        return ManagedProcess([sys.executable, "-u", "-c", code], **kwargs)

    monkeypatch.setattr(storage, "ManagedProcess", start)
    result = storage.upload_once(grant_for(), tmp_path / "unused", limits(), threading.Event())
    assert result.reason_code == "TEST_NO_NETWORK" and result.cleanup_confirmed
    assert seen
    assert "private-signed-value" not in json.dumps(result.model_dump())
