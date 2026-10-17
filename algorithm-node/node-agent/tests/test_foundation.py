import json
import logging
import re
import sys
from pathlib import Path

import pytest
from fastapi.testclient import TestClient
from pydantic import ValidationError

from app.main import create_app
from app.observability import JsonFormatter, redact, trace_id
from app.settings import Settings


@pytest.mark.parametrize(
    "values",
    [
        {"port": 0},
        {"port": 65536},
        {"host": "https://bad-host"},
        {"http_keepalive_seconds": 0},
        {"shutdown_grace_seconds": 121},
        {"log_backups": 0},
        {"log_max_bytes": 1},
    ],
)
def test_invalid_settings_rejected(values: dict[str, object]) -> None:
    with pytest.raises(ValidationError):
        Settings(_env_file=None, **values)


def test_settings_file_env_precedence_and_no_side_effects(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    env = tmp_path / ".env.local"
    env.write_text("SF_AGENT_PORT=18200\n", encoding="utf-8")
    monkeypatch.setenv("SF_AGENT_PORT", "18201")
    work = tmp_path / "not-created"
    settings = Settings(_env_file=env, work_dir=work)
    assert settings.port == 18201
    assert not work.exists()


def test_work_dir_rejects_file(tmp_path: Path) -> None:
    file = tmp_path / "file"
    file.write_text("occupied", encoding="utf-8")
    with pytest.raises(ValidationError):
        Settings(_env_file=None, work_dir=file / "child")


def test_trace_errors_and_safe_request_logs(tmp_path: Path) -> None:
    application = create_app(Settings(_env_file=None, work_dir=tmp_path))

    @application.get("/test-failure", include_in_schema=False)
    def fail() -> None:
        raise RuntimeError("token=do-not-leak rtsp://alice:private@camera/stream")

    @application.get("/test-validation", include_in_schema=False)
    def validate(count: int) -> dict[str, int]:
        return {"count": count}

    with TestClient(application) as client:
        known = "a" * 32
        response = client.get("/health", headers={"X-Trace-Id": known})
        assert response.headers["x-trace-id"] == known
        failed = client.get("/test-failure?password=request-secret")
        assert failed.status_code == 500
        assert failed.json()["code"] == "INTERNAL_ERROR"
        assert failed.json()["traceId"] == failed.headers["x-trace-id"]
        assert "do-not-leak" not in failed.text
        assert "private" not in failed.text
        missing = client.get("/secret-path-token", headers={"X-Trace-Id": "untrusted"})
        assert missing.status_code == 404
        assert re.fullmatch("[a-f0-9]{32}", missing.headers["x-trace-id"])
        assert missing.json()["code"] == "NOT_FOUND"
        invalid = client.get("/test-validation?count=secret-input")
        assert invalid.status_code == 422
        assert invalid.json()["code"] == "VALIDATION_ERROR"
        assert "secret-input" not in invalid.text
        assert "/health" in client.get("/openapi.json").json()["paths"]
        assert client.post("/health").status_code == 405

    log = (tmp_path / "logs" / "node-agent.log").read_text(encoding="utf-8")
    for secret in ("do-not-leak", "alice:private", "request-secret", "secret-path-token"):
        assert secret not in log
    records = [json.loads(line) for line in log.splitlines()]
    assert any(record["traceId"] == known for record in records)
    assert any("Service stopping" in record["message"] for record in records)
    assert trace_id.get() == "-"


def test_docs_can_be_disabled(tmp_path: Path) -> None:
    with TestClient(
        create_app(Settings(_env_file=None, work_dir=tmp_path, docs_enabled=False))
    ) as client:
        assert client.get("/docs").status_code == 404
        assert client.get("/openapi.json").status_code == 404


def test_redaction_covers_structured_values_and_tracebacks() -> None:
    value = '{"password": "hello world", "api_key": "abc"} Bearer xyz rtsp://u:p@host'
    redacted = redact(value)
    for secret in ("hello world", "abc", "xyz", "u:p"):
        assert secret not in redacted
    record = logging.LogRecord("test", logging.ERROR, "", 1, value, (), None)
    assert json.loads(JsonFormatter("test").format(record))["timestamp"].endswith("Z")


@pytest.mark.parametrize(
    ("value", "expected"),
    [
        (
            json.dumps({"password": 'head"tail-secret\\ending', "status": "ok"}),
            '{"password": [REDACTED], "status": "ok"}',
        ),
        (
            "{'token': 'head\\'tail-secret', 'status': 'ok'}",
            "{'token': [REDACTED], 'status': 'ok'}",
        ),
        ('password="head\\"tail-secret with spaces', "password=[REDACTED]"),
        ("token='head\\'tail-secret with spaces\\", "token=[REDACTED]"),
        ('password="', "password=[REDACTED]"),
        ("token='\\", "token=[REDACTED]"),
        (
            "password=first;status=ok token=second&count=2",
            "password=[REDACTED];status=ok token=[REDACTED]&count=2",
        ),
        ("status=ok path=/health", "status=ok path=/health"),
        (
            'authorization="Bearer head\\"tail-secret" status=ok',
            "authorization=[REDACTED] status=ok",
        ),
        ('password="Bearer head\\"tail-secret" status=ok', "password=[REDACTED] status=ok"),
        ("token='Bearer head\\'tail-secret' status=ok", "token=[REDACTED] status=ok"),
        (
            "authorization=Bearer raw-secret status=ok",
            "authorization=[REDACTED] [REDACTED] status=ok",
        ),
    ],
)
def test_redaction_handles_escaped_and_truncated_values(value: str, expected: str) -> None:
    assert redact(value) == expected


def test_formatter_hides_escaped_exception_values_and_long_secrets() -> None:
    try:
        raise RuntimeError("token='head\\'exception-tail'")
    except RuntimeError:
        record = logging.LogRecord(
            "test",
            logging.ERROR,
            "",
            1,
            'password="head\\"message-tail" status=ok',
            (),
            sys.exc_info(),
        )
    payload = json.loads(JsonFormatter("test").format(record))
    assert "message-tail" not in payload["message"]
    assert "status=ok" in payload["message"]
    assert "exception-tail" not in payload["exception"]
    assert "RuntimeError" in payload["exception"]
    assert redact('password="' + '\\"' * 20000 + 'tail-secret" status=ok') == (
        "password=[REDACTED] status=ok"
    )


def test_formatter_keeps_chain_and_group_locations_without_source_literals() -> None:
    try:
        try:
            raise ValueError("password='head\\'cause-tail'")
        except ValueError as cause:
            raise ExceptionGroup("group", [RuntimeError('token="head\\"group-tail"')]) from cause
    except ExceptionGroup:
        record = logging.LogRecord("test", logging.ERROR, "", 1, "rejected", (), sys.exc_info())
    exception = json.loads(JsonFormatter("test").format(record))["exception"]
    assert "cause-tail" not in exception
    assert "group-tail" not in exception
    assert "ValueError" in exception
    assert "ExceptionGroup" in exception
    assert "RuntimeError" in exception
    assert "test_foundation.py" in exception
    assert "direct cause" in exception


def test_formatter_omits_syntax_error_source_excerpt() -> None:
    try:
        raise SyntaxError("invalid syntax", ("plugin.py", 7, 4, 'x="source-only-secret"'))
    except SyntaxError:
        record = logging.LogRecord("test", logging.ERROR, "", 1, "rejected", (), sys.exc_info())
    exception = json.loads(JsonFormatter("test").format(record))["exception"]
    assert "source-only-secret" not in exception
    assert 'File "plugin.py", line 7' in exception
    assert "SyntaxError: invalid syntax" in exception
