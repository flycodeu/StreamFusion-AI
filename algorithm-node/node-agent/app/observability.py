"""Request correlation and bounded logs. Never log request bodies or raw URLs."""

import json
import logging
import re
import sys
import traceback
from contextvars import ContextVar
from datetime import UTC, datetime
from logging.handlers import RotatingFileHandler
from pathlib import Path
from time import monotonic
from typing import Any
from uuid import uuid4

from starlette.responses import JSONResponse
from starlette.types import ASGIApp, Message, Receive, Scope, Send

trace_id: ContextVar[str] = ContextVar("trace_id", default="-")
TRACE_PATTERN = re.compile(r"^[a-f0-9]{32}$")
URL_CREDENTIALS = re.compile(r"(?i)([a-z][a-z0-9+.-]*://)[^/\s@]+@")
BEARER = re.compile(r"(?i)\bBearer\s+[^\s,;\"']+")
SECRETS = re.compile(
    r"""(?ix)(["']?(?:password|passwd|token|secret|api[_-]?key|access[_-]?key|
    access[_-]?token|authorization)["']?\s*[:=]\s*)"""
)


def redact(value: str) -> str:
    # Transport substitutions must not remove escapes inside a quoted secret value.
    value = _redact_secrets(value, quoted_only=True)
    value = URL_CREDENTIALS.sub(r"\1[REDACTED]@", value)
    value = BEARER.sub("Bearer [REDACTED]", value)
    return _redact_secrets(value, quoted_only=False)


def _redact_secrets(value: str, *, quoted_only: bool) -> str:
    safe: list[str] = []
    copied = 0
    while match := SECRETS.search(value, copied):
        start = match.end()
        safe.append(value[copied:start])
        if quoted_only and (start == len(value) or value[start] not in {'"', "'"}):
            copied = start
            continue
        safe.append("[REDACTED]")
        copied = _secret_end(value, start)
        if copied == len(value):
            break
    safe.append(value[copied:])
    return "".join(safe)


def _secret_end(value: str, start: int) -> int:
    """Quoted exception/JSON values may contain escaped quotes or end mid-value."""
    if start == len(value):
        return start
    quote = value[start]
    if quote in {'"', "'"}:
        index = start + 1
        while index < len(value):
            if value[index] == "\\":
                index += 2
            elif value[index] == quote:
                return index + 1
            else:
                index += 1
        return len(value)
    end = start
    while end < len(value) and not value[end].isspace() and value[end] not in ",;&}":
        end += 1
    return end


class TransportLogFilter(logging.Filter):
    def filter(self, record: logging.LogRecord) -> bool:
        # Transport debug/access logs contain raw URLs; use our route-only access log.
        return (
            not (
                record.name.split(".")[0] in {"httpx", "httpcore"}
                and record.levelno < logging.WARNING
            )
            and record.name != "uvicorn.access"
        )


class JsonFormatter(logging.Formatter):
    def __init__(self, service: str) -> None:
        super().__init__()
        self.service = service

    def format(self, record: logging.LogRecord) -> str:
        payload: dict[str, Any] = {
            "timestamp": datetime.fromtimestamp(record.created, UTC)
            .isoformat()
            .replace("+00:00", "Z"),
            "level": record.levelname,
            "service": self.service,
            "traceId": trace_id.get(),
            "message": redact(record.getMessage())[:16384],
        }
        if record.exc_info:
            failure = traceback.TracebackException(*record.exc_info, lookup_lines=False)
            pending = [failure]
            while pending:
                current = pending.pop()
                # Source literals can escape credentials a second time. Keep locations, not source.
                current.stack = traceback.StackSummary.from_list(
                    [
                        traceback.FrameSummary(
                            frame.filename, frame.lineno, frame.name, lookup_line=False, line=""
                        )
                        for frame in current.stack
                    ]
                )
                if current.exc_type is not None and issubclass(current.exc_type, SyntaxError):
                    current.text = ""
                for nested in (current.__cause__, current.__context__):
                    if nested is not None:
                        pending.append(nested)
                if current.exceptions:
                    pending.extend(current.exceptions)
            payload["exception"] = redact("".join(failure.format()))[:16384]
        return json.dumps(payload, ensure_ascii=False)


def configure_logging(
    service: str, work_dir: Path, level: str, max_bytes: int, backups: int
) -> tuple[list[logging.Handler], int]:
    log_dir = work_dir / "logs"
    log_dir.mkdir(parents=True, exist_ok=True)
    console = logging.StreamHandler(sys.stderr)
    file_handler = RotatingFileHandler(
        log_dir / f"{service}.log", maxBytes=max_bytes, backupCount=backups, encoding="utf-8"
    )
    root = logging.getLogger()
    previous = (list(root.handlers), root.level)
    for handler in (console, file_handler):
        handler.setFormatter(JsonFormatter(service))
        handler.addFilter(TransportLogFilter())
    root.handlers = [console, file_handler]
    root.setLevel(level)
    return previous


def restore_logging(previous: tuple[list[logging.Handler], int]) -> None:
    root = logging.getLogger()
    for handler in root.handlers:
        handler.close()
    root.handlers, level = previous
    root.setLevel(level)


def error_body(code: str, message: str) -> dict[str, Any]:
    return {
        "code": code,
        "message": message,
        "data": None,
        "traceId": trace_id.get(),
        "timestamp": datetime.now(UTC).isoformat().replace("+00:00", "Z"),
    }


class RequestLoggingMiddleware:
    def __init__(self, app: ASGIApp) -> None:
        self.app = app

    async def __call__(self, scope: Scope, receive: Receive, send: Send) -> None:
        if scope["type"] != "http":
            await self.app(scope, receive, send)
            return
        headers = dict(scope.get("headers", []))
        incoming = headers.get(b"x-trace-id", b"").decode("latin-1")
        current = incoming if TRACE_PATTERN.fullmatch(incoming) else uuid4().hex
        token = trace_id.set(current)
        started = False
        status = 500
        start = monotonic()

        async def traced_send(message: Message) -> None:
            nonlocal status, started
            if message["type"] == "http.response.start":
                started = True
                status = message["status"]
                response_headers = [
                    (key, value)
                    for key, value in message.get("headers", [])
                    if key.lower() != b"x-trace-id"
                ]
                message["headers"] = response_headers + [(b"x-trace-id", current.encode("ascii"))]
            await send(message)

        try:
            await self.app(scope, receive, traced_send)
        except Exception:
            logging.getLogger("streamfusion.request").exception("Unhandled request exception")
            if started:
                raise
            await JSONResponse(
                error_body("INTERNAL_ERROR", "Internal server error"), status_code=500
            )(scope, receive, traced_send)
        finally:
            route = getattr(scope.get("route"), "path", "<unmatched>")
            logging.getLogger("streamfusion.request").info(
                "method=%s route=%s status=%s durationMs=%.2f",
                scope.get("method", "-"),
                route,
                status,
                (monotonic() - start) * 1000,
            )
            trace_id.reset(token)
