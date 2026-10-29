"""Bounded inputs and measurements for the F02 experiment, not detector verdicts."""

from datetime import datetime
from pathlib import Path
from typing import Literal, Self
from urllib.parse import urlsplit
from uuid import uuid4

from pydantic import BaseModel, ConfigDict, Field, SecretStr, field_validator, model_validator

from camera_inspector.types import Identifier


class MediaSpec(BaseModel):
    model_config = ConfigDict(
        extra="forbid", frozen=True, allow_inf_nan=False, hide_input_in_errors=True
    )

    source: SecretStr
    output_dir: Path
    run_id: Identifier = Field(default_factory=lambda: uuid4().hex)
    trace_id: Identifier = Field(default_factory=lambda: uuid4().hex)
    source_label: Literal["SYNTHETIC", "REAL_CAMERA", "RECORDED_SAMPLE"] = "SYNTHETIC"
    observation_seconds: float = Field(default=10, ge=0.1, le=600)
    hard_timeout_seconds: float = Field(default=30, ge=0.1, le=900)
    monotonic_deadline: float | None = Field(default=None, gt=0)
    startup_timeout_seconds: float = Field(default=10, ge=0.1, le=60)
    silence_timeout_seconds: float = Field(default=5, ge=0.1, le=60)
    sample_fps: float = Field(default=2, ge=0.1, le=10)
    width: int = Field(default=320, ge=16, le=1920)
    height: int = Field(default=180, ge=16, le=1080)
    queue_capacity: int = Field(default=4, ge=1, le=32)
    max_samples: int = Field(default=4096, ge=1, le=10000)
    max_decoded_frames: int = Field(default=200000, ge=1, le=1000000)
    max_cache_bytes: int = Field(default=128 * 1024 * 1024, ge=1024, le=4 * 1024**3)
    max_cache_files: int = Field(default=256, ge=4, le=1024)
    segment_seconds: float = Field(default=2, ge=0.5, le=10)
    pts_gap_seconds: float = Field(default=2, ge=0.01, le=60)
    grace_seconds: float = Field(default=1, ge=0.05, le=5)
    kill_seconds: float = Field(default=2, ge=0.05, le=5)
    reader_join_seconds: float = Field(default=0.5, ge=0.05, le=2)
    evidence_timeout_seconds: float = Field(default=10, ge=0.1, le=30)
    record_original: bool = True
    read_file_realtime: bool = True
    external_supervision: bool = False
    ffmpeg_path: str = "ffmpeg"

    @field_validator("source")
    @classmethod
    def supported_source(cls, value: SecretStr) -> SecretStr:
        source = value.get_secret_value()
        if not source or len(source) > 4096 or any(c in source for c in "\r\n\x00"):
            raise ValueError("invalid media source")
        if Path(source).is_absolute():
            if not Path(source).is_file():
                raise ValueError("media file is unavailable")
        else:
            try:
                parsed = urlsplit(source)
            except ValueError:
                raise ValueError("invalid media URL") from None
            if parsed.scheme not in {"rtsp", "http", "https"} or not parsed.hostname:
                raise ValueError("source must be an absolute file path or RTSP/HTTP URL")
        return value

    @field_validator("output_dir")
    @classmethod
    def absolute_directory(cls, value: Path) -> Path:
        return value.expanduser().resolve()

    @model_validator(mode="after")
    def memory_budget(self) -> Self:
        # Output samples and at most four pinned trigger frames have explicit bounded memory.
        if self.width * self.height * 3 * (2 * self.queue_capacity + 8) > 64 * 1024 * 1024:
            raise ValueError("sample and trigger memory exceeds 64 MiB")
        if (
            self.source_label == "REAL_CAMERA"
            and Path(self.source.get_secret_value()).is_absolute()
        ):
            raise ValueError("local media must be labelled SYNTHETIC or RECORDED_SAMPLE")
        return self


class SampleFrame(BaseModel):
    model_config = ConfigDict(frozen=True)

    run_id: str
    index: int
    pts_seconds: float | None
    received_at: datetime
    elapsed_seconds: float
    width: int
    height: int
    rgb: bytes = Field(repr=False, exclude=True)
    sha256: str


class TriggerReceipt(BaseModel):
    model_config = ConfigDict(frozen=True)

    trigger_id: str
    sample_index: int
    frame_sha256: str
    clip_seconds: Literal[30, 60]
    requested_start: float
    requested_end: float


class EvidenceRecord(BaseModel):
    trigger_id: str
    sample_index: int
    frame_path: Path | None = None
    frame_sha256: str
    frame_pts_seconds: float | None
    requested_start: float
    requested_end: float
    actual_start: float | None = None
    actual_end: float | None = None
    missing_pre: float | None = None
    missing_post: float | None = None
    keyframe_offset_seconds: float | None = None
    segment_paths: list[Path] = Field(default_factory=list)
    clip_path: Path | None = None
    reason_codes: list[str] = Field(default_factory=list)


class ProcessSummary(BaseModel):
    returncode: int | None
    signal: int | None
    stage: str
    stderr_tail: str
    cleanup_confirmed: bool


class MediaReport(BaseModel):
    run_id: str
    trace_id: str
    source_label: str
    status: Literal["COMPLETED", "STOPPED", "FAILED", "LIMIT_REACHED"]
    reason_codes: list[str]
    measurement_layer: Literal["EXECUTOR_DECODE_PATH"] = "EXECUTOR_DECODE_PATH"
    decoded_frames: int
    sampled_frames: int
    dropped_samples: int
    queue_high_water: int
    pts_backward_count: int
    pts_gap_count: int
    pts_missing_count: int
    first_pts_seconds: float | None
    last_pts_seconds: float | None
    max_decoded_gap_seconds: float
    decode_error_count: int
    unsupported_metrics: dict[str, None] = Field(
        default_factory=lambda: {"rtp_packet_gap_seconds": None, "source_packet_loss_rate": None}
    )
    evidence: list[EvidenceRecord]
    cache_peak_bytes: int
    cache_overshoot_bytes: int
    cache_peak_files: int
    execution_elapsed_seconds: float
    cleanup_elapsed_seconds: float
    evidence_elapsed_seconds: float = 0
    elapsed_seconds: float
    cleanup_confirmed: bool
    process_exit: ProcessSummary
