"""Opt-in F03 experiment against a new, owned SeaweedFS instance and test prefix.

The signing authority below is an experiment fixture, not a platform implementation.
No existing storage configuration, account, bucket, or data directory is read.
"""

from __future__ import annotations

import argparse
import base64
import hashlib
import http.client
import ipaddress
import json
import os
import secrets
import shutil
import socket
import ssl
import threading
import time
import traceback
from concurrent.futures import ThreadPoolExecutor
from dataclasses import asdict
from datetime import UTC, datetime, timedelta
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from typing import Any
from urllib.parse import parse_qs, urlsplit
from uuid import uuid4

import boto3
from botocore.config import Config
from botocore.exceptions import ClientError
from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.x509.oid import ExtendedKeyUsageOID, NameOID
from pydantic import SecretStr

from camera_inspector.errors import InspectionError
from camera_inspector.processes import ManagedProcess
from camera_inspector.spool import Spool
from camera_inspector.storage import upload_once
from camera_inspector.storage_types import UploadGrant, UploadLimits, UploadResult

WORKSPACE = Path(__file__).resolve().parents[2]


def save(path: Path, value: Any) -> None:
    temporary = path.with_suffix(path.suffix + ".tmp")
    with temporary.open("w", encoding="utf-8") as stream:
        stream.write(json.dumps(value, indent=2, ensure_ascii=False))
        stream.flush()
        os.fsync(stream.fileno())
    os.replace(temporary, path)


def free_ports(count: int) -> list[int]:
    reserved: list[socket.socket] = []
    try:
        while len(reserved) < count:
            probe = socket.socket()
            try:
                probe.bind(("127.0.0.1", 49152 + secrets.randbelow(16384)))
                reserved.append(probe)
            except OSError:
                probe.close()
        return [item.getsockname()[1] for item in reserved]
    finally:
        for item in reserved:
            item.close()


def certificates(root: Path) -> tuple[Path, Path, Path]:
    """Generate an experiment-only CA and an IP/DNS SAN leaf; verification stays on."""
    now = datetime.now(UTC)
    ca_key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    name = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, "F03 isolated test CA")])
    ca = (
        x509.CertificateBuilder()
        .subject_name(name)
        .issuer_name(name)
        .public_key(ca_key.public_key())
        .serial_number(x509.random_serial_number())
        .not_valid_before(now - timedelta(minutes=5))
        .not_valid_after(now + timedelta(days=1))
        .add_extension(x509.BasicConstraints(ca=True, path_length=0), critical=True)
        .add_extension(
            x509.KeyUsage(False, False, False, False, False, True, True, False, False),
            critical=True,
        )
        .sign(ca_key, hashes.SHA256())
    )
    key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
    leaf = (
        x509.CertificateBuilder()
        .subject_name(x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, "localhost")]))
        .issuer_name(ca.subject)
        .public_key(key.public_key())
        .serial_number(x509.random_serial_number())
        .not_valid_before(now - timedelta(minutes=5))
        .not_valid_after(now + timedelta(days=1))
        .add_extension(x509.BasicConstraints(ca=False, path_length=None), critical=True)
        .add_extension(
            x509.SubjectAlternativeName(
                [x509.IPAddress(ipaddress.ip_address("127.0.0.1")), x509.DNSName("localhost")]
            ),
            critical=False,
        )
        .add_extension(x509.ExtendedKeyUsage([ExtendedKeyUsageOID.SERVER_AUTH]), critical=False)
        .sign(ca_key, hashes.SHA256())
    )
    ca_path, cert_path, key_path = root / "ca.pem", root / "server.pem", root / "server.key"
    ca_path.write_bytes(ca.public_bytes(serialization.Encoding.PEM))
    cert_path.write_bytes(leaf.public_bytes(serialization.Encoding.PEM))
    key_path.write_bytes(
        key.private_bytes(
            serialization.Encoding.PEM,
            serialization.PrivateFormat.PKCS8,
            serialization.NoEncryption(),
        )
    )
    return ca_path, cert_path, key_path


class OwnedCommand:
    def __init__(self, args: list[str], root: Path) -> None:
        self.process = ManagedProcess(args, cwd=root)
        self.process.mark_stage("F03_ISOLATED_STORAGE")
        self.output = bytearray()
        self.closed = False
        self.threads = [
            threading.Thread(target=self.drain, args=(stream,), daemon=True)
            for stream in (self.process.stdout, self.process.stderr)
        ]
        for thread in self.threads:
            thread.start()

    def drain(self, stream: Any) -> None:
        try:
            while data := stream.read(8192):
                self.process.record_stderr(data)
                self.output.extend(data[: max(0, 65536 - len(self.output))])
        except (OSError, ValueError):
            return

    def close(self) -> dict[str, Any]:
        result = self.process.stop(grace_seconds=0.5, kill_seconds=3)
        for thread in self.threads:
            thread.join(2)
        # Server diagnostics may include request paths; publish no stderr or signed URLs.
        record = {key: value for key, value in asdict(result).items() if key != "stderr_tail"}
        record["pid"] = self.process.pid
        record["pipe_threads_stopped"] = all(not thread.is_alive() for thread in self.threads)
        self.process.close()
        self.closed = True
        return record


class IsolatedStorage:
    def __init__(self, root: Path, weed: Path) -> None:
        self.root = root
        self.children: list[OwnedCommand] = []
        self.cleanup: list[dict[str, Any]] = []
        self.run_id = root.name.removeprefix("storage-")
        self.bucket = "inspection-f03-" + self.run_id[:16]
        self.prefix = "p0-f03/" + self.run_id + "/"
        self.ports = dict(
            zip(
                (
                    "master",
                    "master_grpc",
                    "volume",
                    "volume_grpc",
                    "filer",
                    "filer_grpc",
                    "s3_http",
                    "s3_https",
                    "s3_grpc",
                    "metrics",
                ),
                free_ports(10),
                strict=True,
            )
        )
        self.ca_path, self.cert_path, self.key_path = certificates(root)
        cert, key = self.cert_path, self.key_path
        self.endpoint = f"https://127.0.0.1:{self.ports['s3_https']}"
        self.context = ssl.create_default_context(cafile=str(self.ca_path))
        access, secret = secrets.token_hex(16), secrets.token_urlsafe(32)
        self._signer_credentials = (access, secret)
        limited_access, limited_secret = secrets.token_hex(16), secrets.token_urlsafe(32)
        config = root / "config"
        config.mkdir()
        for name in ("data", "master-data", "filer-data", "logs"):
            (root / name).mkdir()
        (config / "filer.toml").write_text(
            "[leveldb2]\nenabled = true\ndir = "
            + json.dumps((root / "filer-data").as_posix())
            + "\n",
            encoding="utf-8",
        )

        def identity(name: str, ak: str, sk: str, actions: list[str]) -> dict[str, Any]:
            return {
                "name": name,
                "credentials": [{"accessKey": ak, "secretKey": sk}],
                "actions": actions,
            }

        save(
            config / "s3.json",
            {
                "identities": [
                    identity("f03-admin", access, secret, ["Admin"]),
                    identity(
                        "f03-prefix",
                        limited_access,
                        limited_secret,
                        [
                            f"Write:{self.bucket}/{self.prefix}*",
                            f"Read:{self.bucket}/{self.prefix}*",
                        ],
                    ),
                ]
            },
        )
        self.client = self.make_client(access, secret)
        self.limited_client = self.make_client(limited_access, limited_secret)
        args = [
            str(weed),
            "-config_dir=" + str(config),
            "-logdir=" + str(root / "logs"),
            "-log_max_files=2",
            "-log_max_size_mb=8",
            "server",
            "-ip=127.0.0.1",
            "-ip.bind=127.0.0.1",
            "-dir=" + str(root / "data"),
            "-master.dir=" + str(root / "master-data"),
            "-master.telemetry=false",
            "-master.volumeSizeLimitMB=64",
            "-volume.max=4",
            "-volume.fileSizeLimitMB=64",
            "-filer",
            "-filer.concurrentFileUploadLimit=8",
            "-filer.concurrentUploadLimitMB=64",
            "-s3",
            "-s3.ip.bind=127.0.0.1",
            "-s3.config=" + str(config / "s3.json"),
            "-s3.cert.file=" + str(cert),
            "-s3.key.file=" + str(key),
            "-s3.cacert.file=" + str(self.ca_path),
            "-s3.port.iceberg=0",
            "-s3.port.lance=0",
            "-s3.concurrentFileUploadLimit=8",
            "-s3.concurrentUploadLimitMB=64",
            "-s3.readerCacheSizeMB=16",
            "-s3.cacheCapacityMB=0",
            "-s3.autoCreateBucket=false",
            "-metricsIp=127.0.0.1",
        ]
        flags = {
            "master": "master.port",
            "master_grpc": "master.port.grpc",
            "volume": "volume.port",
            "volume_grpc": "volume.port.grpc",
            "filer": "filer.port",
            "filer_grpc": "filer.port.grpc",
            "s3_http": "s3.port",
            "s3_https": "s3.port.https",
            "s3_grpc": "s3.port.grpc",
            "metrics": "metricsPort",
        }
        args.extend(f"-{flag}={self.ports[name]}" for name, flag in flags.items())
        try:
            server = OwnedCommand(args, root)
            self.children.append(server)
            deadline = time.monotonic() + 45
            while time.monotonic() < deadline:
                if server.process.poll() is not None:
                    # Local diagnostic is retained only under this new experiment directory.
                    (root / "startup-diagnostic.log").write_bytes(server.output)
                    raise RuntimeError("ISOLATED_STORAGE_STARTUP_EXITED")
                try:
                    self.client.list_buckets()
                    self.client.create_bucket(Bucket=self.bucket)
                    break
                except Exception:
                    time.sleep(0.25)
            else:
                (root / "startup-diagnostic.log").write_bytes(server.output)
                raise RuntimeError("ISOLATED_STORAGE_READINESS_TIMEOUT")
            save(
                root / "environment.json",
                {
                    "run_id": self.run_id,
                    "ports": self.ports,
                    "pid": server.process.pid,
                    "weed_binary": str(weed),
                    "weed_sha256": hashlib.sha256(weed.read_bytes()).hexdigest(),
                    "boto3": boto3.__version__,
                    "tls": "native HTTPS; verified fresh CA and IP SAN",
                    "bucket": self.bucket,
                    "managed_prefix": self.prefix,
                    "source_label": "ISOLATED_SYNTHETIC_FIXTURE",
                    "production_configuration_read": False,
                },
            )
            version = OwnedCommand([str(weed), "version"], root)
            self.children.append(version)
            assert version.process.wait(5) == 0
            for reader in version.threads:
                reader.join(1)
            environment_path = root / "environment.json"
            environment = json.loads(environment_path.read_text(encoding="utf-8"))
            environment["weed_version"] = bytes(version.output).decode("utf-8").strip()
            save(environment_path, environment)
        except BaseException:
            self.close()
            raise

    def make_client(self, access: str, secret: str, endpoint: str | None = None) -> Any:
        return boto3.client(
            "s3",
            endpoint_url=endpoint or self.endpoint,
            aws_access_key_id=access,
            aws_secret_access_key=secret,
            region_name="us-east-1",
            verify=str(self.ca_path),
            config=Config(
                signature_version="s3v4",
                s3={"addressing_style": "path"},
                connect_timeout=2,
                read_timeout=5,
                retries={"max_attempts": 0},
            ),
        )

    def signed(
        self,
        key: str,
        data: bytes,
        *,
        client: Any = None,
        seconds: int = 120,
        content_type: str = "application/octet-stream",
    ) -> tuple[str, dict[str, str]]:
        assert key.startswith(self.prefix), "Fixture grants are restricted to the managed prefix"
        checksum = base64.b64encode(hashlib.sha256(data).digest()).decode("ascii")
        headers = {
            "Content-Length": str(len(data)),
            "Content-Type": content_type,
            "If-None-Match": "*",
            "x-amz-checksum-sha256": checksum,
        }
        url = (client or self.client).generate_presigned_url(
            "put_object",
            Params={
                "Bucket": self.bucket,
                "Key": key,
                "ContentLength": len(data),
                "ContentType": headers["Content-Type"],
                "IfNoneMatch": "*",
                "ChecksumSHA256": checksum,
            },
            ExpiresIn=seconds,
            HttpMethod="PUT",
        )
        signed = parse_qs(urlsplit(url).query)["X-Amz-SignedHeaders"][0].split(";")
        assert set(signed) == {name.lower() for name in headers} | {"host"}
        return url, headers

    def grant(
        self,
        key: str,
        data: bytes,
        *,
        client: Any = None,
        seconds: int = 120,
        content_type: str = "application/octet-stream",
    ) -> UploadGrant:
        url, headers = self.signed(
            key, data, client=client, seconds=seconds, content_type=content_type
        )
        query = parse_qs(urlsplit(url).query)
        expiry = datetime.strptime(query["X-Amz-Date"][0], "%Y%m%dT%H%M%SZ").replace(tzinfo=UTC)
        expiry += timedelta(seconds=int(query["X-Amz-Expires"][0]))
        return UploadGrant(
            evidence_id=uuid4().hex,
            run_id=self.run_id,
            assignment_epoch=1,
            placement_revision=1,
            trace_id=uuid4().hex,
            instance_id="isolated-seaweed-448",
            scope_id="f03-managed-prefix",
            bucket=self.bucket,
            object_key=key,
            endpoint_revision=1,
            expires_at=expiry,
            size_bytes=len(data),
            content_type=content_type,
            checksum_base64=headers["x-amz-checksum-sha256"],
            put_url=SecretStr(url),
            required_headers={name: SecretStr(value) for name, value in headers.items()},
        )

    def limits(self, **changes: Any) -> UploadLimits:
        return UploadLimits(
            max_object_bytes=64 * 1024 * 1024,
            ca_file=self.ca_path,
            total_timeout_seconds=10,
            **changes,
        )

    def confirm(
        self, spool: Spool, grant: UploadGrant, *, acknowledge: bool = True
    ) -> dict[str, Any]:
        """Fixture-only authority: real remote bytes and metadata, then durable fixture index."""
        body = self.read(grant.object_key)
        head = self.client.head_object(Bucket=self.bucket, Key=grant.object_key)
        checksum = base64.b64encode(hashlib.sha256(body).digest()).decode("ascii")
        assert len(body) == grant.size_bytes == head["ContentLength"]
        assert checksum == grant.checksum_base64 and head["ContentType"] == grant.content_type
        confirmation = {
            "evidence_id": grant.evidence_id,
            "run_id": grant.run_id,
            "assignment_epoch": grant.assignment_epoch,
            "object_key": grant.object_key,
            "placement_revision": grant.placement_revision,
            "size_bytes": len(body),
            "checksum_base64": checksum,
            "content_type": head["ContentType"],
            "confirmation_id": uuid4().hex,
            "method": "ACTUAL_GET_REHASH_AND_HEAD_METADATA",
        }
        index_path = self.root / ("fixture-index-" + grant.evidence_id + ".json")
        if index_path.exists():
            previous = json.loads(index_path.read_text(encoding="utf-8"))
            assert all(
                value == previous[name]
                for name, value in confirmation.items()
                if name != "confirmation_id"
            )
            confirmation = previous
        else:
            save(index_path, confirmation)
        if not acknowledge:
            return confirmation
        result = spool.confirm(
            grant,
            size_bytes=len(body),
            checksum_base64=checksum,
            content_type=head["ContentType"],
            confirmation_id=confirmation["confirmation_id"],
        )
        assert result.state == "CONFIRMED" and result.local_state == "RELEASED"
        return confirmation

    def put(self, url: str, headers: dict[str, str], data: bytes, method: str = "PUT") -> int:
        parsed = urlsplit(url)
        connection = http.client.HTTPSConnection(
            parsed.hostname, parsed.port, timeout=5, context=self.context
        )
        try:
            connection.request(method, parsed.path + "?" + parsed.query, body=data, headers=headers)
            response = connection.getresponse()
            response.read(16384)
            return response.status
        finally:
            connection.close()

    def read(self, key: str) -> bytes:
        response = self.client.get_object(Bucket=self.bucket, Key=key)
        try:
            return response["Body"].read(64 * 1024 * 1024)
        finally:
            response["Body"].close()

    def exists(self, key: str) -> bool:
        try:
            self.client.head_object(Bucket=self.bucket, Key=key)
            return True
        except ClientError as error:
            if error.response["ResponseMetadata"]["HTTPStatusCode"] == 404:
                return False
            raise

    def close(self) -> None:
        failed = False
        self.client.close()
        self.limited_client.close()
        for child in reversed(self.children):
            if child.closed:
                continue
            try:
                record = child.close()
                self.cleanup.append(record)
                failed |= not record["cleanup_confirmed"] or not record["pipe_threads_stopped"]
            except Exception as error:
                failed = True
                self.cleanup.append({"pid": child.process.pid, "error_type": type(error).__name__})
        save(self.root / "cleanup.json", self.cleanup)
        if failed:
            raise RuntimeError("OWNED_STORAGE_CLEANUP_NOT_CONFIRMED")


def critical_probe(storage: IsolatedStorage) -> dict[str, Any]:
    records: list[dict[str, Any]] = []

    def record(name: str, status: int, passed: bool, **details: Any) -> None:
        records.append({"case": name, "http_status": status, "passed": passed, **details})
        save(
            storage.root / "critical.json",
            {"cases": records, "passed": all(item["passed"] for item in records)},
        )

    original = b"F03 exact original evidence bytes\n" * 16
    key = storage.prefix + "critical/immutable.bin"
    url, headers = storage.signed(key, original)
    status = storage.put(url, headers, original)
    record("correct_put", status, status == 200 and storage.read(key) == original)
    status = storage.put(url, headers, original)
    record("same_grant_replay", status, status == 412 and storage.read(key) == original)
    different = b"z" * len(original)
    url2, headers2 = storage.signed(key, different)
    status = storage.put(url2, headers2, different)
    record("same_key_different_body", status, status == 412 and storage.read(key) == original)
    bad_key = storage.prefix + "critical/wrong-checksum.bin"
    bad_url, bad_headers = storage.signed(bad_key, original)
    status = storage.put(bad_url, bad_headers, different)
    record("server_validates_sha256", status, 400 <= status < 500 and not storage.exists(bad_key))
    for header, value in (
        ("If-None-Match", None),
        ("If-None-Match", '"changed"'),
        ("Content-Type", "text/plain"),
        ("Content-Length", str(len(original) - 1)),
    ):
        bad_key = storage.prefix + "critical/tampered-" + uuid4().hex + ".bin"
        url3, headers3 = storage.signed(bad_key, original)
        if value is None:
            headers3.pop(header)
        else:
            headers3[header] = value
        status = storage.put(url3, headers3, original)
        record(
            "signed_header_" + header.lower() + ("_removed" if value is None else "_changed"),
            status,
            400 <= status < 500 and not storage.exists(bad_key),
        )
    concurrent_key = storage.prefix + "critical/concurrent.bin"
    barrier = threading.Barrier(2)

    def racing_put(body: bytes) -> int:
        signed_url, signed_headers = storage.signed(concurrent_key, body)
        barrier.wait(timeout=3)
        return storage.put(signed_url, signed_headers, body)

    with ThreadPoolExecutor(max_workers=2) as pool:
        futures = [pool.submit(racing_put, body) for body in (original, different)]
        statuses = [future.result(timeout=10) for future in futures]
    record(
        "concurrent_no_overwrite",
        0,
        sorted(statuses) == [200, 412]
        and storage.read(concurrent_key) == (original, different)[statuses.index(200)],
        statuses=statuses,
    )
    truncated_key = storage.prefix + "critical/truncated.bin"
    partial_url, partial_headers = storage.signed(truncated_key, original)
    parsed = urlsplit(partial_url)
    partial = http.client.HTTPSConnection(
        parsed.hostname, parsed.port, timeout=2, context=storage.context
    )
    response_status = None
    try:
        partial.request(
            "PUT", parsed.path + "?" + parsed.query, body=original[:16], headers=partial_headers
        )
        assert partial.sock is not None
        partial.sock.shutdown(socket.SHUT_WR)
        try:
            response = partial.getresponse()
            response_status = response.status
            response.read(16384)
        except (OSError, http.client.HTTPException):
            pass
    finally:
        partial.close()
    time.sleep(0.1)
    record(
        "truncated_body_disconnect",
        response_status or 0,
        not storage.exists(truncated_key),
        observation="Incomplete request has no object; disconnected callers require reconciliation",
    )
    result = {"cases": records, "passed": all(item["passed"] for item in records)}
    save(storage.root / "critical.json", result)
    return result


class FaultProxy:
    """Bounded TLS fault injection; successful writes always go to the real SeaweedFS.

    It never supplies an S3 implementation or converts rejected writes into success.
    The authority signs the proxy Host; forwarding preserves that signed Host.
    """

    def __init__(self, storage: IsolatedStorage) -> None:
        self.storage = storage
        self.port = free_ports(1)[0]
        self.mode = "PASS"
        self.request_seen = threading.Event()
        self.release = threading.Event()
        self.forwarded_statuses: list[int] = []
        self.requests = 0
        self.active_handlers = 0
        self.handler_condition = threading.Condition()
        proxy = self

        class Handler(BaseHTTPRequestHandler):
            protocol_version = "HTTP/1.1"

            def log_message(self, _format: str, *args: Any) -> None:
                return

            def handle(self) -> None:
                with proxy.handler_condition:
                    proxy.active_handlers += 1
                try:
                    super().handle()
                finally:
                    with proxy.handler_condition:
                        proxy.active_handlers -= 1
                        proxy.handler_condition.notify_all()

            def do_PUT(self) -> None:
                self.handle_request()

            def do_HEAD(self) -> None:
                self.handle_request()

            def handle_request(self) -> None:
                self.connection.settimeout(3)
                proxy.requests += 1
                proxy.request_seen.set()
                mode = proxy.mode
                if mode == "STALL":
                    proxy.release.wait(3)
                    self.close_connection = True
                    return
                if mode == "FAIL_ALL" or (self.command == "HEAD" and mode == "HEAD_503"):
                    self.send_response(503)
                    self.send_header("Content-Length", "0")
                    self.end_headers()
                    self.close_connection = True
                    return
                if self.command == "HEAD" and mode == "HEAD_404":
                    self.send_response(404)
                    self.send_header("Content-Length", "0")
                    self.end_headers()
                    return
                length = int(self.headers.get("Content-Length", "0"))
                if length < 0 or length > 64 * 1024 * 1024:
                    self.send_error(413)
                    return
                data = self.rfile.read(length)
                if len(data) != length:
                    self.close_connection = True
                    return
                upstream = http.client.HTTPSConnection(
                    "127.0.0.1",
                    proxy.storage.ports["s3_https"],
                    context=proxy.storage.context,
                    timeout=5,
                )
                try:
                    upstream.request(
                        self.command, self.path, body=data, headers=dict(self.headers.items())
                    )
                    response = upstream.getresponse()
                    body = response.read(65536)
                    proxy.forwarded_statuses.append(response.status)
                    if mode == "DROP_SUCCESS" and 200 <= response.status < 300:
                        self.close_connection = True
                        return
                    self.send_response(response.status)
                    for name, value in response.getheaders():
                        if name.lower() not in {
                            "connection",
                            "transfer-encoding",
                            "content-length",
                        }:
                            self.send_header(name, value)
                    self.send_header("Content-Length", str(len(body)))
                    self.end_headers()
                    if self.command != "HEAD":
                        self.wfile.write(body)
                finally:
                    upstream.close()

        self.server = ThreadingHTTPServer(("127.0.0.1", self.port), Handler)
        self.server.daemon_threads = True
        context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        context.load_cert_chain(storage.cert_path, storage.key_path)
        self.server.socket = context.wrap_socket(self.server.socket, server_side=True)
        self.thread = threading.Thread(
            target=self.server.serve_forever, kwargs={"poll_interval": 0.05}, daemon=True
        )
        self.thread.start()
        self.client = storage.make_client(
            *storage._signer_credentials, endpoint=f"https://127.0.0.1:{self.port}"
        )

    def close(self) -> None:
        self.client.close()
        self.release.set()
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(2)
        assert not self.thread.is_alive()
        with self.handler_condition:
            assert self.handler_condition.wait_for(lambda: self.active_handlers == 0, timeout=4)


def error_status(call: Any) -> int:
    try:
        response = call()
        return int(response["ResponseMetadata"]["HTTPStatusCode"])
    except ClientError as error:
        return int(error.response["ResponseMetadata"]["HTTPStatusCode"])


def upload_record(result: UploadResult) -> dict[str, Any]:
    return result.model_dump(mode="json")


def matrix_probe(storage: IsolatedStorage, media_root: Path) -> dict[str, Any]:
    matrix: dict[str, Any] = {
        "source_label": "ISOLATED_SYNTHETIC_FIXTURE",
        "authority": "EXPERIMENT_FIXTURE_ONLY; no production authorization or index service",
        "cases": {},
    }

    def case(name: str, value: dict[str, Any]) -> None:
        matrix["cases"][name] = value
        save(storage.root / "matrix.json", matrix)
        assert value["passed"], f"{name}_ASSERTION_FAILED"

    proxy: FaultProxy | None = None
    try:
        # Consume actual previously generated F02 evidence, never a camera URL.
        assert media_root.is_relative_to((WORKSPACE / ".run" / "p0-f02").resolve())
        media_report = json.loads(
            (media_root / "M4-long-gop" / "report.json").read_text(encoding="utf-8")
        )
        frame_path = Path(media_report["evidence"][2]["frame_path"]).resolve(strict=True)
        assert frame_path.is_relative_to(media_root)
        png = frame_path.read_bytes()
        assert png.startswith(b"\x89PNG\r\n\x1a\n")
        trust_rejected = False
        hostname_rejected = False
        try:
            with socket.create_connection(
                ("127.0.0.1", storage.ports["s3_https"]), timeout=2
            ) as raw:
                with ssl.create_default_context().wrap_socket(raw, server_hostname="127.0.0.1"):
                    pass
        except ssl.SSLCertVerificationError:
            trust_rejected = True
        try:
            with socket.create_connection(
                ("127.0.0.1", storage.ports["s3_https"]), timeout=2
            ) as raw:
                with storage.context.wrap_socket(raw, server_hostname="wrong.invalid"):
                    pass
        except ssl.SSLCertVerificationError:
            hostname_rejected = True
        original = b"Actual worker TLS upload and independently verified remote object.\n" * 32
        local = storage.root / "spool-success"
        local.mkdir()
        source = local / "evidence.png"
        source.write_bytes(png)
        dns_client = storage.make_client(
            *storage._signer_credentials,
            endpoint=f"https://localhost:{storage.ports['s3_https']}",
        )
        try:
            grant = storage.grant(
                storage.prefix + "worker/success.png",
                png,
                client=dns_client,
                content_type="image/png",
            )
        finally:
            dns_client.close()
        with Spool(local, max_items=2, max_bytes=len(png)) as spool:
            spool.register(grant, source, retain_until=datetime.now(UTC) + timedelta(minutes=5))
            spool.begin(grant)
            result = upload_once(grant, source, storage.limits(), threading.Event())
            spool.record(result)
            retained_after_put = source.exists()
            first_confirmation = storage.confirm(spool, grant, acknowledge=False)
            retained_after_lost_ack = source.exists()
            confirmation = storage.confirm(spool, grant)
            replay = storage.confirm(spool, grant)
            assert confirmation == first_confirmation == replay
            case(
                "S1",
                {
                    "passed": result.status == "PUT_ACCEPTED"
                    and result.cleanup_confirmed
                    and retained_after_put
                    and not source.exists()
                    and trust_rejected
                    and hostname_rejected,
                    "upload": upload_record(result),
                    "untrusted_ca_rejected": trust_rejected,
                    "wrong_hostname_rejected": hostname_rejected,
                    "source_retained_until_remote_verification": retained_after_put,
                    "source_retained_after_lost_confirmation_ack": retained_after_lost_ack,
                    "confirmation_replay_idempotent": confirmation == replay,
                    "confirmation": confirmation,
                    "source": str(frame_path),
                    "size_bytes": len(png),
                    "sha256": hashlib.sha256(png).hexdigest(),
                    "network": "localhost DNS to loopback; external node route not exercised",
                },
            )
        critical = critical_probe(storage)
        # The experimental deployment bound is tested, not inferred from S3's limit.
        maximum = 64 * 1024 * 1024
        boundary_data = bytes(range(256)) * (maximum // 256)
        boundary_root = storage.root / "spool-boundary"
        boundary_root.mkdir()
        boundary_source = boundary_root / "boundary.bin"
        boundary_source.write_bytes(boundary_data)
        boundary_grant = storage.grant(storage.prefix + "boundary/64mib.bin", boundary_data)
        with Spool(boundary_root, max_items=1, max_bytes=maximum) as spool:
            spool.register(
                boundary_grant,
                boundary_source,
                retain_until=datetime.now(UTC) + timedelta(minutes=5),
            )
            spool.begin(boundary_grant)
            boundary_result = upload_once(
                boundary_grant, boundary_source, storage.limits(), threading.Event()
            )
            spool.record(boundary_result)
            assert boundary_result.status == "PUT_ACCEPTED"
            boundary_confirmation = storage.confirm(spool, boundary_grant)
        del boundary_data
        case(
            "S2",
            {
                "passed": critical["passed"],
                "case_count": len(critical["cases"]),
                "evidence": "critical.json",
                "digest": "SHA256 verified by actual server",
                "maximum_single_put_bytes_tested": maximum,
                "boundary_upload": upload_record(boundary_result),
                "boundary_confirmation": boundary_confirmation,
            },
        )

        proxy = FaultProxy(storage)
        proxy.mode = "DROP_SUCCESS"
        local = storage.root / "spool-unknown"
        local.mkdir()
        source = local / "evidence.bin"
        source.write_bytes(original)
        grant = storage.grant(
            storage.prefix + "worker/unknown.bin", original, client=proxy.client, seconds=3
        )
        reservation = {
            "reserved_bytes": len(original),
            "used_bytes": 0,
            "state": "RESERVED",
            "attempts": 0,
            "max_attempts": 3,
        }
        with Spool(local, max_items=2, max_bytes=8192) as spool:
            spool.register(grant, source, retain_until=datetime.now(UTC) + timedelta(minutes=5))
            spool.begin(grant)
            result = upload_once(grant, source, storage.limits(), threading.Event())
            spool.record(result)
            assert (
                result.status == "UNKNOWN" and 200 in proxy.forwarded_statuses and source.exists()
            )
            snapshots = []
            reconciliation_deadline = time.monotonic() + 5
            for mode in ("HEAD_503", "HEAD_404", "HEAD_503"):
                assert reservation["attempts"] < reservation["max_attempts"]
                assert time.monotonic() < reconciliation_deadline
                proxy.mode = mode
                status = error_status(
                    lambda: proxy.client.head_object(Bucket=storage.bucket, Key=grant.object_key)
                )
                reservation["attempts"] += 1
                reservation["state"] = "RECONCILING"
                snapshots.append({"event": mode, "head_status": status, **reservation})
                assert status == int(mode[-3:]) and source.exists()
            reservation["state"] = "NEEDS_ATTENTION"
            snapshots.append({"event": "RECONCILIATION_LIMIT_REACHED", **reservation})
            # No fourth automatic request; persist the unresolved reservation for review.
            save(storage.root / "fixture-unresolved-reservation.json", reservation)
            assert reservation["attempts"] == reservation["max_attempts"]
            remaining = max(0, (grant.expires_at - datetime.now(UTC)).total_seconds())
            time.sleep(min(4, remaining + 0.1))
            assert datetime.now(UTC) >= grant.expires_at and source.exists()
            snapshots.append({"event": "URL_EXPIRED", **reservation})
            try:
                spool.begin(grant)
            except InspectionError as error:
                replay_rejected = error.code
            else:
                raise AssertionError("Unknown upload was automatically replayed")
            # Real object and metadata verified, fixture index persisted before local deletion.
            confirmation = storage.confirm(spool, grant)
            reservation.update(reserved_bytes=0, used_bytes=len(original), state="VERIFIED")
            snapshots.append({"event": "LATE_REMOTE_CONFIRMATION", **reservation})
            case(
                "S3",
                {
                    "passed": not source.exists() and result.cleanup_confirmed,
                    "upload": upload_record(result),
                    "snapshots": snapshots,
                    "automatic_replay_rejected": replay_rejected,
                    "confirmation": confirmation,
                    "faults": "real PUT forwarded; response dropped; HEAD503/404 injected",
                    "reservation_scope": "fixture only; not production remote budget ledger",
                    "reconciliation_deadline_seconds": 5,
                    "automatic_head_attempts": reservation["attempts"],
                },
            )

        # Use existing, user-authorized F02 synthetic media. Never open a camera URL.
        media_path = Path(media_report["evidence"][2]["clip_path"]).resolve(strict=True)
        assert media_path.is_relative_to(media_root.resolve())
        assert 0 < media_path.stat().st_size <= 64 * 1024 * 1024
        media = media_path.read_bytes()
        local = storage.root / "spool-video"
        local.mkdir()
        source = local / "clip.mkv"
        source.write_bytes(media)
        key = storage.prefix + "video/clip.mkv"
        grant = storage.grant(
            key, media, client=storage.limited_client, content_type="video/x-matroska"
        )
        with Spool(local, max_items=1, max_bytes=len(media)) as spool:
            spool.register(grant, source, retain_until=datetime.now(UTC) + timedelta(minutes=5))
            spool.begin(grant)
            result = upload_once(grant, source, storage.limits(), threading.Event())
            spool.record(result)
            assert result.status == "PUT_ACCEPTED"
            storage.confirm(spool, grant)
        response = storage.limited_client.get_object(
            Bucket=storage.bucket, Key=key, Range="bytes=0-1023"
        )
        try:
            ranged = response["Body"].read()
        finally:
            response["Body"].close()
        assert response["ResponseMetadata"]["HTTPStatusCode"] == 206 and ranged == media[:1024]
        readback = storage.read(key)
        assert readback == media
        (storage.root / "verified-video.mkv").write_bytes(readback)
        adjacent = "p0-f03-adjacent/" + storage.run_id + "/sentinel.bin"
        storage.client.put_object(Bucket=storage.bucket, Key=adjacent, Body=b"owned sentinel")
        other_bucket = "inspection-f03-other-" + storage.run_id[:12]
        storage.client.create_bucket(Bucket=other_bucket)
        rejected = {
            "adjacent_prefix_put": error_status(
                lambda: storage.limited_client.put_object(
                    Bucket=storage.bucket, Key=adjacent, Body=b"forbidden"
                )
            ),
            "adjacent_prefix_get": error_status(
                lambda: storage.limited_client.get_object(Bucket=storage.bucket, Key=adjacent)
            ),
            "adjacent_prefix_delete": error_status(
                lambda: storage.limited_client.delete_object(Bucket=storage.bucket, Key=adjacent)
            ),
            "other_bucket_put": error_status(
                lambda: storage.limited_client.put_object(
                    Bucket=other_bucket, Key="forbidden.bin", Body=b"forbidden"
                )
            ),
        }
        url, headers = storage.signed(
            key, media, client=storage.limited_client, content_type="video/x-matroska"
        )
        rejected["put_url_cannot_get"] = storage.put(url, headers, media, method="GET")
        rejected["put_url_cannot_delete"] = storage.put(url, headers, media, method="DELETE")
        assert all(status == 403 for status in rejected.values())
        assert storage.exists(adjacent) and storage.exists(key)
        lifecycle_status = error_status(
            lambda: storage.client.get_bucket_lifecycle_configuration(Bucket=storage.bucket)
        )
        assert lifecycle_status == 404
        assert key.startswith(storage.prefix) and key != adjacent
        deleted = storage.limited_client.delete_object(Bucket=storage.bucket, Key=key)
        assert deleted["ResponseMetadata"]["HTTPStatusCode"] == 204
        case(
            "S4",
            {
                "passed": not storage.exists(key) and storage.exists(adjacent),
                "range_status": 206,
                "upload": upload_record(result),
                "range_bytes": len(ranged),
                "size_bytes": len(media),
                "source": str(media_path),
                "sha256": hashlib.sha256(media).hexdigest(),
                "readback": "verified-video.mkv",
                "prefix_rejections": rejected,
                "delete_status": 204,
                "adjacent_sentinel_retained": True,
                "scope": "temporary prefix Read/Write identity; Write includes DELETE",
                "camera_authorization": "production API NOT_IMPLEMENTED; not tested",
                "lifecycle": "no lifecycle enabled; exact owned object delete only",
                "lifecycle_configuration_status": lifecycle_status,
            },
        )

        observed_at = datetime.now(UTC).isoformat()
        listed = storage.client.list_objects_v2(Bucket=storage.bucket, Prefix=storage.prefix)
        logical_bytes = sum(item["Size"] for item in listed.get("Contents", []))
        assert not listed.get("IsTruncated", False)
        connection = http.client.HTTPConnection("127.0.0.1", storage.ports["metrics"], timeout=3)
        try:
            connection.request("GET", "/metrics")
            response = connection.getresponse()
            metrics = response.read(256 * 1024).decode("utf-8", errors="replace")
            metric_status = response.status
        finally:
            connection.close()
        selected = [
            line
            for line in metrics.splitlines()
            if any(word in line.lower() for word in ("disk", "volume", "free", "capacity"))
        ][:100]
        (storage.root / "metrics-observation.txt").write_text("\n".join(selected), encoding="utf-8")
        case(
            "S5",
            {
                "passed": metric_status == 200 and logical_bytes > 0,
                "status": "LIMITED",
                "observed_at": observed_at,
                "logical_managed_object_bytes": logical_bytes,
                "units": "bytes",
                "scope": "managed test prefix; excludes replicas, garbage, system data",
                "metrics_http_status": metric_status,
                "raw_metric_lines": len(selected),
                "physical_available_bytes": None,
                "effective_writable_volume_bytes": None,
                "physical_freshness_seconds": None,
                "capacity_claim": "no automated placement or physical availability claim",
                "application_reservation": "S3 fixture only; production ledger not implemented",
            },
        )

        proxy.mode = "FAIL_ALL"
        local = storage.root / "spool-failed"
        local.mkdir()
        failed_results = []
        with Spool(local, max_items=2, max_bytes=2 * len(original)) as spool:
            for index in range(2):
                source = local / f"failed-{index}.bin"
                source.write_bytes(original)
                grant = storage.grant(
                    storage.prefix + f"failed/{index}.bin", original, client=proxy.client
                )
                spool.register(grant, source, retain_until=datetime.now(UTC) + timedelta(minutes=1))
                spool.begin(grant)
                result = upload_once(grant, source, storage.limits(), threading.Event())
                spool.record(result)
                failed_results.append(upload_record(result))
                assert result.status == "UNKNOWN" and result.http_status == 503 and source.exists()
            overflow_source = local / "overflow.bin"
            overflow_source.write_bytes(original)
            overflow_grant = storage.grant(storage.prefix + "failed/overflow.bin", original)
            try:
                spool.register(
                    overflow_grant,
                    overflow_source,
                    retain_until=datetime.now(UTC) + timedelta(minutes=1),
                )
            except InspectionError as error:
                overflow_code = error.code
            else:
                raise AssertionError("Spool accepted an unbounded item")
            # This is the producer's rejected artifact, never a registered/unknown upload.
            overflow_source.unlink()
            assert len(spool.entries()) == 2
            retained_bytes = sum(path.stat().st_size for path in local.glob("*.bin"))
            assert retained_bytes == 2 * len(original)
            spool.stop()
        # Simulate low local free space by threshold, without filling the real disk.
        near = storage.root / "spool-nearfull"
        near.mkdir()
        near_source = near / "candidate.bin"
        near_source.write_bytes(original)
        with Spool(
            near, max_items=2, max_bytes=8192, min_free_bytes=shutil.disk_usage(near).free + 1024**3
        ) as spool:
            try:
                spool.register(
                    overflow_grant,
                    near_source,
                    retain_until=datetime.now(UTC) + timedelta(minutes=1),
                )
            except InspectionError as error:
                nearfull_code = error.code
            else:
                raise AssertionError("Spool ignored minimum free-space threshold")
        near_source.unlink()  # Producer rejected before registration; no remote write exists.
        proxy.mode = "STALL"
        stop_results = []
        for mode in ("DEADLINE", "STOP"):
            proxy.request_seen.clear()
            proxy.release.clear()
            local = storage.root / ("spool-" + mode.lower())
            local.mkdir()
            source = local / "evidence.bin"
            source.write_bytes(original)
            grant = storage.grant(
                storage.prefix + f"stop/{mode}.bin", original, client=proxy.client
            )
            stop = threading.Event()
            limits = storage.limits().model_copy(
                update={"total_timeout_seconds": 1.2, "network_timeout_seconds": 5}
            )
            with Spool(local, max_items=1, max_bytes=len(original)) as spool:
                spool.register(grant, source, retain_until=datetime.now(UTC) + timedelta(minutes=1))
                drain_until = datetime.now(UTC) + timedelta(seconds=0.9)
                spool.stop(drain_until=drain_until)
                limits = limits.model_copy(update={"monotonic_deadline": time.monotonic() + 0.9})
                spool.begin(grant)
                if mode == "STOP":

                    def stop_after_request() -> None:
                        if proxy.request_seen.wait(2):
                            time.sleep(0.1)
                            stop.set()

                    timer = threading.Thread(target=stop_after_request, daemon=True)
                    timer.start()
                result = upload_once(grant, source, limits, stop)
                if mode == "STOP":
                    timer.join(2)
                spool.record(result)
                proxy.release.set()
                assert result.status == "UNKNOWN" and result.cleanup_confirmed and source.exists()
                assert proxy.request_seen.is_set(), (
                    "Fault must occur after the request reaches the proxy"
                )
                assert result.execution_elapsed_seconds < 2.5 and result.cleanup_elapsed_seconds < 4
                stop_results.append(upload_record(result))
                try:
                    spool.begin(grant, now=drain_until + timedelta(seconds=1))
                except InspectionError as error:
                    assert error.code == "SPOOL_STOPPED"
                else:
                    raise AssertionError("Supplementary upload outlived stop drain deadline")
        case(
            "S6",
            {
                "passed": all(item["cleanup_confirmed"] for item in failed_results + stop_results),
                "failed_uploads": failed_results,
                "bounded_items": 2,
                "retained_bytes": retained_bytes,
                "max_bytes": 2 * len(original),
                "item_limit_code": overflow_code,
                "nearfull_code": nearfull_code,
                "stop_and_deadline": stop_results,
                "drain_seconds": 0.9,
                "unknown_objects_auto_retried": False,
                "local_unknown_retained": True,
                "physical_disk_filled": False,
                "remote_failure": "503 injected before real server forwarding; no fake success",
            },
        )
        return matrix
    finally:
        if proxy is not None:
            proxy.close()
            save(
                storage.root / "proxy-cleanup.json",
                {
                    "port": proxy.port,
                    "server_thread_stopped": not proxy.thread.is_alive(),
                    "active_handlers": proxy.active_handlers,
                    "requests": proxy.requests,
                    "forwarded_statuses": proxy.forwarded_statuses,
                },
            )


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--weed", type=Path, required=True)
    parser.add_argument("--critical-only", action="store_true")
    parser.add_argument("--media-root", type=Path)
    args = parser.parse_args()
    weed = args.weed.resolve(strict=True)
    root = WORKSPACE / ".run" / "p0-f03" / ("storage-" + uuid4().hex)
    root.mkdir(parents=True, exist_ok=False)
    storage: IsolatedStorage | None = None
    try:
        storage = IsolatedStorage(root, weed)
        if args.critical_only:
            result = critical_probe(storage)
            passed = result["passed"]
        else:
            if args.media_root is None:
                raise ValueError("Full matrix requires --media-root with authorized F02 artifacts")
            matrix = matrix_probe(storage, args.media_root.resolve(strict=True))
            passed = len(matrix["cases"]) == 6 and all(
                value["passed"] for value in matrix["cases"].values()
            )
        print(json.dumps({"evidence_root": str(root), "passed": passed}))
        return 0 if passed else 1
    except Exception as error:
        save(
            root / "failure.json",
            {
                "error_type": type(error).__name__,
                "frames": [
                    {
                        "file": Path(frame.filename).name,
                        "line": frame.lineno,
                        "function": frame.name,
                    }
                    for frame in traceback.extract_tb(error.__traceback__)
                ],
            },
        )
        print(json.dumps({"evidence_root": str(root), "error_type": type(error).__name__}))
        return 1
    finally:
        if storage is not None:
            storage.close()


if __name__ == "__main__":
    raise SystemExit(main())
