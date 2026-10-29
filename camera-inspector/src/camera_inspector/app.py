"""Authenticated, bounded HTTP control for the independent P0 inspection project."""

import asyncio
import logging
import re
from collections.abc import AsyncIterator, Awaitable, Callable
from contextlib import asynccontextmanager
from datetime import UTC, datetime
from secrets import compare_digest
from uuid import uuid4

from fastapi import FastAPI, Request
from pydantic import ValidationError
from starlette.exceptions import HTTPException
from starlette.responses import JSONResponse, Response

from camera_inspector.control import Assessor, ControlRepository, ControlService
from camera_inspector.errors import InspectionError
from camera_inspector.settings import Settings
from camera_inspector.types import ControlCommand

_TRACE = re.compile(r"^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$")
_LOGGER = logging.getLogger("camera_inspector.control")


def _response(request: Request, code: str, status: int, data: object = None) -> JSONResponse:
    return JSONResponse(
        {
            "code": code,
            "data": data,
            "traceId": request.state.trace_id,
            "timestamp": datetime.now(UTC).isoformat().replace("+00:00", "Z"),
        },
        status_code=status,
        headers={"X-Trace-Id": request.state.trace_id},
    )


def create_app(
    settings: Settings | None = None,
    repository: ControlRepository | None = None,
    assessor: Assessor | None = None,
    *,
    boot_id: str | None = None,
) -> FastAPI:
    config = settings if settings is not None else Settings()
    boot = boot_id or uuid4().hex
    if repository is None:
        from camera_inspector.db import DatabaseConfig, make_connection_factory
        from camera_inspector.repository import Repository

        connection = make_connection_factory(
            DatabaseConfig(
                host=config.db_host,
                port=config.db_port,
                user=config.db_user,
                password=config.db_password.get_secret_value(),
                database=config.db_database,
                timeout_seconds=config.db_timeout_seconds,
                ssl_ca=str(config.db_ssl_ca) if config.db_ssl_ca is not None else None,
                allow_insecure_loopback=config.db_allow_insecure_loopback,
            )
        )
        repository = Repository(connection, node_id=config.node_id, boot_id=boot)
    service = ControlService(
        repository,
        config.node_id,
        boot,
        assessor=assessor,
        io_timeout_seconds=config.io_timeout_seconds,
        admission_timeout_seconds=config.admission_timeout_seconds,
    )

    @asynccontextmanager
    async def lifespan(_application: FastAPI) -> AsyncIterator[None]:
        try:
            yield
        finally:
            service.close()

    application = FastAPI(
        title="Camera Inspector",
        version="0.1.0",
        lifespan=lifespan,
        docs_url=None,
        redoc_url=None,
        openapi_url=None,
    )
    application.state.control_service = service

    @application.middleware("http")
    async def authenticate(
        request: Request, call_next: Callable[[Request], Awaitable[Response]]
    ) -> Response:
        incoming = request.headers.get("x-trace-id", "")
        request.state.trace_id = incoming if _TRACE.fullmatch(incoming) else uuid4().hex
        credentials = request.headers.getlist("authorization")
        scheme, _, supplied = (credentials[0] if len(credentials) == 1 else "").partition(" ")
        expected = config.auth_token.get_secret_value().encode("ascii")
        if scheme.lower() != "bearer" or not compare_digest(supplied.encode("utf-8"), expected):
            return _response(request, "UNAUTHORIZED", 401)
        try:
            response = await call_next(request)
            response.headers["X-Trace-Id"] = request.state.trace_id
            return response
        except Exception as error:
            _LOGGER.error(
                "request failed traceId=%s errorType=%s",
                request.state.trace_id,
                type(error).__name__,
            )
            return _response(request, "INTERNAL_ERROR", 500)

    @application.exception_handler(InspectionError)
    async def inspection_error(request: Request, error: InspectionError) -> JSONResponse:
        _LOGGER.warning("control rejected traceId=%s code=%s", request.state.trace_id, error.code)
        return _response(request, error.code, error.status_code)

    @application.exception_handler(HTTPException)
    async def route_error(request: Request, error: HTTPException) -> JSONResponse:
        code = {404: "NOT_FOUND", 405: "METHOD_NOT_ALLOWED"}.get(error.status_code, "HTTP_ERROR")
        return _response(request, code, error.status_code)

    @application.get("/inspection/v1/status")
    async def status(request: Request) -> JSONResponse:
        return _response(
            request,
            "SUCCESS",
            200,
            {
                "contractVersion": "1.0",
                "serviceVersion": application.version,
                "nodeId": config.node_id,
                "bootId": boot,
                "mode": "P0_CONTRACT",
                "capabilityStatus": "NOT_VERIFIED",
                "detectors": [],
                "availableSlots": 0,
            },
        )

    @application.post("/inspection/v1/control", status_code=202)
    async def control(request: Request) -> JSONResponse:
        lengths = request.headers.getlist("content-length")
        length = lengths[0] if lengths else None
        if len(lengths) > 1 or (
            length is not None
            and (
                len(length) > 20
                or not length.isascii()
                or not length.isdigit()
                or int(length) > config.request_max_bytes
            )
        ):
            raise InspectionError("REQUEST_TOO_LARGE", 413)
        body = bytearray()
        try:
            async with asyncio.timeout(config.request_timeout_seconds):
                async for chunk in request.stream():
                    if len(body) + len(chunk) > config.request_max_bytes:
                        raise InspectionError("REQUEST_TOO_LARGE", 413)
                    body.extend(chunk)
        except TimeoutError:
            raise InspectionError("REQUEST_TIMEOUT", 408) from None
        try:
            command = ControlCommand.model_validate_json(bytes(body))
        except ValidationError:
            raise InspectionError("VALIDATION_ERROR", 422) from None
        # Once valid, the durable command's trace is authoritative across HTTP, DB and logs.
        request.state.trace_id = command.trace_id
        snapshot = await service.apply(command)
        _LOGGER.info(
            "control acknowledged traceId=%s controlId=%s controlSeq=%s state=%s",
            command.trace_id,
            command.control_id,
            command.control_seq,
            snapshot.observed_state,
        )
        return _response(request, "SUCCESS", 202, snapshot.model_dump(mode="json", by_alias=True))

    return application
