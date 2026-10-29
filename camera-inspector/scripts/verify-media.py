"""Opt-in F02 experiment using owned ZLM, generated sources, and bounded child jobs."""

from __future__ import annotations

import argparse
import ctypes
import hashlib
import json
import os
import re
import secrets
import shutil
import socket
import threading
import time
from ctypes import wintypes
from dataclasses import asdict
from pathlib import Path
from typing import Any
from urllib.parse import urlencode
from urllib.request import urlopen
from uuid import uuid4

from camera_inspector.media_types import MediaReport, MediaSpec, SampleFrame
from camera_inspector.processes import ManagedProcess

WORKSPACE = Path(__file__).resolve().parents[2]
EVIDENCE_ROOT = WORKSPACE / ".run" / "p0-f02"


def save(path: Path, value: Any) -> None:
    path.write_text(json.dumps(value, indent=2, ensure_ascii=False), encoding="utf-8")


def executable(value: str) -> str:
    resolved = shutil.which(value) or value
    path = Path(resolved).resolve(strict=True)
    if not path.is_file():
        raise ValueError("Executable is not a file")
    return str(path)


class OwnedCommand:
    """Drain both pipes continuously; retain bounded diagnostic output only."""

    def __init__(self, args: list[str], *, cwd: Path, capture: bool = False) -> None:
        self.process = ManagedProcess(args, cwd=cwd)
        self.process.mark_stage("F02_EXPERIMENT")
        self.output = bytearray()
        self.overflow = False
        self.capture = capture
        self.closed = False
        self.exit: dict[str, Any] | None = None
        self.threads = [
            threading.Thread(target=self._drain, args=(True,), daemon=True),
            threading.Thread(target=self._drain, args=(False,), daemon=True),
        ]
        for thread in self.threads:
            thread.start()

    def _drain(self, stdout: bool) -> None:
        stream = self.process.stdout if stdout else self.process.stderr
        try:
            while data := stream.read(16384):
                if stdout and self.capture:
                    room = 2 * 1024 * 1024 - len(self.output)
                    self.output.extend(data[:room])
                    self.overflow |= len(data) > room
                else:
                    self.process.record_stderr(data)
        except (OSError, ValueError):
            return

    def close(self, *, graceful: bool = False) -> dict[str, Any]:
        if self.closed:
            assert self.exit is not None
            return self.exit
        if graceful and self.process.poll() is None:
            try:
                self.process.stdin.write(b"q\n")
                self.process.wait(2)
            except (OSError, ValueError, TimeoutError):
                pass
        result = self.process.stop(grace_seconds=1, kill_seconds=2)
        for thread in self.threads:
            thread.join(timeout=2)
        self.exit = {"pid": self.process.pid, **asdict(result)}
        self.process.close()
        self.closed = True
        if not result.cleanup_confirmed:
            raise AssertionError("Owned process cleanup was not confirmed")
        return self.exit


def command(args: list[str], directory: Path, timeout: float = 30) -> bytes:
    process = OwnedCommand(args, cwd=directory, capture=True)
    try:
        code = process.process.wait(timeout)
        for thread in process.threads:
            thread.join(timeout=2)
        if code != 0 or process.overflow:
            raise AssertionError(f"Fixture command failed: {process.process.stderr_tail}")
        return bytes(process.output)
    finally:
        process.close()


def record_versions(root: Path, zlm: str, ffmpeg: str, ffprobe: str) -> None:
    environment_path = root / "environment.json"
    environment = json.loads(environment_path.read_text(encoding="utf-8"))
    fingerprint = hashlib.sha256(Path(zlm).read_bytes()).hexdigest()
    assert fingerprint == environment["zlm_sha256"], "ZLM binary changed since this run"
    versions = {}
    for name, binary in (("ffmpeg", ffmpeg), ("ffprobe", ffprobe)):
        versions[name] = command([binary, "-version"], root, 5).decode("utf-8").splitlines()[0]
    zlm_version = command([zlm, "-v"], root, 5).decode("utf-8", errors="replace")
    commit = re.search(r"git hash值:\s*([a-f0-9]+)", zlm_version)
    assert commit is not None, "ZLM did not report its build commit"
    versions["zlm_git_commit"] = commit.group(1)
    environment["versions"] = versions
    environment["source_bit_rate"] = None
    environment["source_bit_rate_note"] = "Not measured; synthetic variable-bitrate H.264"
    save(environment_path, environment)


def port() -> int:
    for _ in range(40):
        candidate = 49152 + secrets.randbelow(16384)
        with socket.socket() as probe:
            try:
                probe.bind(("127.0.0.1", candidate))
                return candidate
            except OSError:
                continue
    raise RuntimeError("No available isolated high port")


class IsolatedMedia:
    def __init__(self, root: Path, zlm: str, ffmpeg: str, ffprobe: str) -> None:
        self.root, self.ffmpeg, self.ffprobe = root, ffmpeg, ffprobe
        self.api_port, self.rtsp_port = port(), port()
        while self.rtsp_port == self.api_port:
            self.rtsp_port = port()
        self.secret = secrets.token_urlsafe(32)
        self.server_id = "inspection-f02-" + uuid4().hex
        self.children: list[OwnedCommand] = []
        self.cleanup: list[dict[str, Any]] = []
        self.source_count = 0
        config = root / "zlm.ini"
        config.write_text(
            f"[api]\napiDebug=0\nsecret={self.secret}\n"
            f"[general]\nmediaServerId={self.server_id}\nlisten_ip=127.0.0.1\n"
            "[hook]\nenable=0\n"
            f"[http]\nport={self.api_port}\nsslport=0\nallow_ip_range=127.0.0.1\n"
            f"rootPath={root / 'www'}\n"
            f"[rtsp]\nport={self.rtsp_port}\nsslport=0\ndirectProxy=0\n"
            "[rtmp]\nport=0\nsslport=0\n[rtc]\nport=0\ntcpPort=0\n"
            "[shell]\nport=0\n[srt]\nport=0\n[rtp_proxy]\nport=0\n"
            "[protocol]\nenable_hls=0\nenable_mp4=0\nenable_rtsp=1\nenable_rtmp=0\n"
            "enable_audio=0\nauto_close=0\n",
            encoding="utf-8",
        )
        self.server = self.start(
            [
                zlm,
                "-c",
                str(config),
                "-t",
                "2",
                "--affinity",
                "0",
                "-l",
                "3",
                "--log-dir",
                str(root / "zlm-logs"),
                "--log-size",
                "4",
                "--log-slice",
                "2",
            ]
        )
        try:
            self._wait_ready()
        except BaseException:
            self.close()
            raise

    def _wait_ready(self) -> None:
        deadline = time.monotonic() + 15
        while time.monotonic() < deadline:
            if self.server.process.poll() is not None:
                raise AssertionError("Owned ZLM exited during startup")
            try:
                config_result = self.api("getServerConfig")
                if config_result["data"][0]["general.mediaServerId"] == self.server_id:
                    return
                raise AssertionError("ZLM identity mismatch")
            except (OSError, KeyError):
                time.sleep(0.1)
        raise AssertionError("Owned ZLM startup deadline exceeded")

    def start(self, args: list[str], *, capture: bool = False) -> OwnedCommand:
        child = OwnedCommand(args, cwd=self.root, capture=capture)
        self.children.append(child)
        return child

    def api(self, method: str, **parameters: object) -> dict[str, Any]:
        query = urlencode({"secret": self.secret, **parameters})
        with urlopen(
            f"http://127.0.0.1:{self.api_port}/index/api/{method}?{query}", timeout=2
        ) as response:
            result = json.loads(response.read(1024 * 1024))
        if result.get("code") != 0:
            raise OSError("Isolated ZLM API operation failed")
        return result

    def source(self, *, gop: int = 15) -> tuple[OwnedCommand, str, str]:
        self.source_count += 1
        stream = f"source-{self.source_count}"
        url = f"rtsp://127.0.0.1:{self.rtsp_port}/inspection/{stream}"
        process = self.start(
            [
                self.ffmpeg,
                "-hide_banner",
                "-loglevel",
                "warning",
                "-re",
                "-f",
                "lavfi",
                "-i",
                "testsrc2=size=640x360:rate=15",
                "-an",
                "-c:v",
                "libx264",
                "-preset",
                "ultrafast",
                "-tune",
                "zerolatency",
                "-threads",
                "1",
                "-g",
                str(gop),
                "-keyint_min",
                str(gop),
                "-sc_threshold",
                "0",
                "-bf",
                "0",
                "-pix_fmt",
                "yuv420p",
                "-f",
                "rtsp",
                "-rtsp_transport",
                "tcp",
                url,
            ]
        )
        deadline = time.monotonic() + 12
        while time.monotonic() < deadline:
            if process.process.poll() is not None:
                raise AssertionError(f"Synthetic source exited: {process.process.stderr_tail}")
            try:
                self.info(stream)
                return process, url, stream
            except OSError:
                time.sleep(0.1)
        raise AssertionError("Synthetic source startup deadline exceeded")

    def info(self, stream: str) -> dict[str, Any]:
        return self.api(
            "getMediaInfo", schema="rtsp", vhost="__defaultVhost__", app="inspection", stream=stream
        )

    def probe(self, path: Path) -> dict[str, Any]:
        raw = command(
            [
                self.ffprobe,
                "-v",
                "error",
                "-show_entries",
                "format=duration,size:stream=codec_name,width,height,avg_frame_rate",
                "-of",
                "json",
                str(path),
            ],
            self.root,
        )
        return json.loads(raw)

    def close(self) -> None:
        failures: list[str] = []
        for child in reversed(self.children):
            try:
                self.cleanup.append(child.close(graceful=child is not self.server))
            except BaseException as error:
                failures.append(type(error).__name__)
                self.cleanup.append(
                    {
                        "pid": child.process.pid,
                        "cleanup_confirmed": False,
                        "error_type": type(error).__name__,
                    }
                )
        save(self.root / "owned-process-cleanup.json", self.cleanup)
        if failures:
            raise AssertionError("Owned process cleanup failed: " + ",".join(failures))


class ProcessEntry(ctypes.Structure):
    _fields_ = [
        ("size", wintypes.DWORD),
        ("usage", wintypes.DWORD),
        ("pid", wintypes.DWORD),
        ("heap", ctypes.c_size_t),
        ("module", wintypes.DWORD),
        ("threads", wintypes.DWORD),
        ("parent", wintypes.DWORD),
        ("priority", wintypes.LONG),
        ("flags", wintypes.DWORD),
        ("executable", wintypes.WCHAR * 260),
    ]


class MemoryCounters(ctypes.Structure):
    _fields_ = [("size", wintypes.DWORD), ("faults", wintypes.DWORD)] + [
        (name, ctypes.c_size_t)
        for name in (
            "peak_working",
            "working",
            "peak_paged",
            "paged",
            "peak_nonpaged",
            "nonpaged",
            "pagefile",
            "peak_pagefile",
        )
    ]


class IoCounters(ctypes.Structure):
    _fields_ = [
        (name, ctypes.c_ulonglong)
        for name in ("reads", "writes", "other", "read_bytes", "write_bytes", "other_bytes")
    ]


class ResourceSample:
    """Read-only Windows process-tree counters; I/O is not network-byte attribution."""

    def __init__(self) -> None:
        self.started = time.monotonic()
        self.stop_event = threading.Event()
        self.baseline: dict[tuple[int, int], tuple[float, int]] = {}
        self.latest: dict[tuple[int, int], tuple[float, int]] = {}
        self.peak_rss = self.peak_processes = 0
        self.supported = os.name == "nt"
        if not self.supported:
            return
        self.api = ctypes.WinDLL("kernel32", use_last_error=True)
        for name, args, result in (
            ("CreateToolhelp32Snapshot", [wintypes.DWORD, wintypes.DWORD], wintypes.HANDLE),
            ("Process32FirstW", [wintypes.HANDLE, ctypes.c_void_p], wintypes.BOOL),
            ("Process32NextW", [wintypes.HANDLE, ctypes.c_void_p], wintypes.BOOL),
            ("OpenProcess", [wintypes.DWORD, wintypes.BOOL, wintypes.DWORD], wintypes.HANDLE),
            ("GetProcessTimes", [wintypes.HANDLE] + [ctypes.c_void_p] * 4, wintypes.BOOL),
            (
                "K32GetProcessMemoryInfo",
                [wintypes.HANDLE, ctypes.c_void_p, wintypes.DWORD],
                wintypes.BOOL,
            ),
            ("GetProcessIoCounters", [wintypes.HANDLE, ctypes.c_void_p], wintypes.BOOL),
            ("CloseHandle", [wintypes.HANDLE], wintypes.BOOL),
        ):
            function = getattr(self.api, name)
            function.argtypes, function.restype = args, result
        self.baseline = self._snapshot()
        self.thread = threading.Thread(target=self._monitor, daemon=True)
        self.thread.start()

    def _snapshot(self) -> dict[tuple[int, int], tuple[float, int]]:
        snapshot = self.api.CreateToolhelp32Snapshot(2, 0)
        entry = ProcessEntry()
        entry.size = ctypes.sizeof(entry)
        parents: dict[int, int] = {}
        try:
            present = self.api.Process32FirstW(snapshot, ctypes.byref(entry))
            while present:
                parents[int(entry.pid)] = int(entry.parent)
                present = self.api.Process32NextW(snapshot, ctypes.byref(entry))
        finally:
            self.api.CloseHandle(snapshot)
        owned = {os.getpid()}
        for _ in range(16):
            children = {pid for pid, parent in parents.items() if parent in owned}
            if children <= owned:
                break
            owned.update(children)
        values: dict[tuple[int, int], tuple[float, int]] = {}
        rss = 0
        for pid in owned:
            handle = self.api.OpenProcess(0x0400 | 0x0010, False, pid)
            if not handle:
                continue
            try:
                created, exited, kernel, user = (ctypes.c_ulonglong() for _ in range(4))
                memory, io = MemoryCounters(), IoCounters()
                memory.size = ctypes.sizeof(memory)
                if not self.api.GetProcessTimes(
                    handle, *(ctypes.byref(item) for item in (created, exited, kernel, user))
                ):
                    continue
                self.api.K32GetProcessMemoryInfo(
                    handle, ctypes.byref(memory), ctypes.sizeof(memory)
                )
                self.api.GetProcessIoCounters(handle, ctypes.byref(io))
                rss += memory.working
                values[(pid, int(created.value))] = (
                    (kernel.value + user.value) / 10_000_000,
                    io.read_bytes + io.write_bytes + io.other_bytes,
                )
            finally:
                self.api.CloseHandle(handle)
        self.latest.update(values)
        self.peak_rss = max(self.peak_rss, rss)
        self.peak_processes = max(self.peak_processes, len(values))
        return values

    def _monitor(self) -> None:
        while not self.stop_event.wait(0.2):
            self._snapshot()

    def finish(self) -> dict[str, Any]:
        self.stop_event.set()
        if self.supported:
            self.thread.join(timeout=2)
            self._snapshot()
        elapsed = time.monotonic() - self.started
        cpu = sum(
            max(0, value[0] - self.baseline.get(pid, (0, 0))[0])
            for pid, value in self.latest.items()
        )
        io = sum(
            max(0, value[1] - self.baseline.get(pid, (0, 0))[1])
            for pid, value in self.latest.items()
        )
        return {
            "scope": "verifier coordinator and owned ZLM/publisher/media process tree",
            "sampling": "200ms lower-bound counters; short-lived exit intervals may be missed",
            "elapsed_seconds": elapsed,
            "cpu_seconds": cpu if self.supported else None,
            "cpu_percent_one_core": 100 * cpu / elapsed if self.supported else None,
            "host_capacity_percent": 100 * cpu / elapsed / (os.cpu_count() or 1)
            if self.supported
            else None,
            "peak_rss_bytes": self.peak_rss if self.supported else None,
            "peak_process_count": self.peak_processes if self.supported else None,
            "process_io_transfer_bytes": io if self.supported else None,
            "network_bytes": None,
            "upload_throughput": None,
            "inference_throughput": None,
            "unsupported": ["network byte attribution", "real uploads", "real model inference"],
        }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--zlm", required=True)
    parser.add_argument("--ffmpeg", default="ffmpeg")
    parser.add_argument("--ffprobe", default="ffprobe")
    parser.add_argument(
        "--smoke", action="store_true", help="Only verify isolated source and ffprobe"
    )
    parser.add_argument(
        "--cases",
        nargs="+",
        choices=["M1", "M2", "M3", "M4", "M5", "M6"],
        default=["M1", "M2", "M3", "M4", "M5", "M6"],
    )
    arguments = parser.parse_args()
    root = EVIDENCE_ROOT / ("media-" + uuid4().hex)
    root.mkdir(parents=True, exist_ok=False)
    print(f"F02 evidence: {root}", flush=True)
    media: IsolatedMedia | None = None
    try:
        media = IsolatedMedia(
            root,
            executable(arguments.zlm),
            executable(arguments.ffmpeg),
            executable(arguments.ffprobe),
        )
        publisher, url, stream = media.source()
        raw = command(
            [
                media.ffprobe,
                "-v",
                "error",
                "-rtsp_transport",
                "tcp",
                "-timeout",
                "5000000",
                "-show_entries",
                "stream=codec_name,width,height,avg_frame_rate",
                "-of",
                "json",
                url,
            ],
            root,
            10,
        )
        save(root / "source-metadata.json", json.loads(raw))
        save(
            root / "environment.json",
            {
                "source_label": "SYNTHETIC",
                "zlm_pid": media.server.process.pid,
                "source_pid": publisher.process.pid,
                "api_port": media.api_port,
                "rtsp_port": media.rtsp_port,
                "logical_processors": os.cpu_count(),
                "zlm_sha256": hashlib.sha256(
                    Path(executable(arguments.zlm)).read_bytes()
                ).hexdigest(),
            },
        )
        record_versions(root, executable(arguments.zlm), media.ffmpeg, media.ffprobe)
        if not arguments.smoke:
            run_matrix(media, publisher, url, stream, arguments.cases)
        print("F02 isolated verification completed", flush=True)
        return 0
    except BaseException as error:
        save(root / "failure.json", {"type": type(error).__name__, "message": str(error)[:2048]})
        raise
    finally:
        if media is not None:
            media.close()


class SessionRun:
    def __init__(self, media: IsolatedMedia, name: str, source: str, **options: Any) -> None:
        from camera_inspector.media import MediaSession

        parameters = {
            "observation_seconds": 4,
            "hard_timeout_seconds": 15,
            "startup_timeout_seconds": 5,
            "silence_timeout_seconds": 2,
            "record_original": False,
            **options,
        }
        self.spec = MediaSpec(
            source=source,
            output_dir=media.root / name,
            ffmpeg_path=media.ffmpeg,
            **parameters,
        )
        self.session = MediaSession(self.spec)
        self.stop = threading.Event()
        self.done = threading.Event()
        self.report: MediaReport | None = None
        self.error: BaseException | None = None
        self.thread = threading.Thread(target=self._run, daemon=True)
        self.thread.start()

    def _run(self) -> None:
        try:
            self.report = self.session.run(self.stop)
        except BaseException as error:
            self.error = error
        finally:
            self.done.set()

    def sample(self, timeout: float = 10) -> SampleFrame:
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline and not self.done.is_set():
            sample = self.session.poll_sample(0.1)
            if sample is not None:
                return sample
        report = self.finish()
        raise AssertionError(f"No frame: {report.reason_codes}")

    def finish(self, *, drain: bool = True) -> MediaReport:
        deadline = time.monotonic() + self.spec.hard_timeout_seconds + 10
        while not self.done.wait(0.03):
            if drain:
                self.session.poll_sample(0)
            if time.monotonic() >= deadline:
                self.stop.set()
                if not self.done.wait(5):
                    raise AssertionError("Media coordinator exceeded its hard deadline and cleanup")
        if self.error:
            raise self.error
        assert self.report is not None
        save(self.spec.output_dir / "report.json", self.report.model_dump(mode="json"))
        assert self.report.cleanup_confirmed, self.report.reason_codes
        return self.report


def expect_completed(report: MediaReport) -> None:
    assert report.status == "COMPLETED", report.reason_codes
    assert report.decoded_frames > 0 and report.sampled_frames > 0
    assert report.measurement_layer == "EXECUTOR_DECODE_PATH"
    assert all(value is None for value in report.unsupported_metrics.values())


def summary(report: MediaReport) -> dict[str, Any]:
    return {
        "report": f"{report.run_id}",
        "status": report.status,
        "reason_codes": report.reason_codes,
        "decoded_frames": report.decoded_frames,
        "sampled_frames": report.sampled_frames,
        "elapsed_seconds": report.elapsed_seconds,
        "cleanup_confirmed": report.cleanup_confirmed,
    }


def verify_evidence(media: IsolatedMedia, report: MediaReport) -> list[dict[str, Any]]:
    checks = []
    for evidence in report.evidence:
        result: dict[str, Any] = {
            "trigger_id": evidence.trigger_id,
            "reason_codes": evidence.reason_codes,
        }
        if evidence.frame_path:
            raw = command(
                [
                    media.ffmpeg,
                    "-v",
                    "error",
                    "-i",
                    str(evidence.frame_path),
                    "-frames:v",
                    "1",
                    "-pix_fmt",
                    "rgb24",
                    "-threads",
                    "1",
                    "-f",
                    "rawvideo",
                    "pipe:1",
                ],
                media.root,
                10,
            )
            result["trigger_rgb_hash_matches"] = (
                hashlib.sha256(raw).hexdigest() == evidence.frame_sha256
            )
            assert result["trigger_rgb_hash_matches"]
        if evidence.clip_path:
            result["probe"] = media.probe(evidence.clip_path)
            duration = float(result["probe"]["format"]["duration"])
            assert duration > 0
            assert result["probe"]["streams"][0]["codec_name"] == "h264"
            assert evidence.actual_start is not None and evidence.actual_end is not None
            assert abs(duration - (evidence.actual_end - evidence.actual_start)) < 0.5
            first_packet = json.loads(
                command(
                    [
                        media.ffprobe,
                        "-v",
                        "error",
                        "-select_streams",
                        "v:0",
                        "-read_intervals",
                        "%+#1",
                        "-show_packets",
                        "-show_entries",
                        "packet=pts_time,dts_time,flags",
                        "-of",
                        "json",
                        str(evidence.clip_path),
                    ],
                    media.root,
                    10,
                )
            )["packets"][0]
            result["first_packet"] = first_packet
            assert "K" in first_packet["flags"], "Copied clip must start at a keyframe"
        checks.append(result)
    return checks


def run_matrix(
    media: IsolatedMedia, publisher: OwnedCommand, url: str, stream: str, cases: list[str]
) -> None:
    results: dict[str, Any] = {
        "source_label": "SYNTHETIC",
        "measurement_layer": "EXECUTOR_DECODE_PATH",
        "limits": "No field-camera, 1000-camera, real-model or upload capacity claim",
        "cases": {},
    }
    destination = media.root / "matrix.json"

    def record(name: str, result: dict[str, Any]) -> None:
        results["cases"][name] = {"passed": True, **result}
        save(destination, results)
        print(f"{name} verified", flush=True)

    if "M1" in cases:
        viewer = media.start(
            [
                media.ffmpeg,
                "-hide_banner",
                "-loglevel",
                "warning",
                "-rtsp_transport",
                "tcp",
                "-timeout",
                "5000000",
                "-i",
                url,
                "-an",
                "-threads",
                "1",
                "-progress",
                "pipe:1",
                "-stats_period",
                "0.2",
                "-f",
                "null",
                "-",
            ],
            capture=True,
        )
        session = SessionRun(media, "M1-shared", url)
        session.sample()
        shared_readers = media.info(stream).get("totalReaderCount", 0)
        report = session.finish()
        expect_completed(report)
        before = re.findall(rb"frame=(\d+)", bytes(viewer.output))
        time.sleep(1)
        after = re.findall(rb"frame=(\d+)", bytes(viewer.output))
        assert shared_readers >= 2, f"Expected two consumers, observed {shared_readers}"
        assert before and after and int(after[-1]) > int(before[-1])
        remaining_readers = media.info(stream).get("totalReaderCount", 0)
        assert remaining_readers >= 1
        viewer.close(graceful=True)
        record(
            "M1",
            {
                "inspection": summary(report),
                "shared_readers": shared_readers,
                "preview_frames_before": int(before[-1]),
                "preview_frames_after": int(after[-1]),
                "remaining_preview_readers": remaining_readers,
                "ownership_boundary": (
                    "Experimental publisher owner closes after consumers; "
                    "no platform lease integration"
                ),
            },
        )

    if "M2" in cases:
        broken_source, broken_url, _ = media.source()
        interrupted = SessionRun(
            media,
            "M2-disconnect",
            broken_url,
            observation_seconds=20,
            hard_timeout_seconds=10,
            silence_timeout_seconds=1,
        )
        interrupted.sample()
        broken_source.close(graceful=True)
        disconnected = interrupted.finish()
        assert disconnected.status != "COMPLETED"
        damaged = media.root / "damaged-input.bin"
        damaged.write_bytes(b"not-a-media-stream\0" * 64)
        corrupt = SessionRun(
            media, "M2-corrupt", str(damaged), startup_timeout_seconds=1, hard_timeout_seconds=3
        ).finish()
        assert corrupt.status == "FAILED" and corrupt.decoded_frames == 0
        assert "SAMPLE_EVIDENCE_INSUFFICIENT" in corrupt.reason_codes
        pts_file = media.root / "pts-jump.ts"
        command(
            [
                media.ffmpeg,
                "-v",
                "error",
                "-f",
                "lavfi",
                "-i",
                "testsrc2=size=320x180:rate=15",
                "-frames:v",
                "90",
                "-vf",
                "setpts=PTS+3/TB*gte(N\\,45)",
                "-fps_mode",
                "passthrough",
                "-c:v",
                "libx264",
                "-preset",
                "ultrafast",
                "-threads",
                "1",
                "-g",
                "15",
                "-bf",
                "0",
                "-f",
                "mpegts",
                str(pts_file),
            ],
            media.root,
        )
        pts = SessionRun(
            media,
            "M2-pts",
            str(pts_file),
            read_file_realtime=False,
            observation_seconds=20,
            hard_timeout_seconds=5,
        ).finish()
        assert pts.pts_gap_count > 0, pts.reason_codes
        limited = SessionRun(
            media,
            "M2-cpu-limited",
            url,
            observation_seconds=3,
            hard_timeout_seconds=8,
            width=1920,
            height=1080,
            sample_fps=10,
            queue_capacity=1,
        )
        limited.sample()
        assert limited.session.process is not None
        limited.session.process.set_cpu_limit(1)
        constrained = limited.finish()
        record(
            "M2",
            {
                "disconnect": summary(disconnected),
                "corrupt": summary(corrupt),
                "pts_jump": summary(pts),
                "pts_gap_count": pts.pts_gap_count,
                "cpu_limited": summary(constrained),
                "job_cpu_hard_cap_percent": 1,
                "measurement_note": "Known local CPU quota; no inference of source packet loss",
            },
        )

    if "M3" in cases:
        fast = SessionRun(media, "M3-single", url, sample_fps=10, observation_seconds=3).finish()
        expect_completed(fast)
        slow_run = SessionRun(
            media, "M3-slow-branches", url, sample_fps=10, queue_capacity=1, observation_seconds=4
        )
        handed_off = 0
        while not slow_run.done.wait(0.4):
            if slow_run.session.poll_sample(0) is not None:
                handed_off += 1
        slow = slow_run.finish(drain=False)
        expect_completed(slow)
        assert slow.dropped_samples > 0 and slow.queue_high_water == 1
        assert "LOCAL_SAMPLE_OVERLOAD" in slow.reason_codes
        record(
            "M3",
            {
                "single": summary(fast),
                "slow": summary(slow),
                "handed_off_samples": handed_off,
                "dropped_samples": slow.dropped_samples,
                "queue_capacity": 1,
                "queue_high_water": slow.queue_high_water,
                "branch_note": "Synthetic slow branch; no real model or upload benchmark",
            },
        )

    if "M4" in cases:
        long_source, long_url, _ = media.source(gop=120)
        full = SessionRun(
            media,
            "M4-long-gop",
            long_url,
            observation_seconds=70,
            hard_timeout_seconds=95,
            startup_timeout_seconds=12,
            silence_timeout_seconds=10,
            record_original=True,
            max_cache_bytes=128 * 1024 * 1024,
        )
        requested: list[dict[str, Any]] = []
        first = full.sample(15)
        requested.append(full.session.trigger(first, clip_seconds=30, pre_seconds=15).model_dump())
        made30 = made60 = made_pre = False
        while not full.done.wait(0.02):
            sample = full.session.poll_sample(0.1)
            if sample is None:
                continue
            if not made30 and sample.elapsed_seconds >= 3:
                requested.append(full.session.trigger(sample, clip_seconds=30).model_dump())
                made30 = True
            elif not made60 and sample.elapsed_seconds >= 5:
                requested.append(full.session.trigger(sample, clip_seconds=60).model_dump())
                made60 = True
            elif not made_pre and sample.elapsed_seconds >= 35:
                requested.append(
                    full.session.trigger(sample, clip_seconds=30, pre_seconds=15).model_dump()
                )
                made_pre = True
        complete = full.finish()
        expect_completed(complete)
        assert len(complete.evidence) == 4
        assert all(item.frame_path is not None for item in complete.evidence)
        checks = verify_evidence(media, complete)
        assert sum(bool(item.get("trigger_rgb_hash_matches")) for item in checks) == 4
        for evidence in complete.evidence[1:]:
            assert evidence.clip_path is not None, evidence.reason_codes
            assert evidence.missing_post is not None and evidence.missing_post < 0.2
            assert evidence.missing_pre is not None and evidence.missing_pre < 0.2
        assert complete.evidence[0].missing_pre is not None and complete.evidence[0].missing_pre > 0
        long_source.close(graceful=True)
        late = SessionRun(
            media,
            "M4-late-deadline",
            url,
            observation_seconds=20,
            hard_timeout_seconds=6,
            record_original=True,
        )
        late_frame = late.sample()
        late.session.trigger(late_frame, clip_seconds=30)
        incomplete = late.finish()
        assert incomplete.status == "FAILED" and "HARD_DEADLINE" in incomplete.reason_codes
        assert incomplete.evidence and incomplete.evidence[0].segment_paths
        late_evidence = incomplete.evidence[0]
        assert late_evidence.missing_post is not None and late_evidence.missing_post > 0
        assert late_evidence.frame_path is None and late_evidence.clip_path is None
        assert "EVIDENCE_NOT_FINALIZED" in late_evidence.reason_codes
        record(
            "M4",
            {
                "long_gop_frames": 120,
                "source_fps": 15,
                "complete": summary(complete),
                "requested": requested,
                "clip_and_trigger_checks": checks,
                "late": summary(incomplete),
                "late_evidence": [item.model_dump(mode="json") for item in incomplete.evidence],
            },
        )

    if "M5" in cases:
        stopping = SessionRun(media, "M5-stop", url, observation_seconds=30)
        stopping.sample()
        start_stop = time.monotonic()
        stopping.stop.set()
        stopped = stopping.finish()
        stop_elapsed = time.monotonic() - start_stop
        assert stopped.status == "STOPPED" and stop_elapsed < 5
        deadline = SessionRun(
            media, "M5-deadline", url, observation_seconds=30, hard_timeout_seconds=2
        ).finish()
        assert deadline.status == "FAILED" and "HARD_DEADLINE" in deadline.reason_codes
        assert deadline.elapsed_seconds < 6
        record(
            "M5",
            {
                "stop": summary(stopped),
                "stop_elapsed_seconds": stop_elapsed,
                "deadline": summary(deadline),
                "parent_kill": "See independent ManagedProcess real parent-kill test",
                "http_db_boundary": (
                    "Media experiment has no HTTP/DB dependency; production bridge not implemented"
                ),
            },
        )

    if "M6" in cases:
        publisher.close(graceful=True)
        stages: list[dict[str, Any]] = []
        for concurrency in (1, 2, 4, 8):
            resources = ResourceSample()
            sources = [media.source() for _ in range(concurrency)]
            sessions = [
                SessionRun(
                    media,
                    f"M6-C{concurrency}-{index}",
                    source_url,
                    observation_seconds=4,
                    hard_timeout_seconds=15,
                    record_original=True,
                )
                for index, (_, source_url, _) in enumerate(sources)
            ]
            while not all(session.done.wait(0.01) for session in sessions):
                for session in sessions:
                    session.session.poll_sample(0)
            reports = [session.finish() for session in sessions]
            for source, _, _ in sources:
                source.close(graceful=True)
            measurements = resources.finish()
            for report in reports:
                expect_completed(report)
            stage = {
                "concurrency": concurrency,
                "reports": [summary(report) for report in reports],
                "resources": measurements,
                "source_encoder_cpu_included": True,
                "cache_peak_bytes_sum": sum(report.cache_peak_bytes for report in reports),
            }
            stages.append(stage)
            save(media.root / "concurrency-stages.json", stages)
            print(f"M6 concurrency {concurrency} completed", flush=True)
            if any(report.dropped_samples > 0 for report in reports):
                raise AssertionError("Stopped increasing concurrency after local sample overload")
            if (
                measurements["host_capacity_percent"] is not None
                and measurements["host_capacity_percent"] > 80
            ):
                raise AssertionError(
                    "Stopped increasing experimental concurrency after host overload"
                )
        listener = socket.socket()
        listener.bind(("127.0.0.1", 0))
        listener.listen(16)
        listener.settimeout(0.1)
        accepted: list[socket.socket] = []
        stop_accepting = threading.Event()

        def blackhole() -> None:
            while not stop_accepting.is_set():
                try:
                    client, _ = listener.accept()
                    accepted.append(client)
                except TimeoutError:
                    continue
                except OSError:
                    return

        thread = threading.Thread(target=blackhole, daemon=True)
        thread.start()
        resources = ResourceSample()
        try:
            unavailable = f"rtsp://127.0.0.1:{listener.getsockname()[1]}/no-response"
            sessions = [
                SessionRun(
                    media,
                    f"M6-timeout-{index}",
                    unavailable,
                    observation_seconds=10,
                    startup_timeout_seconds=1,
                    hard_timeout_seconds=3,
                )
                for index in range(8)
            ]
            timeouts = [session.finish() for session in sessions]
            for report in timeouts:
                assert report.status == "FAILED" and report.decoded_frames == 0
                assert "FIRST_FRAME_TIMEOUT" in report.reason_codes
        finally:
            stop_accepting.set()
            listener.close()
            for client in accepted:
                client.close()
            thread.join(timeout=1)
        record(
            "M6",
            {
                "stages": stages,
                "all_timeout": [summary(report) for report in timeouts],
                "timeout_resources": resources.finish(),
            },
        )


if __name__ == "__main__":
    raise SystemExit(main())
