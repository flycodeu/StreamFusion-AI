"""Verify the platform's Java SigV4 grant against a fresh HTTPS SeaweedFS fixture."""

import argparse
import base64
import hashlib
import importlib.util
import json
import os
import subprocess
import threading
from datetime import UTC, datetime, timedelta
from pathlib import Path
from urllib.parse import parse_qs, urlsplit
from uuid import uuid4

from pydantic import SecretStr

from camera_inspector.storage import upload_once
from camera_inspector.storage_types import UploadGrant, UploadLimits


def fixture_module(path: Path):  # type: ignore[no-untyped-def]
    spec = importlib.util.spec_from_file_location("verify_storage_fixture", path)
    if spec is None or spec.loader is None:
        raise RuntimeError("Storage fixture unavailable")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def java_string(value: str) -> str:
    return json.dumps(value, ensure_ascii=True)


def signed_url(
    java: Path,
    classes: Path,
    endpoint: str,
    bucket: str,
    key: str,
    access: str,
    secret: str,
    content: bytes,
) -> str:
    checksum = base64.b64encode(hashlib.sha256(content).digest()).decode("ascii")
    headers = (
        'java.util.Map.of("if-none-match","*","content-length",'
        + java_string(str(len(content)))
        + ',"content-type","image/jpeg","x-amz-checksum-sha256",'
        + java_string(checksum)
        + ")"
    )
    source = (
        "import com.streamfusion.platform.storage.s3.S3Signature;\n"
        "import java.time.Instant;\n"
        'System.out.println("PRESIGNED:"+S3Signature.presignedUrl('
        + ",".join(
            java_string(item) for item in (endpoint, bucket, key, "us-east-1", access, secret)
        )
        + ",600,"
        + headers
        + ",Instant.now().truncatedTo(java.time.temporal.ChronoUnit.SECONDS)));\n/exit\n"
    )
    result = subprocess.run(
        [str(java), "--class-path", str(classes), "--feedback", "silent"],
        input=source,
        text=True,
        capture_output=True,
        timeout=30,
        check=True,
    )
    for line in result.stdout.splitlines():
        if "PRESIGNED:" in line:
            return line.split("PRESIGNED:", 1)[1].strip()
    raise RuntimeError("Java presign result missing")


def main() -> int:
    parser = argparse.ArgumentParser()
    parser.add_argument("--weed", type=Path, required=True)
    parser.add_argument("--java-home", type=Path, required=True)
    args = parser.parse_args()
    workspace = Path(__file__).resolve().parents[2]
    root = workspace / ".run" / "p0-f03" / ("platform-presign-" + uuid4().hex)
    root.mkdir(parents=True)
    fixture = fixture_module(Path(__file__).with_name("verify-storage.py"))
    storage = fixture.IsolatedStorage(root, args.weed.resolve())
    try:
        content = b"\xff\xd8" + bytes(range(256)) * 4 + b"\xff\xd9"
        source = root / "java-presign.jpg"
        source.write_bytes(content)
        key = storage.prefix + "java-presign/1.jpg"
        access, secret = storage._signer_credentials  # Isolated ephemeral fixture only.
        url = signed_url(
            args.java_home / "bin" / "jshell.exe",
            workspace / "platform-api" / "target" / "classes",
            storage.endpoint,
            storage.bucket,
            key,
            access,
            secret,
            content,
        )
        query = parse_qs(urlsplit(url).query)
        signed_at = datetime.strptime(query["X-Amz-Date"][0], "%Y%m%dT%H%M%SZ").replace(tzinfo=UTC)
        grant = UploadGrant(
            evidence_id="java-presign",
            run_id="java-presign-run",
            assignment_epoch=1,
            placement_revision=1,
            trace_id="java-presign-trace",
            instance_id="isolated",
            scope_id="isolated",
            bucket=storage.bucket,
            object_key=key,
            endpoint_revision=1,
            expires_at=signed_at + timedelta(seconds=600),
            size_bytes=len(content),
            content_type="image/jpeg",
            checksum_base64=base64.b64encode(hashlib.sha256(content).digest()).decode("ascii"),
            put_url=SecretStr(url),
            required_headers={
                name: SecretStr(value)
                for name, value in {
                    "if-none-match": "*",
                    "content-length": str(len(content)),
                    "content-type": "image/jpeg",
                    "x-amz-checksum-sha256": base64.b64encode(
                        hashlib.sha256(content).digest()
                    ).decode("ascii"),
                }.items()
            },
        )
        result = upload_once(
            grant,
            source.resolve(),
            UploadLimits(max_object_bytes=64 * 1024**2, ca_file=storage.ca_path),
            threading.Event(),
        )
        remote = storage.client.get_object(Bucket=storage.bucket, Key=key)["Body"].read()
        if result.status != "PUT_ACCEPTED" or remote != content or not result.cleanup_confirmed:
            raise RuntimeError("Java presign upload verification failed")
        truststore = root / "truststore.p12"
        subprocess.run(
            [
                str(args.java_home / "bin" / "keytool.exe"),
                "-importcert",
                "-noprompt",
                "-alias",
                "isolated-seaweedfs",
                "-file",
                str(storage.ca_path),
                "-keystore",
                str(truststore),
                "-storetype",
                "PKCS12",
                "-storepass",
                "inspection-test-only",
            ],
            capture_output=True,
            text=True,
            timeout=30,
            check=True,
        )
        environment = os.environ.copy()
        environment.update(
            {
                "JAVA_HOME": str(args.java_home),
                "JAVA_TOOL_OPTIONS": (
                    f'-Djavax.net.ssl.trustStore="{truststore}" '
                    "-Djavax.net.ssl.trustStorePassword=inspection-test-only"
                ),
                "SF_STORAGE_LIVE_ENDPOINT": storage.endpoint,
                "SF_STORAGE_LIVE_BUCKET": storage.bucket,
                "SF_STORAGE_LIVE_PREFIX": storage.prefix,
                "SF_STORAGE_LIVE_ACCESS": access,
                "SF_STORAGE_LIVE_SECRET": secret,
            }
        )
        platform = subprocess.run(
            [
                str(workspace / "platform-api" / "mvnw.cmd"),
                "-q",
                "-Dtest=StorageWriteProbeLiveTest",
                "-Dspotless.check.skip=true",
                "test",
            ],
            cwd=workspace / "platform-api",
            env=environment,
            capture_output=True,
            text=True,
            timeout=180,
        )
        if platform.returncode != 0:
            raise RuntimeError(
                "Platform live write probe failed: "
                + platform.stdout[-2500:]
                + platform.stderr[-2500:]
            )
        (root / "result.json").write_text(
            json.dumps(
                {
                    "java_presign_accepted": True,
                    "python_grant_validated": True,
                    "remote_sha256": hashlib.sha256(remote).hexdigest(),
                    "bytes": len(remote),
                    "platform_write_probe_passed": True,
                    "production_configuration_read": False,
                },
                indent=2,
            ),
            encoding="utf-8",
        )
        print(root / "result.json")
        return 0
    finally:
        storage.close()


if __name__ == "__main__":
    raise SystemExit(main())
