"""Single-object upload grants; these carry no storage account credentials."""

import base64
import binascii
from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import Literal, Self
from urllib.parse import parse_qs, quote, unquote, urlsplit

from pydantic import (
    AwareDatetime,
    BaseModel,
    ConfigDict,
    Field,
    SecretStr,
    field_validator,
    model_validator,
)

from camera_inspector.types import Identifier

UPLOAD_AUTH_MAX_SECONDS = 900


class UploadGrant(BaseModel):
    model_config = ConfigDict(
        extra="forbid", frozen=True, hide_input_in_errors=True, allow_inf_nan=False
    )

    evidence_id: Identifier
    run_id: Identifier
    assignment_epoch: int = Field(strict=True, ge=1)
    placement_revision: int = Field(strict=True, ge=1)
    trace_id: Identifier
    instance_id: Identifier
    scope_id: Identifier
    bucket: str = Field(min_length=1, max_length=255)
    object_key: str = Field(min_length=1, max_length=1024)
    endpoint_revision: int = Field(strict=True, ge=1)
    expires_at: AwareDatetime
    size_bytes: int = Field(strict=True, ge=1)
    content_type: str = Field(min_length=1, max_length=128)
    checksum_algorithm: Literal["SHA256"] = "SHA256"
    checksum_base64: str
    put_url: SecretStr
    required_headers: dict[str, SecretStr] = Field(repr=False)

    @field_validator("put_url")
    @classmethod
    def https_url(cls, value: SecretStr) -> SecretStr:
        raw = value.get_secret_value()
        if len(raw) > 16384 or any(ord(char) < 33 or ord(char) > 126 for char in raw):
            raise ValueError("invalid upload URL")
        try:
            parsed = urlsplit(raw)
            port = parsed.port
        except ValueError:
            raise ValueError("invalid upload URL") from None
        if (
            parsed.scheme != "https"
            or not parsed.hostname
            or parsed.username is not None
            or parsed.password is not None
            or parsed.fragment
            or (port is not None and not 1 <= port <= 65535)
        ):
            raise ValueError("upload URL requires verified HTTPS without user information")
        return value

    @field_validator("checksum_base64")
    @classmethod
    def sha256_digest(cls, value: str) -> str:
        try:
            decoded = base64.b64decode(value, validate=True)
        except (ValueError, binascii.Error):
            raise ValueError("invalid SHA256 checksum") from None
        if len(decoded) != 32 or base64.b64encode(decoded).decode("ascii") != value:
            raise ValueError("invalid SHA256 checksum")
        return value

    @field_validator("bucket", "object_key", "content_type")
    @classmethod
    def no_controls(cls, value: str) -> str:
        if any(ord(char) < 32 or ord(char) == 127 for char in value):
            raise ValueError("control characters are not allowed")
        return value

    @model_validator(mode="after")
    def bound_headers(self) -> Self:
        expected = {
            "if-none-match": "*",
            "content-length": str(self.size_bytes),
            "content-type": self.content_type,
            "x-amz-checksum-sha256": self.checksum_base64,
        }
        # F03 freezes one verified SHA256 PUT path, not a general HTTP header facility.
        headers: dict[str, str] = {}
        for name, secret in self.required_headers.items():
            key = name.lower()
            value = secret.get_secret_value()
            if key in headers or key not in expected or value != expected[key]:
                raise ValueError("upload headers do not match the grant")
            headers[key] = value
        if headers != expected:
            raise ValueError("upload requires condition, length, type and checksum headers")
        try:
            query = parse_qs(urlsplit(self.put_url.get_secret_value()).query, max_num_fields=32)
        except ValueError:
            raise ValueError("invalid signed upload query") from None
        signed = query.get("X-Amz-SignedHeaders", [])
        if (
            query.get("X-Amz-Algorithm") != ["AWS4-HMAC-SHA256"]
            or len(signed) != 1
            or set(signed[0].split(";")) != set(expected) | {"host"}
        ):
            raise ValueError("required upload headers must be signed")
        # One tested S3 path-style contract. No URL style guessing or client signing.
        path = urlsplit(self.put_url.get_secret_value()).path
        expected_path = f"/{self.bucket}/{self.object_key}"
        if (
            "/" in self.bucket
            or "\\" in expected_path
            or any(part in {"", ".", ".."} for part in expected_path[1:].split("/"))
            or unquote(expected_path) != expected_path
            or path != quote(expected_path, safe="/-._~")
        ):
            raise ValueError("upload URL must match the exact path-style object location")
        dates = query.get("X-Amz-Date", [])
        durations = query.get("X-Amz-Expires", [])
        try:
            if len(dates) != 1 or len(durations) != 1:
                raise ValueError
            signed_at = datetime.strptime(dates[0], "%Y%m%dT%H%M%SZ").replace(tzinfo=UTC)
            seconds = int(durations[0])
            if str(seconds) != durations[0] or not 1 <= seconds <= UPLOAD_AUTH_MAX_SECONDS:
                raise ValueError
            signed_expiry = signed_at + timedelta(seconds=seconds)
            if abs((self.expires_at - signed_expiry).total_seconds()) >= 1:
                raise ValueError
        except (ValueError, OverflowError):
            raise ValueError("upload expiry must match the bounded signed expiry") from None
        object.__setattr__(self, "expires_at", min(self.expires_at, signed_expiry))
        return self


class UploadLimits(BaseModel):
    model_config = ConfigDict(
        extra="forbid", frozen=True, hide_input_in_errors=True, allow_inf_nan=False
    )

    # Only the 64 MiB single-PUT path has been verified against the target store.
    max_object_bytes: int = Field(strict=True, ge=1, le=64 * 1024**2)
    chunk_bytes: int = Field(default=64 * 1024, strict=True, ge=4096, le=1024 * 1024)
    network_timeout_seconds: float = Field(default=5, ge=0.05, le=60)
    total_timeout_seconds: float = Field(default=120, ge=0.1, le=900)
    monotonic_deadline: float | None = Field(default=None, gt=0)
    grace_seconds: float = Field(default=0.2, ge=0, le=5)
    kill_seconds: float = Field(default=2, ge=0.05, le=5)
    reader_join_seconds: float = Field(default=0.5, ge=0.05, le=2)
    ca_file: Path | None = None
    external_supervision: bool = False

    @field_validator("ca_file")
    @classmethod
    def absolute_ca(cls, value: Path | None) -> Path | None:
        # Do not stat/read potentially blocking paths outside the supervised child.
        if value is not None and not value.is_absolute():
            raise ValueError("CA file must be an absolute path")
        return value


class UploadResult(BaseModel):
    model_config = ConfigDict(extra="forbid", frozen=True, allow_inf_nan=False)

    evidence_id: Identifier
    run_id: Identifier
    assignment_epoch: int
    placement_revision: int
    trace_id: Identifier
    status: Literal["NOT_SENT", "PUT_ACCEPTED", "REJECTED", "UNKNOWN"]
    reason_code: str
    http_status: int | None = None
    # Locally completed send chunks, not remote durability; null when no receipt exists.
    bytes_sent: int | None = None
    etag: str | None = None
    object_version: str | None = None
    process_id: int | None = None
    returncode: int | None = None
    signal: int | None = None
    stage: str = "UPLOAD_NOT_STARTED"
    stderr_tail: str = ""
    cleanup_confirmed: bool
    execution_elapsed_seconds: float
    cleanup_elapsed_seconds: float
