"""One FFmpeg reader, bounded sampling and local evidence for F02 experiments."""

from __future__ import annotations

import csv
import hashlib
import math
import queue
import re
import shutil
import threading
import time
from dataclasses import asdict
from datetime import UTC, datetime
from pathlib import Path
from typing import BinaryIO, Literal
from uuid import uuid4

from camera_inspector.errors import InspectionError
from camera_inspector.media_types import (
    EvidenceRecord,
    MediaReport,
    MediaSpec,
    ProcessSummary,
    SampleFrame,
    TriggerReceipt,
)
from camera_inspector.processes import ManagedProcess, ProcessExit

_FRAME = re.compile(
    rb"\[showinfo@(decoded|sample) @ [^]]+\].*?\bn:\s*(\d+)\s+pts:\s*\S+\s+pts_time:(\S+)"
)
_DECODE_ERROR = re.compile(
    rb"error while decoding|corrupt decoded frame|invalid nal|decode_slice_header", re.I
)
_SEGMENT = re.compile(r"segment-[0-9]{6}\.mkv")


def _executable(value: str) -> str:
    located = shutil.which(value)
    if located is None:
        raise InspectionError("FFMPEG_UNAVAILABLE", 503)
    return str(Path(located).resolve())


def _join(threads: list[threading.Thread], seconds: float) -> bool:
    deadline = time.monotonic() + seconds
    for thread in threads:
        if thread.ident is not None:
            thread.join(timeout=max(0, deadline - time.monotonic()))
    return not any(thread.is_alive() for thread in threads)


class MediaSession:
    """A single-use session. Call run in its own thread and poll samples separately."""

    def __init__(self, spec: MediaSpec) -> None:
        self.spec = spec
        self._lock = threading.RLock()
        self._samples: queue.Queue[SampleFrame] = queue.Queue(spec.queue_capacity)
        self._frames: dict[int, tuple[bytes, datetime, float]] = {}
        self._metadata: dict[int, float | None] = {}
        self._issued: dict[int, tuple[str, float | None, float, datetime]] = {}
        self._triggers: list[tuple[TriggerReceipt, SampleFrame]] = []
        self._pair_floor = 0
        self._sample_sequence = -1
        self._started = 0.0
        self._first_decode: float | None = None
        self._last_decode: float | None = None
        self._first_pts: float | None = None
        self._last_pts: float | None = None
        self._decoded = self._sampled = self._dropped = self._queue_high = 0
        self._pts_backwards = self._pts_gaps = self._pts_missing = self._decode_errors = 0
        self._max_decode_gap = 0.0
        self._cache_peak = self._cache_files = 0
        self._fault: str | None = None
        self._running = False
        self._used = False
        self._evidence_cleanup_confirmed = True
        self.process: ManagedProcess | None = None

    def poll_sample(self, timeout: float = 0) -> SampleFrame | None:
        if not math.isfinite(timeout) or not 0 <= timeout <= 60:
            raise ValueError("sample poll timeout must be between 0 and 60 seconds")
        try:
            return self._samples.get(timeout=timeout)
        except queue.Empty:
            return None

    def trigger(
        self, sample: SampleFrame, clip_seconds: Literal[30, 60] = 30, pre_seconds: float = 0
    ) -> TriggerReceipt:
        if (
            clip_seconds not in {30, 60}
            or not math.isfinite(pre_seconds)
            or not 0 <= pre_seconds <= clip_seconds
        ):
            raise InspectionError("INVALID_EVIDENCE_WINDOW", 422)
        with self._lock:
            if not self._running:
                raise InspectionError("MEDIA_NOT_RUNNING", 409)
            original = self._issued.get(sample.index)
            if (
                sample.run_id != self.spec.run_id
                or sample.width != self.spec.width
                or sample.height != self.spec.height
                or not isinstance(sample.rgb, bytes)
                or len(sample.rgb) != self.spec.width * self.spec.height * 3
                or original
                != (sample.sha256, sample.pts_seconds, sample.elapsed_seconds, sample.received_at)
                or hashlib.sha256(sample.rgb).hexdigest() != sample.sha256
            ):
                raise InspectionError("INVALID_TRIGGER_FRAME", 422)
            if len(self._triggers) >= 4:
                raise InspectionError("TRIGGER_LIMIT", 409)
            origin = sample.pts_seconds
            if origin is None:
                raise InspectionError("TRIGGER_PTS_UNAVAILABLE", 409)
            receipt = TriggerReceipt(
                trigger_id=uuid4().hex,
                sample_index=sample.index,
                frame_sha256=sample.sha256,
                clip_seconds=clip_seconds,
                requested_start=origin - pre_seconds,
                requested_end=origin - pre_seconds + clip_seconds,
            )
            self._triggers.append((receipt, sample))
            return receipt

    def _command(self) -> list[str]:
        spec = self.spec
        source = spec.source.get_secret_value()
        args = [
            _executable(spec.ffmpeg_path),
            "-hide_banner",
            "-nostats",
            "-loglevel",
            "info",
            "-copyts",
            "-start_at_zero",
            "-filter_threads",
            "1",
        ]
        if Path(source).is_absolute():
            args += ["-protocol_whitelist", "file"]
            if spec.read_file_realtime:
                args += ["-re"]
        elif source.lower().startswith("rtsp:"):
            args += [
                "-protocol_whitelist",
                "rtsp,tcp,udp,rtp",
                "-rtsp_transport",
                "tcp",
                "-timeout",
                str(int(spec.startup_timeout_seconds * 1_000_000)),
            ]
        else:
            args += [
                "-protocol_whitelist",
                "http,https,tcp,tls,crypto",
                "-rw_timeout",
                str(int(spec.startup_timeout_seconds * 1_000_000)),
            ]
        filters = (
            "showinfo@decoded=checksum=0,"
            f"select='isnan(prev_selected_t)+gte(t-prev_selected_t,{1 / spec.sample_fps:.9f})',"
            f"scale={spec.width}:{spec.height},format=rgb24,showinfo@sample=checksum=0"
        )
        args += [
            "-threads",
            "1",
            "-i",
            source,
            "-map",
            "0:v:0",
            "-an",
            "-sn",
            "-dn",
            "-vf",
            filters,
            "-c:v",
            "rawvideo",
            "-threads",
            "1",
            "-fps_mode",
            "passthrough",
            "-f",
            "rawvideo",
            "pipe:1",
        ]
        if spec.record_original:
            args += [
                "-map",
                "0:v:0",
                "-an",
                "-sn",
                "-dn",
                "-c:v",
                "copy",
                "-f",
                "segment",
                "-segment_format",
                "matroska",
                "-segment_time",
                str(spec.segment_seconds),
                "-segment_list",
                "segment-index.csv",
                "-segment_list_type",
                "csv",
                "-segment_list_size",
                str(spec.max_cache_files),
                "-reset_timestamps",
                "1",
                "segment-%06d.mkv",
            ]
        return args

    def _line(self, line: bytes, process: ManagedProcess) -> None:
        match = _FRAME.search(line)
        if match is None:
            if _DECODE_ERROR.search(line):
                with self._lock:
                    self._decode_errors += 1
                process.record_stderr(b"DECODE_ERROR\n")
            elif not line.startswith(b"[showinfo@"):
                process.record_stderr(line + b"\n")
            return
        try:
            parsed = float(match[3])
            pts = parsed if math.isfinite(parsed) else None
        except ValueError:
            pts = None
        now = time.monotonic()
        with self._lock:
            if match[1] == b"decoded":
                self._decoded += 1
                if self._first_decode is None:
                    self._first_decode = now
                    self._first_pts = pts
                if self._last_decode is not None:
                    self._max_decode_gap = max(self._max_decode_gap, now - self._last_decode)
                self._last_decode = now
                if pts is None:
                    self._pts_missing += 1
                elif self._last_pts is not None:
                    difference = pts - self._last_pts
                    self._pts_backwards += difference < 0
                    self._pts_gaps += difference > self.spec.pts_gap_seconds
                if pts is not None:
                    self._last_pts = pts
                if self._decoded >= self.spec.max_decoded_frames:
                    self._fault = "DECODED_FRAME_LIMIT"
            else:
                index = int(match[2])
                if index <= self._sample_sequence:
                    self._fault = "SAMPLE_METADATA_RESET"
                    return
                self._sample_sequence = index
                if index >= self._pair_floor:
                    self._metadata[index] = pts
                    self._pair(index)

    def _read_stderr(self, stream: BinaryIO, process: ManagedProcess) -> None:
        pending = bytearray()
        discarding = False
        try:
            while chunk := stream.read(4096):
                for part in chunk.splitlines(keepends=True):
                    ends = part.endswith((b"\n", b"\r"))
                    if not discarding:
                        if len(pending) + len(part) > 4096:
                            pending.clear()
                            discarding = True
                        else:
                            pending.extend(part)
                    if ends:
                        if not discarding:
                            self._line(bytes(pending).rstrip(b"\r\n"), process)
                        pending.clear()
                        discarding = False
            if pending:
                self._line(bytes(pending), process)
        except (OSError, ValueError):
            with self._lock:
                self._fault = self._fault or "STDERR_READ_FAILED"

    def _read_frames(self, stream: BinaryIO) -> None:
        frame_bytes = self.spec.width * self.spec.height * 3
        pending = bytearray()
        index = 0
        try:
            while chunk := stream.read(min(frame_bytes - len(pending), 65536)):
                pending.extend(chunk)
                if len(pending) != frame_bytes:
                    continue
                now = time.monotonic()
                with self._lock:
                    if index >= self._pair_floor:
                        self._frames[index] = (
                            bytes(pending),
                            datetime.now(UTC),
                            now - self._started,
                        )
                        self._pair(index)
                index += 1
                pending.clear()
        except (OSError, ValueError):
            with self._lock:
                self._fault = self._fault or "STDOUT_READ_FAILED"

    def _pair(self, index: int) -> None:
        if index in self._frames and index in self._metadata:
            rgb, received, elapsed = self._frames.pop(index)
            pts = self._metadata.pop(index)
            self._sampled += 1
            if self._sampled > self.spec.max_samples:
                self._fault = "SAMPLE_LIMIT"
                return
            frame = SampleFrame(
                run_id=self.spec.run_id,
                index=index,
                pts_seconds=pts,
                received_at=received,
                elapsed_seconds=elapsed,
                width=self.spec.width,
                height=self.spec.height,
                rgb=rgb,
                sha256=hashlib.sha256(rgb).hexdigest(),
            )
            self._issued[index] = (frame.sha256, pts, elapsed, received)
            try:
                self._samples.put_nowait(frame)
            except queue.Full:
                self._dropped += 1
            self._queue_high = max(self._queue_high, self._samples.qsize())
        while len(self._frames) + len(self._metadata) > self.spec.queue_capacity + 2:
            oldest = min((*self._frames, *self._metadata))
            self._frames.pop(oldest, None)
            self._metadata.pop(oldest, None)
            self._pair_floor = oldest + 1
            self._dropped += 1

    def _cache(self) -> bool:
        total = count = 0
        for path in self.spec.output_dir.iterdir():
            if path.is_file():
                total += path.stat().st_size
                count += 1
            if count > self.spec.max_cache_files:
                break
        self._cache_peak = max(self._cache_peak, total)
        self._cache_files = max(self._cache_files, count)
        return total >= self.spec.max_cache_bytes or count >= self.spec.max_cache_files

    def run(self, stop_event: threading.Event) -> MediaReport:
        with self._lock:
            if self._used:
                raise InspectionError("MEDIA_SESSION_USED", 409)
            self._used = True
            self._running = True
            self._started = time.monotonic()
        deadline = self._started + self.spec.hard_timeout_seconds
        if self.spec.monotonic_deadline is not None:
            deadline = min(deadline, self.spec.monotonic_deadline)
        try:
            if stop_event.is_set():
                raise InspectionError("MEDIA_START_CANCELLED", 409)
            if time.monotonic() >= deadline:
                raise InspectionError("MEDIA_DEADLINE_EXPIRED", 409)
            self.spec.output_dir.mkdir(parents=True, exist_ok=True)
            if any(self.spec.output_dir.iterdir()):
                raise InspectionError("MEDIA_OUTPUT_NOT_EMPTY", 409)
            args = self._command()
            if stop_event.is_set() or time.monotonic() >= deadline:
                raise InspectionError("MEDIA_START_CANCELLED", 409)
            process = ManagedProcess(
                args, cwd=self.spec.output_dir, external_supervision=self.spec.external_supervision
            )
        except BaseException:
            self._running = False
            raise
        reason = "HARD_DEADLINE"
        status: Literal["COMPLETED", "STOPPED", "FAILED", "LIMIT_REACHED"] = "FAILED"
        self.process = process
        readers: list[threading.Thread] = []
        try:
            process.mark_stage("CONNECTING")
            readers = [
                threading.Thread(target=self._read_frames, args=(process.stdout,), daemon=True),
                threading.Thread(
                    target=self._read_stderr, args=(process.stderr, process), daemon=True
                ),
            ]
            for thread in readers:
                thread.start()
            while True:
                now = time.monotonic()
                with self._lock:
                    first, last, fault = self._first_decode, self._last_decode, self._fault
                    evidence_end = max(
                        (
                            frame.elapsed_seconds
                            + receipt.clip_seconds
                            - max(0, (frame.pts_seconds or 0) - receipt.requested_start)
                            for receipt, frame in self._triggers
                        ),
                        default=0,
                    )
                if stop_event.is_set():
                    status, reason = "STOPPED", "STOP_REQUESTED"
                    break
                if now >= deadline:
                    reason = "HARD_DEADLINE"
                    break
                if fault:
                    reason = fault
                    status = "LIMIT_REACHED" if fault.endswith("LIMIT") else "FAILED"
                    break
                if self._cache():
                    status, reason = "LIMIT_REACHED", "CACHE_LIMIT"
                    break
                if first is None and now - self._started >= self.spec.startup_timeout_seconds:
                    reason = "FIRST_FRAME_TIMEOUT"
                    break
                if last is not None and now - last >= self.spec.silence_timeout_seconds:
                    reason = "DECODE_PATH_STALLED"
                    break
                if first is not None:
                    process.mark_stage("OBSERVING")
                    if now >= max(
                        first + self.spec.observation_seconds, self._started + evidence_end
                    ):
                        status, reason = "COMPLETED", "OBSERVATION_COMPLETE"
                        break
                if process.poll() is not None:
                    reason = "SOURCE_ENDED" if process.poll() == 0 else "DECODER_EXIT"
                    break
                stop_event.wait(0.02)
        except OSError:
            status, reason = "FAILED", "MEDIA_IO_ERROR"
        finally:
            execution_end = time.monotonic()
            with self._lock:
                self._running = False
            try:
                if process.poll() is None:
                    try:
                        process.stdin.write(b"q\n")
                        process.stdin.flush()
                        process.wait(self.spec.grace_seconds)
                    except (OSError, ValueError, TimeoutError):
                        pass
                exited = process.stop(grace_seconds=0, kill_seconds=self.spec.kill_seconds)
                joined = _join(readers, self.spec.reader_join_seconds)
                if not joined:
                    reason = "PIPE_CLEANUP_UNCONFIRMED"
                    status = "FAILED"
                exited = ProcessExit(
                    exited.returncode,
                    exited.signal,
                    exited.stage,
                    process.stderr_tail,
                    exited.cleanup_confirmed and joined,
                )
            finally:
                process.close()
        cleanup_end = time.monotonic()
        if not exited.cleanup_confirmed:
            status, reason = "FAILED", "CLEANUP_UNCONFIRMED"
        evidence: list[EvidenceRecord] = []
        try:
            self._cache()
            if self._triggers:
                evidence = self._evidence(
                    deadline, stop_event, allow_processes=status == "COMPLETED"
                )
            self._cache()
        except OSError:
            status, reason = "FAILED", "EVIDENCE_IO_ERROR"
        evidence_end = time.monotonic()
        reasons = [reason]
        if self._dropped:
            reasons.append("LOCAL_SAMPLE_OVERLOAD")
        if self._pts_backwards or self._pts_gaps:
            reasons.append("DECODE_PATH_PTS_DISCONTINUITY")
        if self._decode_errors:
            reasons.append("DECODE_ERRORS")
        if self._sampled == 0:
            reasons.append("SAMPLE_EVIDENCE_INSUFFICIENT")
        confirmed = exited.cleanup_confirmed and self._evidence_cleanup_confirmed
        if not confirmed:
            reasons.append("CLEANUP_UNCONFIRMED")
            status = "FAILED"
        return MediaReport(
            run_id=self.spec.run_id,
            trace_id=self.spec.trace_id,
            source_label=self.spec.source_label,
            status=status,
            reason_codes=reasons,
            decoded_frames=self._decoded,
            sampled_frames=self._sampled,
            dropped_samples=self._dropped,
            queue_high_water=self._queue_high,
            pts_backward_count=self._pts_backwards,
            pts_gap_count=self._pts_gaps,
            pts_missing_count=self._pts_missing,
            first_pts_seconds=self._first_pts,
            last_pts_seconds=self._last_pts,
            max_decoded_gap_seconds=self._max_decode_gap,
            decode_error_count=self._decode_errors,
            evidence=evidence,
            cache_peak_bytes=self._cache_peak,
            cache_overshoot_bytes=max(0, self._cache_peak - self.spec.max_cache_bytes),
            cache_peak_files=self._cache_files,
            execution_elapsed_seconds=execution_end - self._started,
            cleanup_elapsed_seconds=evidence_end - execution_end,
            evidence_elapsed_seconds=evidence_end - cleanup_end,
            elapsed_seconds=time.monotonic() - self._started,
            cleanup_confirmed=confirmed,
            process_exit=ProcessSummary(**asdict(exited)),
        )

    def _auxiliary(
        self,
        args: list[str],
        deadline: float,
        stop_event: threading.Event,
        payload: bytes | None = None,
    ) -> bool:
        if (
            not self._evidence_cleanup_confirmed
            or stop_event.is_set()
            or time.monotonic() >= deadline
        ):
            return False
        args[0] = _executable(args[0])
        process = ManagedProcess(
            args, cwd=self.spec.output_dir, external_supervision=self.spec.external_supervision
        )

        def drain(stream: BinaryIO, stderr: bool) -> None:
            try:
                while data := stream.read(4096):
                    if stderr:
                        process.record_stderr(data)
            except (OSError, ValueError):
                pass

        def write() -> None:
            try:
                if payload is not None:
                    view = memoryview(payload)
                    while view:
                        written = process.stdin.write(view)
                        if written is None or written <= 0:
                            break
                        view = view[written:]
                process.stdin.close()
            except (OSError, ValueError):
                pass

        threads: list[threading.Thread] = []
        success = False
        try:
            process.mark_stage("EVIDENCE")
            threads = [
                threading.Thread(target=drain, args=(process.stdout, False), daemon=True),
                threading.Thread(target=drain, args=(process.stderr, True), daemon=True),
                threading.Thread(target=write, daemon=True),
            ]
            for thread in threads:
                thread.start()
            deadline = min(deadline, time.monotonic() + self.spec.evidence_timeout_seconds)
            while process.poll() is None and time.monotonic() < deadline:
                if stop_event.is_set() or self._cache():
                    break
                time.sleep(0.02)
            success = process.poll() == 0
        finally:
            try:
                result = process.stop(grace_seconds=0, kill_seconds=self.spec.kill_seconds)
                joined = _join(threads, self.spec.reader_join_seconds)
                confirmed = result.cleanup_confirmed and joined
                self._evidence_cleanup_confirmed &= confirmed
            finally:
                process.close()
        return success and confirmed

    def _segments(self) -> list[tuple[Path, float, float]]:
        index = self.spec.output_dir / "segment-index.csv"
        if not index.exists() or index.stat().st_size > 256 * 1024:
            return []
        segments: list[tuple[Path, float, float]] = []
        with index.open(newline="", encoding="utf-8") as stream:
            for row in csv.reader(stream):
                if len(segments) >= self.spec.max_cache_files or len(row) != 3:
                    break
                if not _SEGMENT.fullmatch(row[0]):
                    continue
                try:
                    start, end = float(row[1]), float(row[2])
                except ValueError:
                    continue
                path = self.spec.output_dir / row[0]
                if math.isfinite(start) and math.isfinite(end) and end >= start and path.is_file():
                    segments.append((path, start, end))
        return segments

    def _evidence(
        self, deadline: float, stop_event: threading.Event, *, allow_processes: bool
    ) -> list[EvidenceRecord]:
        segments = self._segments()
        records: list[EvidenceRecord] = []
        for receipt, frame in self._triggers:
            record = EvidenceRecord(
                trigger_id=receipt.trigger_id,
                sample_index=frame.index,
                frame_sha256=frame.sha256,
                frame_pts_seconds=frame.pts_seconds,
                requested_start=receipt.requested_start,
                requested_end=receipt.requested_end,
            )
            records.append(record)
            selected = [
                (path, start, end)
                for path, start, end in segments
                if end > receipt.requested_start and start < receipt.requested_end
            ]
            if selected:
                record.segment_paths = [item[0] for item in selected]
                record.actual_start = selected[0][1]
                record.actual_end = selected[-1][2]
                record.missing_pre = max(0, record.actual_start - receipt.requested_start)
                record.missing_post = max(0, receipt.requested_end - record.actual_end)
                record.keyframe_offset_seconds = record.actual_start - receipt.requested_start
                if record.missing_pre or record.missing_post:
                    record.reason_codes.append("CLIP_WINDOW_INCOMPLETE")
                if any(right[1] - left[2] > 0.1 for left, right in zip(selected, selected[1:])):
                    record.reason_codes.append("CLIP_SEGMENT_GAP")
            else:
                record.reason_codes.append("CLIP_UNAVAILABLE")
            if (
                not allow_processes
                or not self._evidence_cleanup_confirmed
                or stop_event.is_set()
                or time.monotonic() >= deadline
            ):
                record.reason_codes.append("EVIDENCE_NOT_FINALIZED")
                continue
            if self._cache():
                record.reason_codes.append("EVIDENCE_CACHE_LIMIT")
                continue
            image_path = self.spec.output_dir / f"trigger-{receipt.trigger_id}.png"
            args = [
                self.spec.ffmpeg_path,
                "-hide_banner",
                "-loglevel",
                "error",
                "-protocol_whitelist",
                "pipe",
                "-f",
                "rawvideo",
                "-pixel_format",
                "rgb24",
                "-video_size",
                f"{frame.width}x{frame.height}",
                "-i",
                "pipe:0",
                "-frames:v",
                "1",
                "-threads",
                "1",
                str(image_path),
            ]
            if self._auxiliary(args, deadline, stop_event, frame.rgb):
                record.frame_path = image_path
            else:
                record.reason_codes.append("TRIGGER_FRAME_WRITE_FAILED")
            if not selected:
                continue
            if (
                not self._evidence_cleanup_confirmed
                or stop_event.is_set()
                or time.monotonic() >= deadline
            ):
                record.reason_codes.append("EVIDENCE_NOT_FINALIZED")
                continue
            listing = self.spec.output_dir / f"clip-{receipt.trigger_id}.txt"
            listing.write_text(
                "".join(f"file '{path.name}'\n" for path, _, _ in selected), encoding="utf-8"
            )
            clip = self.spec.output_dir / f"clip-{receipt.trigger_id}.mkv"
            args = [
                self.spec.ffmpeg_path,
                "-hide_banner",
                "-loglevel",
                "error",
                "-protocol_whitelist",
                "file",
                "-f",
                "concat",
                "-safe",
                "1",
                "-i",
                str(listing),
                "-map",
                "0:v:0",
                "-c:v",
                "copy",
                str(clip),
            ]
            if not self._cache() and self._auxiliary(args, deadline, stop_event):
                record.clip_path = clip
            else:
                record.reason_codes.append("CLIP_WRITE_FAILED")
        return records
