"""node-agent process foundation; no platform sync or inference."""

import logging
from collections.abc import AsyncIterator
from contextlib import asynccontextmanager
from typing import Literal

from fastapi import FastAPI, Request
from fastapi.exceptions import RequestValidationError
from pydantic import BaseModel
from starlette.exceptions import HTTPException
from starlette.responses import JSONResponse

from app.observability import (
    RequestLoggingMiddleware,
    configure_logging,
    error_body,
    restore_logging,
)
from app.settings import Settings


class HealthResponse(BaseModel):
    status: Literal["UP"] = "UP"
    service: Literal["node-agent"] = "node-agent"
    mode: Literal["skeleton"] = "skeleton"


def create_app(settings: Settings | None = None) -> FastAPI:
    config = settings if settings is not None else Settings()

    @asynccontextmanager
    async def lifespan(application: FastAPI) -> AsyncIterator[None]:
        previous = configure_logging(
            "node-agent",
            config.work_dir,
            config.log_level,
            config.log_max_bytes,
            config.log_backups,
        )
        logger = logging.getLogger("streamfusion.lifecycle")
        logger.info("Service started; mode=skeleton")
        try:
            yield
        finally:
            logger.info("Service stopping")
            restore_logging(previous)

    application = FastAPI(
        title="StreamFusion node-agent",
        version="0.1.0",
        lifespan=lifespan,
        docs_url="/docs" if config.docs_enabled else None,
        redoc_url=None,
        openapi_url="/openapi.json" if config.docs_enabled else None,
    )
    application.add_middleware(RequestLoggingMiddleware)

    @application.exception_handler(HTTPException)
    async def http_error(request: Request, exc: HTTPException) -> JSONResponse:
        codes = {404: "NOT_FOUND", 405: "METHOD_NOT_ALLOWED"}
        return JSONResponse(
            error_body(
                codes.get(exc.status_code, "HTTP_ERROR"),
                {404: "Resource not found", 405: "Method not allowed"}.get(
                    exc.status_code, "Request rejected"
                ),
            ),
            status_code=exc.status_code,
            headers=exc.headers,
        )

    @application.exception_handler(RequestValidationError)
    async def validation_error(request: Request, exc: RequestValidationError) -> JSONResponse:
        return JSONResponse(
            error_body("VALIDATION_ERROR", "Request validation failed"), status_code=422
        )

    @application.get("/health", response_model=HealthResponse, tags=["Health"])
    def health() -> HealthResponse:
        return HealthResponse()

    return application
