"""Real child-process tests; these never connect to cameras or launch media servers."""

import json
import os
import subprocess
import sys
import threading
import time
from pathlib import Path

import pytest

from camera_inspector.errors import InspectionError
from camera_inspector.processes import ManagedProcess, _Pipe


def start_python(source: str) -> ManagedProcess:
    return ManagedProcess(
        [sys.executable, "-u", "-c", source], external_supervision=os.name != "nt"
    )


def test_streams_and_natural_exit():
    process = start_python(
        "import sys; print(sys.stdin.buffer.read(4).decode()); print('detail', file=sys.stderr)"
    )
    try:
        process.stdin.write(b"ping")
        process.stdin.flush()
        assert process.stdout.read(32).strip() == b"ping"
        error = process.stderr.read(32)
        process.record_stderr(error)
        assert process.wait(5) == 0
        process.mark_stage("OBSERVED")
        result = process.stop(grace_seconds=0.1, kill_seconds=1)
        assert result.returncode == 0
        assert result.signal is None
        assert result.stage == "OBSERVED"
        assert result.stderr_tail.strip() == "detail"
        assert result.cleanup_confirmed
    finally:
        process.close()


def test_wait_is_bounded_and_stop_is_idempotent():
    process = start_python("import time; time.sleep(60)")
    try:
        with pytest.raises(TimeoutError, match="PROCESS_WAIT_TIMEOUT"):
            process.wait(0.02)
        first = process.stop(grace_seconds=0.2, kill_seconds=1)
        second = process.stop(grace_seconds=0.1, kill_seconds=1)
        assert first.cleanup_confirmed and second.cleanup_confirmed
        assert first.returncode == second.returncode
        process.close()
        assert process.stop() == second
        assert process.poll() == second.returncode
    finally:
        process.close()


def test_close_never_waits_for_a_blocked_pipe_reader():
    read_fd, write_fd = os.pipe()
    stream = _Pipe(read_fd, "rb")
    started = threading.Event()

    def read_pipe():
        started.set()
        try:
            stream.read(1)
        except (OSError, ValueError):
            pass

    reader = threading.Thread(target=read_pipe, daemon=True)
    reader.start()
    try:
        assert started.wait(1)
        deadline = time.monotonic() + 1
        while stream._active_operations == 0 and time.monotonic() < deadline:
            time.sleep(0.001)
        assert stream._active_operations == 1
        before = time.monotonic()
        stream.close()
        assert time.monotonic() - before < 0.2
        assert reader.is_alive()
        with pytest.raises(ValueError, match="closing process pipe"):
            stream.read(1)
    finally:
        os.close(write_fd)
        reader.join(timeout=2)
        stream.close()
    assert not reader.is_alive()
    assert stream.closed


def test_only_allowlisted_environment_is_inherited(monkeypatch):
    monkeypatch.setenv("SF_INSPECTOR_DB_PASSWORD", "must-not-reach-child")
    monkeypatch.setenv("AWS_SECRET_ACCESS_KEY", "must-not-reach-child-either")
    process = start_python("import json,os; print(json.dumps(dict(os.environ)))")
    try:
        output = process.stdout.read()
        assert process.wait(5) == 0
        environment = json.loads(output)
        assert "SF_INSPECTOR_DB_PASSWORD" not in environment
        assert "AWS_SECRET_ACCESS_KEY" not in environment
    finally:
        process.close()


@pytest.mark.parametrize("key", ["SF_INSPECTOR_DB_PASSWORD", "AWS_SECRET_ACCESS_KEY", "PYTHONPATH"])
def test_credentials_cannot_be_added_to_child_environment(key):
    with pytest.raises(InspectionError, match="PROCESS_ENV_NOT_ALLOWED"):
        ManagedProcess([sys.executable, "-c", "pass"], env={key: "private"})


def test_stderr_tail_redacts_split_urls_tokens_and_discards_oversize_lines():
    process = start_python("pass")
    try:
        for chunk in (
            b"opening rtsp://user:se",
            b"cret@example.test/video?token=private\n",
            b"Authorization: Bearer full-secret-value\n",
            b"token=another-secret\n",
            b'{"access_token":"json-secret"}\n',
            b"x" * 3000 + b"secret-tail\n",
        ):
            process.record_stderr(chunk)
        result = process.stderr_tail
        assert "secret" not in result.replace("redacted-secret", "")
        assert "example.test" not in result
        assert "[redacted-url]" in result
        assert "[stderr line omitted]" in result
        process.record_stderr(b"safe\n" * 10000)
        assert len(process.stderr_tail.encode()) <= 16384
        process.record_stderr(("\u753b\u9762" * 300 + "\n").encode() * 20)
        assert len(process.stderr_tail.encode()) <= 16384
    finally:
        process.close()


def test_invalid_stage_command_and_timeout_are_rejected():
    with pytest.raises(InspectionError, match="INVALID_PROCESS_ARGUMENTS"):
        ManagedProcess("python -c pass")
    with pytest.raises(InspectionError, match="INVALID_PROCESS_ARGUMENTS"):
        ManagedProcess(["python", "-c", "pass"])
    process = start_python("pass")
    try:
        with pytest.raises(ValueError, match="invalid process stage"):
            process.mark_stage("rtsp://credentials")
        with pytest.raises(ValueError, match="timeout"):
            process.stop(grace_seconds=float("inf"))
    finally:
        process.close()


@pytest.mark.skipif(os.name != "nt", reason="Windows atomic job containment")
def test_stop_confirms_stubborn_descendant_has_exited():
    process = start_python(
        "import subprocess,sys,time; "
        "p=subprocess.Popen([sys.executable,'-c','import time; time.sleep(60)']); "
        "print(p.pid,flush=True); time.sleep(60)"
    )
    try:
        child_pid = int(process.stdout.readline())
        result = process.stop(grace_seconds=0.05, kill_seconds=2)
        assert result.cleanup_confirmed
        assert not pid_running(child_pid)
    finally:
        process.close()


@pytest.mark.skipif(os.name != "nt", reason="Windows job CPU cap")
def test_cpu_limit_is_applied_to_the_same_job():
    process = start_python("import time; time.sleep(60)")
    try:
        process.set_cpu_limit(10)
        with pytest.raises(ValueError, match="CPU percent"):
            process.set_cpu_limit(0)
        assert process.stop(grace_seconds=0.1, kill_seconds=1).cleanup_confirmed
    finally:
        process.close()


@pytest.mark.skipif(os.name != "nt", reason="Windows job incomplete member list")
def test_incomplete_member_snapshot_never_claims_confirmed_cleanup():
    process = start_python("import time; time.sleep(60)")
    try:
        assert process._windows is not None
        process._windows._members_complete = False
        result = process.stop(grace_seconds=0.01, kill_seconds=0.05)
        assert not result.cleanup_confirmed
        assert process.wait(1) is not None
        process.close()
        assert not process.stop().cleanup_confirmed
    finally:
        process.close()


def pid_running(pid: int) -> bool:
    import ctypes
    from ctypes import wintypes

    api = ctypes.WinDLL("kernel32", use_last_error=True)
    api.OpenProcess.argtypes = [wintypes.DWORD, wintypes.BOOL, wintypes.DWORD]
    api.OpenProcess.restype = wintypes.HANDLE
    api.WaitForSingleObject.argtypes = [wintypes.HANDLE, wintypes.DWORD]
    api.WaitForSingleObject.restype = wintypes.DWORD
    api.CloseHandle.argtypes = [wintypes.HANDLE]
    handle = api.OpenProcess(0x00100000, False, pid)
    if not handle:
        return False
    try:
        return api.WaitForSingleObject(handle, 0) == 258
    finally:
        api.CloseHandle(handle)


@pytest.mark.skipif(os.name != "nt", reason="Windows kill-on-job-close")
def test_killing_coordinator_reaps_child_and_grandchild(tmp_path: Path):
    """Kill the coordinator itself: no Python finally or taskkill can perform cleanup."""
    state = tmp_path / "processes.json"
    child = (
        "import subprocess,sys,time; "
        "p=subprocess.Popen([sys.executable,'-c','import time; time.sleep(60)']); "
        "print(p.pid,flush=True); time.sleep(60)"
    )
    coordinator = (
        "import json,sys,time; from pathlib import Path; "
        "from camera_inspector.processes import ManagedProcess; "
        f"p=ManagedProcess([sys.executable,'-u','-c',{child!r}]); "
        "g=int(p.stdout.readline()); "
        f"Path({str(state)!r}).write_text(json.dumps([p.pid,g])); "
        "time.sleep(60)"
    )
    # This process is intentionally outside ManagedProcess: the test kills its job owner.
    owner = subprocess.Popen(
        [sys.executable, "-c", coordinator],
        stdin=subprocess.DEVNULL,
        stdout=subprocess.DEVNULL,
        stderr=subprocess.PIPE,
        creationflags=subprocess.CREATE_NO_WINDOW,
    )
    ids = []
    try:
        deadline = time.monotonic() + 5
        while not state.exists() and time.monotonic() < deadline:
            assert owner.poll() is None, "coordinator exited before recording its children"
            time.sleep(0.02)
        assert state.exists()
        ids = json.loads(state.read_text())
        assert all(pid_running(pid) for pid in ids)
        owner.kill()
        owner.wait(timeout=3)
        deadline = time.monotonic() + 3
        while any(pid_running(pid) for pid in ids) and time.monotonic() < deadline:
            time.sleep(0.02)
        assert not any(pid_running(pid) for pid in ids)
    finally:
        if owner.poll() is None:
            owner.kill()
            owner.wait(timeout=3)
        if owner.stderr:
            owner.stderr.close()


@pytest.mark.skipif(os.name == "nt", reason="Linux external supervision contract")
def test_linux_rejects_launch_without_external_supervision():
    with pytest.raises(InspectionError, match="EXTERNAL_PROCESS_SUPERVISION_REQUIRED"):
        ManagedProcess([sys.executable, "-c", "pass"])
