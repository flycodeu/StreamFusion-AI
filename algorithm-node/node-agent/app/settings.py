"""Validated process settings. Loading does not create directories or contact services."""

import ipaddress
import os
from pathlib import Path
from typing import Literal

from pydantic import Field, field_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

PROJECT_DIR = Path(__file__).resolve().parents[1]


class Settings(BaseSettings):
    model_config = SettingsConfigDict(
        env_prefix="SF_AGENT_",
        env_file=PROJECT_DIR / ".env.local",
        env_file_encoding="utf-8",
        hide_input_in_errors=True,
        extra="forbid",
    )

    host: str = "127.0.0.1"
    port: int = Field(default=8100, ge=1, le=65535)
    work_dir: Path = PROJECT_DIR / ".run"
    http_keepalive_seconds: int = Field(default=5, ge=1, le=120)
    shutdown_grace_seconds: int = Field(default=15, ge=1, le=120)
    log_level: Literal["DEBUG", "INFO", "WARNING", "ERROR"] = "INFO"
    log_max_bytes: int = Field(default=10 * 1024 * 1024, ge=1024, le=100 * 1024 * 1024)
    log_backups: int = Field(default=5, ge=1, le=10)
    docs_enabled: bool = True

    @field_validator("host")
    @classmethod
    def valid_host(cls, value: str) -> str:
        if value == "localhost":
            return value
        ipaddress.ip_address(value)
        return value

    @field_validator("work_dir")
    @classmethod
    def valid_work_dir(cls, value: Path) -> Path:
        path = value.expanduser()
        if not path.is_absolute():
            path = PROJECT_DIR / path
        path = path.resolve()
        ancestor = path
        while not ancestor.exists():
            ancestor = ancestor.parent
        if not ancestor.is_dir() or not os.access(ancestor, os.W_OK):
            raise ValueError("work_dir must be a directory with a writable existing ancestor")
        return path
