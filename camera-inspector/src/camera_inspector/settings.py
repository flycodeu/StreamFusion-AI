"""Independent node settings; loading never creates files or opens a database."""

import ipaddress
from pathlib import Path
from typing import Literal, Self

from pydantic import Field, SecretStr, field_validator, model_validator
from pydantic_settings import BaseSettings, SettingsConfigDict

from camera_inspector.types import Identifier


class Settings(BaseSettings):
    model_config = SettingsConfigDict(
        env_prefix="SF_INSPECTOR_",
        env_file=".env.local",
        env_file_encoding="utf-8",
        hide_input_in_errors=True,
        extra="forbid",
    )

    node_id: Identifier
    auth_token: SecretStr
    host: str = "0.0.0.0"
    port: int = Field(default=8120, ge=1, le=65535)
    db_host: str = "127.0.0.1"
    db_port: int = Field(default=3306, ge=1, le=65535)
    db_user: str = Field(min_length=1, max_length=64)
    db_password: SecretStr
    db_database: str = Field(pattern=r"^[A-Za-z][A-Za-z0-9_]{0,63}$")
    db_timeout_seconds: int = Field(default=3, ge=1, le=30)
    db_ssl_ca: Path | None = None
    db_allow_insecure_loopback: bool = False
    request_max_bytes: int = Field(default=8192, ge=512, le=65536)
    request_timeout_seconds: float = Field(default=5, ge=0.1, le=30)
    io_timeout_seconds: float = Field(default=5, ge=0.05, le=120)
    admission_timeout_seconds: float = Field(default=10, ge=0.05, le=300)
    tls_certfile: Path | None = None
    tls_keyfile: Path | None = None
    trusted_proxy: bool = False
    work_dir: Path = Path(".run")
    log_level: Literal["DEBUG", "INFO", "WARNING", "ERROR"] = "INFO"
    log_max_bytes: int = Field(default=5 * 1024 * 1024, ge=1024, le=100 * 1024 * 1024)
    log_backups: int = Field(default=3, ge=1, le=10)

    @field_validator("auth_token")
    @classmethod
    def valid_token(cls, value: SecretStr) -> SecretStr:
        token = value.get_secret_value()
        if not 32 <= len(token) <= 256 or not token.isascii() or any(c.isspace() for c in token):
            raise ValueError("auth_token must contain 32 to 256 non-space ASCII characters")
        return value

    @field_validator("host")
    @classmethod
    def valid_host(cls, value: str) -> str:
        if value != "localhost":
            ipaddress.ip_address(value)
        return value

    @field_validator("work_dir", "db_ssl_ca", "tls_certfile", "tls_keyfile")
    @classmethod
    def absolute_path(cls, value: Path | None) -> Path | None:
        if value is None:
            return None
        path = value.expanduser()
        # Launch from the documented project/config directory, also for wheel installs.
        return (path if path.is_absolute() else Path.cwd() / path).resolve()

    @model_validator(mode="after")
    def paired_tls(self) -> Self:
        if (self.tls_certfile is None) != (self.tls_keyfile is None):
            raise ValueError("tls_certfile and tls_keyfile must be configured together")
        return self
