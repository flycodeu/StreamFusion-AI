"""F02 mechanics use labelled synthetic files; they do not accept real camera health."""

import hashlib
import io
import shutil
import subprocess
import threading
import time
from concurrent.futures import ThreadPoolExecutor
from datetime import UTC, datetime
from pathlib import Path

import pytest
from pydantic import ValidationError

from camera_inspector import media
from camera_inspector.errors import InspectionError
from camera_inspector.media import MediaSession
from camera_inspector.media_types import MediaSpec, SampleFrame, TriggerReceipt
from camera_inspector.processes import ProcessExit


def spec(tmp_path: Path, **values) -> MediaSpec:
    source = tmp_path / "source.mp4"
    if not source.exists():
        source.write_bytes(b"test fixture")
    return MediaSpec(source=str(source), output_dir=tmp_path / "output", **values)


class FakeProcess:
    def __init__(self, *_args, **_kwargs):
        self.stdin = io.BytesIO()
        self.stdout = io.BytesIO()
        self.stderr = io.BytesIO()
        self.stderr_tail = ""
        self.stopped = self.closed = False

    def mark_stage(self, _stage):
        pass

    def record_stderr(self, _data):
        pass

    def poll(self):
        return 0 if self.stopped else None

    def wait(self, _timeout):
        self.stopped = True
        return 0

    def stop(self, **_kwargs):
        self.stopped = True
        return ProcessExit(0, None, "TEST", "", True)

    def close(self):
        self.closed = True


@pytest.mark.parametrize("cancel", [True, False])
def test_no_process_starts_after_stop_or_expired_deadline(tmp_path, monkeypatch, cancel):
    stop = threading.Event()
    if cancel:
        stop.set()
    configured = spec(tmp_path, monotonic_deadline=None if cancel else time.monotonic() - 1)
    monkeypatch.setattr(media, "ManagedProcess", lambda *_a, **_k: pytest.fail("must not spawn"))
    session = MediaSession(configured)
    with pytest.raises(InspectionError, match="MEDIA_START_CANCELLED|MEDIA_DEADLINE_EXPIRED"):
        session.run(stop)
    assert not session._running


def test_bad_source_is_redacted_and_network_protocols_exclude_file(tmp_path):
    secret = "rtsp://user:camera-secret@[broken"
    with pytest.raises(ValidationError) as error:
        MediaSpec(source=secret, output_dir=tmp_path)
    assert "camera-secret" not in str(error.value)
    configured = MediaSpec(source="https://example.test/video.m3u8", output_dir=tmp_path)
    args = MediaSession(configured)._command()
    whitelist = args[args.index("-protocol_whitelist") + 1].split(",")
    assert "file" not in whitelist and "pipe" not in whitelist
    assert Path(args[0]).is_absolute()


def test_reader_start_failure_still_stops_and_closes_process(tmp_path, monkeypatch):
    process = FakeProcess()
    monkeypatch.setattr(media, "ManagedProcess", lambda *_a, **_k: process)
    monkeypatch.setattr(
        threading.Thread,
        "start",
        lambda _self: (_ for _ in ()).throw(RuntimeError("thread failed")),
    )
    with pytest.raises(RuntimeError, match="thread failed"):
        MediaSession(spec(tmp_path)).run(threading.Event())
    assert process.stopped and process.closed


def test_auxiliary_filesystem_failure_still_cleans_process(tmp_path, monkeypatch):
    process = FakeProcess()
    monkeypatch.setattr(media, "ManagedProcess", lambda *_a, **_k: process)
    session = MediaSession(spec(tmp_path))
    monkeypatch.setattr(session, "_cache", lambda: (_ for _ in ()).throw(OSError("disk failed")))
    with pytest.raises(OSError, match="disk failed"):
        session._auxiliary(["ffmpeg"], time.monotonic() + 3, threading.Event())
    assert process.stopped and process.closed


def test_unknown_auxiliary_cleanup_prevents_all_later_processes(tmp_path, monkeypatch):
    class UnknownCleanup(FakeProcess):
        def poll(self):
            return 0

        def stop(self, **_kwargs):
            self.stopped = True
            return ProcessExit(0, None, "EVIDENCE", "", False)

    spawned = []

    def spawn(*_args, **_kwargs):
        process = UnknownCleanup()
        spawned.append(process)
        return process

    monkeypatch.setattr(media, "ManagedProcess", spawn)
    session = MediaSession(spec(tmp_path))
    session.spec.output_dir.mkdir()
    stop = threading.Event()
    assert not session._auxiliary(["ffmpeg"], time.monotonic() + 5, stop)
    assert not session._evidence_cleanup_confirmed
    assert not session._auxiliary(["ffmpeg"], time.monotonic() + 5, stop)
    assert len(spawned) == 1 and spawned[0].closed


def test_frame_pairing_is_bounded_and_trigger_cannot_change_sample(tmp_path):
    session = MediaSession(spec(tmp_path, width=16, height=16, queue_capacity=1))
    session._running = True
    session._started = time.monotonic()
    process = FakeProcess()
    payload = b"\x17" * (16 * 16 * 3)
    for index in range(4):
        session._frames[index] = (payload, datetime.now(UTC), float(index))
        session._line(
            f"[showinfo@sample @ fake] n: {index} pts: {index} pts_time:{index}".encode(), process
        )
    assert session._samples.qsize() == 1
    assert session._dropped == 3
    frame = session.poll_sample()
    assert frame is not None
    for update in (
        {"run_id": "other"},
        {"pts_seconds": 999.0},
        {"rgb": b"wrong"},
        {"sha256": "fake"},
    ):
        with pytest.raises(InspectionError, match="INVALID_TRIGGER_FRAME"):
            session.trigger(frame.model_copy(update=update))
    receipt = session.trigger(frame, pre_seconds=15)
    assert receipt.frame_sha256 == hashlib.sha256(payload).hexdigest()
    assert receipt.requested_start == -15


def test_interrupted_evidence_reports_existing_bounds_without_new_process(tmp_path, monkeypatch):
    session = MediaSession(spec(tmp_path))
    session.spec.output_dir.mkdir()
    segment = session.spec.output_dir / "segment-000000.mkv"
    segment.write_bytes(b"existing segment")
    (session.spec.output_dir / "segment-index.csv").write_text("segment-000000.mkv,0,2\n")
    frame = SampleFrame(
        run_id=session.spec.run_id,
        index=0,
        pts_seconds=0,
        received_at=datetime.now(UTC),
        elapsed_seconds=0,
        width=16,
        height=16,
        rgb=b"",
        sha256="original-frame",
    )
    receipt = TriggerReceipt(
        trigger_id="trigger",
        sample_index=0,
        frame_sha256=frame.sha256,
        clip_seconds=30,
        requested_start=-15,
        requested_end=15,
    )
    session._triggers.append((receipt, frame))
    monkeypatch.setattr(media, "ManagedProcess", lambda *_a, **_k: pytest.fail("must not spawn"))
    evidence = session._evidence(time.monotonic() - 1, threading.Event(), allow_processes=False)[0]
    assert evidence.actual_start == 0 and evidence.actual_end == 2
    assert evidence.missing_pre == 15 and evidence.missing_post == 13
    assert evidence.frame_path is None and evidence.clip_path is None
    assert "EVIDENCE_NOT_FINALIZED" in evidence.reason_codes


@pytest.fixture
def real_file(tmp_path):
    executable = shutil.which("ffmpeg")
    if executable is None:
        pytest.skip("FFmpeg is required for labelled synthetic media tests")
    output = tmp_path / "source.mp4"
    subprocess.run(
        [
            executable,
            "-hide_banner",
            "-loglevel",
            "error",
            "-f",
            "lavfi",
            "-i",
            "testsrc2=size=64x48:rate=10:duration=6",
            "-c:v",
            "libx264",
            "-threads",
            "1",
            "-g",
            "10",
            "-pix_fmt",
            "yuv420p",
            str(output),
        ],
        timeout=10,
        check=True,
        capture_output=True,
    )
    return output


@pytest.mark.media
def test_real_ffmpeg_single_decode_and_lossless_original_trigger(tmp_path, real_file):
    configured = spec(
        tmp_path,
        width=64,
        height=48,
        sample_fps=5,
        observation_seconds=0.7,
        hard_timeout_seconds=8,
        startup_timeout_seconds=3,
    )
    session, stop = MediaSession(configured), threading.Event()
    with ThreadPoolExecutor(max_workers=1) as executor:
        result = executor.submit(session.run, stop)
        frame = session.poll_sample(3)
        if frame is None:
            pytest.fail(str(result.result(timeout=5)))
        session.trigger(frame, pre_seconds=29.5)
        while not result.done():
            session.poll_sample(0.1)
        report = result.result(timeout=2)
    assert report.status == "COMPLETED", report.model_dump()
    assert report.cleanup_confirmed
    assert report.decoded_frames > report.sampled_frames > 0
    assert report.measurement_layer == "EXECUTOR_DECODE_PATH"
    assert all(value is None for value in report.unsupported_metrics.values())
    evidence = report.evidence[0]
    assert evidence.frame_path is not None, evidence.model_dump()
    decoded = subprocess.run(
        [
            shutil.which("ffmpeg"),
            "-hide_banner",
            "-loglevel",
            "error",
            "-i",
            str(evidence.frame_path),
            "-f",
            "rawvideo",
            "-pix_fmt",
            "rgb24",
            "pipe:1",
        ],
        timeout=5,
        check=True,
        capture_output=True,
    ).stdout
    assert hashlib.sha256(decoded).hexdigest() == frame.sha256 == evidence.frame_sha256
    assert evidence.missing_pre is not None and evidence.missing_pre > 0


@pytest.mark.media
def test_slow_consumer_does_not_block_stop_or_forge_source_loss(tmp_path, real_file):
    session = MediaSession(
        spec(
            tmp_path,
            record_original=False,
            width=64,
            height=48,
            sample_fps=10,
            queue_capacity=1,
            observation_seconds=4,
            hard_timeout_seconds=6,
        )
    )
    stop = threading.Event()
    with ThreadPoolExecutor(max_workers=1) as executor:
        result = executor.submit(session.run, stop)
        deadline = time.monotonic() + 3
        while session._dropped == 0 and not result.done() and time.monotonic() < deadline:
            time.sleep(0.02)
        before = time.monotonic()
        stop.set()
        report = result.result(timeout=4)
    assert report.status == "STOPPED", report.model_dump()
    assert report.cleanup_confirmed and time.monotonic() - before < 3
    assert report.dropped_samples > 0
    assert "LOCAL_SAMPLE_OVERLOAD" in report.reason_codes
    assert report.unsupported_metrics["source_packet_loss_rate"] is None
