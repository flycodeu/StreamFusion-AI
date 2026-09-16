"""node-agent process skeleton. No platform sync or inference is implemented."""

from typing import Literal

from fastapi import FastAPI
from pydantic import BaseModel

app = FastAPI(title="StreamFusion node-agent", version="0.1.0")


class HealthResponse(BaseModel):
    status: Literal["UP"] = "UP"
    service: Literal["node-agent"] = "node-agent"
    mode: Literal["skeleton"] = "skeleton"


@app.get("/health", response_model=HealthResponse)
def health() -> HealthResponse:
    """Report process availability only, not model or video readiness."""
    return HealthResponse()
