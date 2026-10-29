"""Explicit MySQL connection settings; runtime never creates or migrates tables."""

from __future__ import annotations

import ipaddress
import ssl
from collections.abc import Callable
from dataclasses import dataclass, field
from pathlib import Path
from typing import TYPE_CHECKING

import pymysql
from pymysql.connections import Connection
from pymysql.constants import CLIENT
from pymysql.cursors import DictCursor

if TYPE_CHECKING:
    _ConnectionBase = Connection[DictCursor]
else:
    _ConnectionBase = Connection


class _StrictTlsConnection(_ConnectionBase):
    server_capabilities: int

    def _request_authentication(self) -> None:
        # PyMySQL 1.1.2 otherwise permits plaintext fallback. Its private handshake hook
        # runs before authentication bytes are written; driver upgrades must retest it.
        if self.ssl and not self.server_capabilities & CLIENT.SSL:
            raise pymysql.OperationalError(2026, "DATABASE_TLS_UNAVAILABLE")
        super()._request_authentication()  # type: ignore[misc]


@dataclass(frozen=True)
class DatabaseConfig:
    host: str
    port: int
    user: str
    password: str = field(repr=False)
    database: str
    timeout_seconds: int = 3
    ssl_ca: str | None = None
    allow_insecure_loopback: bool = False

    def __post_init__(self) -> None:
        if not 1 <= self.port <= 65535 or not 1 <= self.timeout_seconds <= 30:
            raise ValueError("invalid database port or timeout")
        if not self.host or not self.user or not self.database:
            raise ValueError("database host, user and name are required")
        if self.ssl_ca is None:
            try:
                loopback = ipaddress.ip_address(self.host).is_loopback
            except ValueError:
                loopback = False
            if not (self.allow_insecure_loopback and loopback):
                raise ValueError("database TLS is required outside explicit loopback tests")
        elif not Path(self.ssl_ca).is_file():
            raise ValueError("database TLS CA file is unavailable")


def make_connection_factory(config: DatabaseConfig) -> Callable[[], Connection[DictCursor]]:
    context: ssl.SSLContext | None = None
    if config.ssl_ca is not None:
        context = ssl.create_default_context(cafile=config.ssl_ca)

    def connect() -> Connection[DictCursor]:
        connection = _StrictTlsConnection(
            host=config.host,
            port=config.port,
            user=config.user,
            password=config.password,
            database=config.database,
            charset="utf8mb4",
            cursorclass=DictCursor,
            connect_timeout=config.timeout_seconds,
            read_timeout=config.timeout_seconds,
            write_timeout=config.timeout_seconds,
            autocommit=False,
            ssl=context,
        )
        try:
            with connection.cursor() as cursor:
                cursor.execute("SET SESSION time_zone = '+00:00'")
                cursor.execute("SET SESSION TRANSACTION ISOLATION LEVEL READ COMMITTED")
                cursor.execute(
                    "SET SESSION innodb_lock_wait_timeout = %s", (config.timeout_seconds,)
                )
                cursor.execute(
                    "SET SESSION max_execution_time = %s", (config.timeout_seconds * 1000,)
                )
            return connection
        except BaseException:
            connection.close()
            raise

    return connect
