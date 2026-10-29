"""Bounded child processes; Windows job containment is established during creation.

Linux process groups require an externally supervised systemd/cgroup deployment.
They do not provide Windows job kill-on-parent-close semantics by themselves.
"""

from __future__ import annotations

import ctypes
import io
import math
import os
import re
import signal
import subprocess
import threading
import time
from collections.abc import Buffer, Mapping, Sequence
from ctypes import wintypes
from dataclasses import dataclass
from pathlib import Path
from typing import IO

from camera_inspector.errors import InspectionError

_ENV_KEYS = frozenset({"SYSTEMROOT", "WINDIR", "PATH", "TEMP", "TMP", "LANG", "LC_ALL"})
_STDERR_BYTES = 16 * 1024
_STDERR_LINE_BYTES = 2048
_MAX_JOB_MEMBERS = 256
_SPAWN_LOCK = threading.Lock()
_URL = re.compile(r"(?i)\b[a-z][a-z0-9+.-]*://[^\s]+")
_AUTH = re.compile(r"(?i)\b(?:authorization|proxy-authorization)\s*[:=][^\r\n]*")
_SECRET = re.compile(
    r"(?i)\b(?:token|access_token|api[_-]?key|password|secret)[\"']?\s*[:=]\s*"
    r"(?:\"[^\"\r\n]*\"|'[^'\r\n]*'|[^\s,}]+)"
)
_USERINFO = re.compile(r"[^\s:/]+:[^\s@]+@[^\s]+")


def _environment(overrides: Mapping[str, str] | None) -> dict[str, str]:
    result = {key.upper(): value for key, value in os.environ.items() if key.upper() in _ENV_KEYS}
    for key, value in (overrides or {}).items():
        if key.upper() not in _ENV_KEYS or "\0" in value:
            raise InspectionError("PROCESS_ENV_NOT_ALLOWED", 422)
        result[key.upper()] = value
    return result


def _seconds(value: float) -> float:
    if not math.isfinite(value) or not 0 <= value <= 300:
        raise ValueError("process timeout must be between 0 and 300 seconds")
    return value


def _redact(data: bytes | bytearray) -> bytes:
    text = data.decode("utf-8", errors="replace")
    text = _AUTH.sub("[redacted-auth]", text)
    text = _URL.sub("[redacted-url]", text)
    text = _USERINFO.sub("[redacted-userinfo]", text)
    return _SECRET.sub("[redacted-secret]", text).encode("utf-8")


@dataclass(frozen=True)
class ProcessExit:
    returncode: int | None
    signal: int | None
    stage: str
    stderr_tail: str
    cleanup_confirmed: bool


class _Pipe(io.FileIO):
    """Defer native close until an in-flight read/write returns, without waiting here."""

    def __init__(self, descriptor: int, mode: str) -> None:
        self._operation_lock = threading.Lock()
        self._active_operations = 0
        self._close_requested = False
        super().__init__(descriptor, mode, closefd=True)

    def _begin(self) -> None:
        with self._operation_lock:
            if self._close_requested:
                raise ValueError("operation on closing process pipe")
            self._active_operations += 1

    def _finish(self) -> None:
        with self._operation_lock:
            self._active_operations -= 1
            if self._close_requested and self._active_operations == 0:
                super().close()

    def read(self, size: int | None = -1) -> bytes:
        self._begin()
        try:
            data = super().read(size)
            if data is None:
                raise BlockingIOError("process pipe is not configured for nonblocking reads")
            return data
        finally:
            self._finish()

    def write(self, data: Buffer) -> int:
        self._begin()
        try:
            written = super().write(data)
            if written is None:
                raise BlockingIOError("process pipe is not configured for nonblocking writes")
            return written
        finally:
            self._finish()

    def close(self) -> None:
        with self._operation_lock:
            self._close_requested = True
            if self._active_operations == 0:
                super().close()


class _StartupInfo(ctypes.Structure):
    _fields_ = [
        ("cb", wintypes.DWORD),
        ("reserved", wintypes.LPWSTR),
        ("desktop", wintypes.LPWSTR),
        ("title", wintypes.LPWSTR),
        ("x", wintypes.DWORD),
        ("y", wintypes.DWORD),
        ("x_size", wintypes.DWORD),
        ("y_size", wintypes.DWORD),
        ("x_chars", wintypes.DWORD),
        ("y_chars", wintypes.DWORD),
        ("fill", wintypes.DWORD),
        ("flags", wintypes.DWORD),
        ("show", wintypes.WORD),
        ("reserved_size", wintypes.WORD),
        ("reserved_bytes", ctypes.c_void_p),
        ("stdin", wintypes.HANDLE),
        ("stdout", wintypes.HANDLE),
        ("stderr", wintypes.HANDLE),
    ]


class _StartupInfoEx(ctypes.Structure):
    _fields_ = [("startup", _StartupInfo), ("attributes", ctypes.c_void_p)]


class _ProcessInformation(ctypes.Structure):
    _fields_ = [
        ("process", wintypes.HANDLE),
        ("thread", wintypes.HANDLE),
        ("pid", wintypes.DWORD),
        ("thread_id", wintypes.DWORD),
    ]


class _BasicLimits(ctypes.Structure):
    _fields_ = [
        ("process_time", ctypes.c_longlong),
        ("job_time", ctypes.c_longlong),
        ("flags", wintypes.DWORD),
        ("min_working_set", ctypes.c_size_t),
        ("max_working_set", ctypes.c_size_t),
        ("active_process_limit", wintypes.DWORD),
        ("affinity", ctypes.c_size_t),
        ("priority", wintypes.DWORD),
        ("scheduling", wintypes.DWORD),
    ]


class _IoCounters(ctypes.Structure):
    _fields_ = [(name, ctypes.c_ulonglong) for name in ("r", "w", "o", "rb", "wb", "ob")]


class _ExtendedLimits(ctypes.Structure):
    _fields_ = [
        ("basic", _BasicLimits),
        ("io", _IoCounters),
        ("process_memory", ctypes.c_size_t),
        ("job_memory", ctypes.c_size_t),
        ("peak_process_memory", ctypes.c_size_t),
        ("peak_job_memory", ctypes.c_size_t),
    ]


class _JobAccounting(ctypes.Structure):
    _fields_ = [
        ("user_time", ctypes.c_longlong),
        ("kernel_time", ctypes.c_longlong),
        ("period_user_time", ctypes.c_longlong),
        ("period_kernel_time", ctypes.c_longlong),
        ("page_faults", wintypes.DWORD),
        ("total_processes", wintypes.DWORD),
        ("active_processes", wintypes.DWORD),
        ("terminated_processes", wintypes.DWORD),
    ]


class _CpuLimit(ctypes.Structure):
    _fields_ = [("flags", wintypes.DWORD), ("rate", wintypes.DWORD)]


class _WindowsProcess:
    """Small Win32 binding needed for atomic JOB_LIST, unsupported by Python Popen."""

    def __init__(self, args: Sequence[str], cwd: Path | None, env: Mapping[str, str]) -> None:
        import msvcrt

        self._api = ctypes.WinDLL("kernel32", use_last_error=True)
        signatures = {
            "CreateJobObjectW": ([ctypes.c_void_p, wintypes.LPCWSTR], wintypes.HANDLE),
            "SetInformationJobObject": (
                [wintypes.HANDLE, ctypes.c_int, ctypes.c_void_p, wintypes.DWORD],
                wintypes.BOOL,
            ),
            "QueryInformationJobObject": (
                [wintypes.HANDLE, ctypes.c_int, ctypes.c_void_p, wintypes.DWORD, ctypes.c_void_p],
                wintypes.BOOL,
            ),
            "TerminateJobObject": ([wintypes.HANDLE, wintypes.UINT], wintypes.BOOL),
            "CloseHandle": ([wintypes.HANDLE], wintypes.BOOL),
            "InitializeProcThreadAttributeList": (
                [ctypes.c_void_p, wintypes.DWORD, wintypes.DWORD, ctypes.c_void_p],
                wintypes.BOOL,
            ),
            "UpdateProcThreadAttribute": (
                [
                    ctypes.c_void_p,
                    wintypes.DWORD,
                    ctypes.c_size_t,
                    ctypes.c_void_p,
                    ctypes.c_size_t,
                    ctypes.c_void_p,
                    ctypes.c_void_p,
                ],
                wintypes.BOOL,
            ),
            "DeleteProcThreadAttributeList": ([ctypes.c_void_p], None),
            "CreateProcessW": (
                [
                    wintypes.LPCWSTR,
                    wintypes.LPWSTR,
                    ctypes.c_void_p,
                    ctypes.c_void_p,
                    wintypes.BOOL,
                    wintypes.DWORD,
                    ctypes.c_void_p,
                    wintypes.LPCWSTR,
                    ctypes.c_void_p,
                    ctypes.c_void_p,
                ],
                wintypes.BOOL,
            ),
            "WaitForSingleObject": ([wintypes.HANDLE, wintypes.DWORD], wintypes.DWORD),
            "GetExitCodeProcess": ([wintypes.HANDLE, ctypes.c_void_p], wintypes.BOOL),
            "TerminateProcess": ([wintypes.HANDLE, wintypes.UINT], wintypes.BOOL),
            "OpenProcess": ([wintypes.DWORD, wintypes.BOOL, wintypes.DWORD], wintypes.HANDLE),
            "IsProcessInJob": ([wintypes.HANDLE, wintypes.HANDLE, ctypes.c_void_p], wintypes.BOOL),
        }
        for name, (argtypes, restype) in signatures.items():
            function = getattr(self._api, name)
            function.argtypes = argtypes
            function.restype = restype
        self._job: int | None = None
        self._handle: int | None = None
        self._returncode: int | None = None
        self._members: dict[int, int] = {}
        self._members_complete = True
        descriptors: set[int] = set()
        streams: list[IO[bytes]] = []
        attributes = None
        attributes_ready = False
        try:
            # Anonymous, non-inheritable handle; only this coordinator owns the job lifetime.
            self._job = self._api.CreateJobObjectW(None, None)
            self._check(self._job)
            limits = _ExtendedLimits()
            limits.basic.flags = 0x00002000  # JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE, no breakaway.
            self._check(
                self._api.SetInformationJobObject(
                    self._job, 9, ctypes.byref(limits), ctypes.sizeof(limits)
                )
            )
            pairs = []
            for _ in range(3):
                pair = os.pipe()
                descriptors.update(pair)
                pairs.append(pair)
            stdin_read, stdin_write = pairs[0]
            stdout_read, stdout_write = pairs[1]
            stderr_read, stderr_write = pairs[2]
            inherited = (stdin_read, stdout_write, stderr_write)
            for descriptor in inherited:
                os.set_inheritable(descriptor, True)
            handles = (wintypes.HANDLE * 3)(*(msvcrt.get_osfhandle(fd) for fd in inherited))
            jobs = (wintypes.HANDLE * 1)(self._job)
            size = ctypes.c_size_t()
            self._api.InitializeProcThreadAttributeList(None, 2, 0, ctypes.byref(size))
            attributes = ctypes.create_string_buffer(size.value)
            self._check(
                self._api.InitializeProcThreadAttributeList(attributes, 2, 0, ctypes.byref(size))
            )
            attributes_ready = True
            for attribute, value in ((0x00020002, handles), (0x0002000D, jobs)):
                self._check(
                    self._api.UpdateProcThreadAttribute(
                        attributes,
                        0,
                        attribute,
                        ctypes.byref(value),
                        ctypes.sizeof(value),
                        None,
                        None,
                    )
                )
            startup = _StartupInfoEx()
            startup.startup.cb = ctypes.sizeof(startup)
            startup.startup.flags = 0x00000100 | 0x00000001  # USESTDHANDLES | USESHOWWINDOW.
            startup.startup.show = 0
            startup.startup.stdin, startup.startup.stdout, startup.startup.stderr = handles
            startup.attributes = ctypes.cast(attributes, ctypes.c_void_p)
            information = _ProcessInformation()
            command = ctypes.create_unicode_buffer(subprocess.list2cmdline(args))
            environment = ctypes.create_unicode_buffer(
                "\0".join(f"{key}={value}" for key, value in sorted(env.items())) + "\0"
            )
            self._check(
                self._api.CreateProcessW(
                    args[0],
                    command,
                    None,
                    None,
                    True,
                    0x00080000 | 0x00000400 | 0x08000000,
                    environment,
                    str(cwd) if cwd else None,
                    ctypes.byref(startup),
                    ctypes.byref(information),
                )
            )
            self._handle = information.process
            self.pid = int(information.pid)
            self._api.CloseHandle(information.thread)
            for descriptor, mode in ((stdin_write, "wb"), (stdout_read, "rb"), (stderr_read, "rb")):
                streams.append(_Pipe(descriptor, mode))
                descriptors.remove(descriptor)
            self.stdin, self.stdout, self.stderr = streams
        except BaseException:
            for stream in streams:
                stream.close()
            self.close()
            raise
        finally:
            if attributes_ready:
                self._api.DeleteProcThreadAttributeList(attributes)
            for descriptor in descriptors:
                os.close(descriptor)

    @staticmethod
    def _check(value: object) -> None:
        if not value:
            raise OSError(ctypes.get_last_error(), "PROCESS_OS_OPERATION_FAILED")

    def poll(self) -> int | None:
        if self._returncode is None:
            status = self._api.WaitForSingleObject(self._handle, 0)
            if status == 0:
                code = wintypes.DWORD()
                self._check(self._api.GetExitCodeProcess(self._handle, ctypes.byref(code)))
                self._returncode = int(code.value)
            elif status != 258:
                raise OSError("PROCESS_WAIT_FAILED")
        return self._returncode

    def terminate(self) -> None:
        if self.poll() is None:
            result = self._api.TerminateProcess(self._handle, 1)
            if not result and self.poll() is None:
                self._check(result)

    def kill_tree(self) -> None:
        self._check(self._api.TerminateJobObject(self._job, 1))

    def prepare_stop(self) -> None:
        # Job accounting can reach zero just before exiting process handles are signaled.
        # Hold finite member handles so confirmed cleanup waits for their actual exits too.
        for pid, handle in list(self._members.items()):
            if self._api.WaitForSingleObject(handle, 0) == 0:
                self._api.CloseHandle(handle)
                del self._members[pid]
        buffer = ctypes.create_string_buffer(8 + ctypes.sizeof(ctypes.c_size_t) * _MAX_JOB_MEMBERS)
        result = self._api.QueryInformationJobObject(self._job, 3, buffer, len(buffer), None)
        if not result:
            self._members_complete = False
            return
        counts = (wintypes.DWORD * 2).from_buffer(buffer)
        count = int(counts[1])
        if int(counts[0]) > count or count > _MAX_JOB_MEMBERS:
            self._members_complete = False
            return
        pids = (ctypes.c_size_t * count).from_buffer(buffer, 8)
        for pid in pids:
            if pid in self._members:
                continue
            if len(self._members) >= _MAX_JOB_MEMBERS:
                self._members_complete = False
                return
            handle = self._api.OpenProcess(0x00100000 | 0x1000, False, pid)
            if not handle:
                if ctypes.get_last_error() != 87:  # A process already gone has no openable PID.
                    self._members_complete = False
                continue
            belongs = wintypes.BOOL()
            ok = self._api.IsProcessInJob(handle, self._job, ctypes.byref(belongs))
            if ok and belongs.value:
                self._members[int(pid)] = int(handle)
            else:
                if not ok and self._api.WaitForSingleObject(handle, 0) != 0:
                    self._members_complete = False
                self._api.CloseHandle(handle)

    def tree_empty(self) -> bool:
        info = _JobAccounting()
        self._check(
            self._api.QueryInformationJobObject(
                self._job, 1, ctypes.byref(info), ctypes.sizeof(info), None
            )
        )
        return bool(
            self._members_complete
            and info.active_processes == 0
            and all(
                self._api.WaitForSingleObject(handle, 0) == 0 for handle in self._members.values()
            )
        )

    def set_cpu_limit(self, percent: int) -> None:
        limit = _CpuLimit(0x1 | 0x4, percent * 100)  # ENABLE | HARD_CAP, in 1/100 percent.
        self._check(
            self._api.SetInformationJobObject(
                self._job, 15, ctypes.byref(limit), ctypes.sizeof(limit)
            )
        )
        actual = _CpuLimit()
        self._check(
            self._api.QueryInformationJobObject(
                self._job, 15, ctypes.byref(actual), ctypes.sizeof(actual), None
            )
        )
        if actual.flags & 0x5 != 0x5 or actual.rate != limit.rate:
            raise OSError("PROCESS_CPU_LIMIT_NOT_APPLIED")

    def close(self) -> None:
        for handle in self._members.values():
            self._api.CloseHandle(handle)
        self._members.clear()
        if self._job is not None:
            self._api.CloseHandle(self._job)
            self._job = None
        if self._handle is not None:
            self._api.CloseHandle(self._handle)
            self._handle = None


class ManagedProcess:
    """Caller drains both output pipes continuously and owns FFmpeg's optional q request.

    stop() confirms the Windows job is empty. On externally supervised Linux it
    confirms only this process group; external cgroup cleanup remains a deployment duty.
    """

    def __init__(
        self,
        args: Sequence[str],
        *,
        cwd: Path | None = None,
        env: Mapping[str, str] | None = None,
        external_supervision: bool = False,
    ) -> None:
        if isinstance(args, (str, bytes)) or not args or len(args) > 256:
            raise InspectionError("INVALID_PROCESS_ARGUMENTS", 422)
        if not Path(args[0]).is_absolute() or any("\0" in arg for arg in args):
            raise InspectionError("INVALID_PROCESS_ARGUMENTS", 422)
        environment = _environment(env)
        self._windows: _WindowsProcess | None = None
        self._posix: subprocess.Popen[bytes] | None = None
        self._stage = "CREATED"
        self._stderr = bytearray()
        self._pending = bytearray()
        self._discarding_line = False
        self._stderr_lock = threading.Lock()
        self._closed = False
        self._last_code: int | None = None
        self._cleanup_confirmed = False
        try:
            if os.name == "nt":
                with _SPAWN_LOCK:
                    self._windows = _WindowsProcess(args, cwd, environment)
                self.pid = self._windows.pid
                self.stdin, self.stdout, self.stderr = (
                    self._windows.stdin,
                    self._windows.stdout,
                    self._windows.stderr,
                )
            else:
                if not external_supervision:
                    raise InspectionError("EXTERNAL_PROCESS_SUPERVISION_REQUIRED", 422)
                self._posix = subprocess.Popen(
                    args,
                    cwd=cwd,
                    env=environment,
                    stdin=subprocess.PIPE,
                    stdout=subprocess.PIPE,
                    stderr=subprocess.PIPE,
                    close_fds=True,
                    start_new_session=True,
                    shell=False,
                    bufsize=0,
                )
                self.pid = self._posix.pid
                assert self._posix.stdin and self._posix.stdout and self._posix.stderr
                wrapped: list[IO[bytes]] = []
                try:
                    for original, mode in (
                        (self._posix.stdin, "wb"),
                        (self._posix.stdout, "rb"),
                        (self._posix.stderr, "rb"),
                    ):
                        wrapped.append(_Pipe(os.dup(original.fileno()), mode))
                        original.close()
                except OSError:
                    for stream in wrapped:
                        stream.close()
                    try:
                        getattr(os, "killpg")(self._posix.pid, getattr(signal, "SIGKILL"))
                    except ProcessLookupError:
                        pass
                    self._posix.wait(timeout=1)
                    raise
                self.stdin, self.stdout, self.stderr = wrapped
        except OSError:
            raise InspectionError("PROCESS_START_FAILED", 503) from None

    def mark_stage(self, stage: str) -> None:
        if re.fullmatch(r"[A-Z][A-Z0-9_]{0,63}", stage) is None:
            raise ValueError("invalid process stage")
        self._stage = stage

    def record_stderr(self, data: bytes) -> None:
        # Keep complete bounded lines before redaction, including URLs split across reads.
        with self._stderr_lock:
            offset = 0
            while offset < len(data):
                newline = data.find(b"\n", offset)
                end = len(data) if newline < 0 else newline + 1
                if not self._discarding_line:
                    if len(self._pending) + end - offset > _STDERR_LINE_BYTES:
                        self._pending.clear()
                        self._discarding_line = True
                    else:
                        self._pending.extend(data[offset:end])
                if newline >= 0:
                    line = (
                        b"[stderr line omitted]\n"
                        if self._discarding_line
                        else _redact(self._pending)
                    )
                    self._stderr.extend(line)
                    del self._stderr[:-_STDERR_BYTES]
                    self._pending.clear()
                    self._discarding_line = False
                offset = end

    @property
    def stderr_tail(self) -> str:
        with self._stderr_lock:
            pending = b"[stderr line omitted]" if self._discarding_line else _redact(self._pending)
            # Ignore a split UTF-8 character at the tail boundary to preserve the byte cap.
            return (bytes(self._stderr) + pending)[-_STDERR_BYTES:].decode("utf-8", errors="ignore")

    def poll(self) -> int | None:
        if self._closed:
            return self._last_code
        if self._windows is not None:
            self._last_code = self._windows.poll()
        else:
            assert self._posix is not None
            self._last_code = self._posix.poll()
        return self._last_code

    def set_cpu_limit(self, percent: int) -> None:
        if isinstance(percent, bool) or not isinstance(percent, int) or not 1 <= percent <= 100:
            raise ValueError("CPU percent must be an integer from 1 to 100")
        if self._windows is None or self._closed:
            raise InspectionError("PROCESS_CPU_LIMIT_UNAVAILABLE", 422)
        try:
            self._windows.set_cpu_limit(percent)
        except OSError:
            raise InspectionError("PROCESS_CPU_LIMIT_FAILED", 503) from None

    def wait(self, timeout: float) -> int:
        deadline = time.monotonic() + _seconds(timeout)
        while True:
            result = self.poll()
            if result is not None:
                return result
            if time.monotonic() >= deadline:
                raise TimeoutError("PROCESS_WAIT_TIMEOUT")
            time.sleep(min(0.01, max(0, deadline - time.monotonic())))

    def _tree_empty(self) -> bool:
        if self._windows is not None:
            return self._windows.tree_empty()
        try:
            getattr(os, "killpg")(self.pid, 0)
            return False
        except ProcessLookupError:
            return True

    def _wait_tree(self, timeout: float) -> bool:
        deadline = time.monotonic() + timeout
        while True:
            if self.poll() is not None and self._tree_empty():
                return True
            if time.monotonic() >= deadline:
                return False
            time.sleep(min(0.01, max(0, deadline - time.monotonic())))

    def stop(self, grace_seconds: float = 2, kill_seconds: float = 2) -> ProcessExit:
        _seconds(grace_seconds)
        _seconds(kill_seconds)
        if self._closed or self._cleanup_confirmed:
            return self._exit_result()
        confirmed = False
        try:
            if self._windows is not None:
                self._windows.prepare_stop()
                self._windows.terminate()
            else:
                try:
                    getattr(os, "killpg")(self.pid, signal.SIGTERM)
                except ProcessLookupError:
                    pass
            confirmed = self._wait_tree(grace_seconds)
            if not confirmed:
                if self._windows is not None:
                    self._windows.prepare_stop()
                    self._windows.kill_tree()
                else:
                    try:
                        getattr(os, "killpg")(self.pid, getattr(signal, "SIGKILL"))
                    except ProcessLookupError:
                        pass
                confirmed = self._wait_tree(kill_seconds)
            self.poll()
        except OSError:
            confirmed = False
        self._cleanup_confirmed = confirmed
        return self._exit_result()

    def _exit_result(self) -> ProcessExit:
        return ProcessExit(
            self._last_code,
            -self._last_code if self._last_code is not None and self._last_code < 0 else None,
            self._stage,
            self.stderr_tail,
            self._cleanup_confirmed,
        )

    def close(self) -> None:
        """Close after stop and joining pipe readers; never add another cleanup wait."""
        if self._closed:
            return
        if not self._cleanup_confirmed:
            self.stop(grace_seconds=0, kill_seconds=0)
        if self._windows is not None:
            self._windows.close()
        for stream in (self.stdin, self.stdout, self.stderr):
            stream.close()
        self._closed = True
