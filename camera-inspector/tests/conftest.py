"""Real MySQL fixtures, restricted to the instance owned by the isolated test script."""

import json
import os
import re
import secrets
from collections.abc import Callable, Iterator
from pathlib import Path
from typing import TYPE_CHECKING
from uuid import uuid4

import pymysql
import pytest
from pymysql.connections import Connection
from pymysql.cursors import DictCursor

if TYPE_CHECKING:
    from camera_inspector.db import DatabaseConfig


WORKSPACE = Path(__file__).resolve().parents[2]
RUN_ROOT = WORKSPACE / ".run" / "inspection-p0"
DDL_ROOT = WORKSPACE / "platform-api" / "sql" / "业务" / "巡检P0"
STORAGE_DDL_ROOT = DDL_ROOT.parent
STORAGE_DDL_NAMES = ("图床实例表.sql", "图床范围表.sql", "图床默认位置表.sql")
DDL_NAMES = (
    "01-control.sql",
    "02-budget.sql",
    "03-job.sql",
    "04-run.sql",
    "05-result.sql",
    "06-claim-receipt.sql",
    "07-evidence.sql",
)
WORKER_GRANTS = {
    "inspection_control": (
        "SELECT",
        "UPDATE (accepted_seq,accepted_boot_id,accepted_expires_at,command_digest,"
        "observed_state,available_slots,reason_code,trace_id,claim_after_deadline,"
        "claim_after_due,claim_after_job_id)",
    ),
    "inspection_budget": ("SELECT", "UPDATE (used_slots)"),
    "inspection_job": ("SELECT", "UPDATE (state,assignment_epoch)"),
    "inspection_run": (
        "SELECT",
        "INSERT (run_id,job_id,assignment_epoch,claim_request_id,control_id,control_seq,"
        "node_id,boot_id,spec_json,budget_keys_json,claimed_at,lease_expires_at,trace_id)",
        "UPDATE (state,started_at,lease_expires_at,resources_released,released_at)",
    ),
    "inspection_result": (
        "SELECT",
        "INSERT (run_id,detector_key,result_digest,payload_json,submitted_at)",
    ),
    "inspection_claim_receipt": (
        "SELECT",
        "INSERT (node_id,boot_id,request_id,control_id,control_seq,run_id,created_at)",
    ),
}


def _admin_connection(port: int) -> Connection:
    return pymysql.connect(
        host="127.0.0.1",
        port=port,
        user="root",
        password="",
        charset="utf8mb4",
        cursorclass=DictCursor,
        autocommit=True,
        connect_timeout=3,
        read_timeout=3,
        write_timeout=3,
        local_infile=False,
    )


def _verify_instance(connection: Connection, port: int, datadir: Path) -> None:
    with connection.cursor() as cursor:
        cursor.execute("SELECT @@port AS port, @@datadir AS datadir, @@version AS version")
        actual = cursor.fetchone()
    if not isinstance(actual, dict):
        pytest.fail("Isolated MySQL identity query returned an invalid response")
    if actual["port"] != port or Path(actual["datadir"]).resolve() != datadir:
        pytest.fail("Refusing a MySQL server outside the isolated test data directory")
    if not str(actual["version"]).startswith("8."):
        pytest.fail("The contract tests require the isolated MySQL 8 server")


@pytest.fixture(scope="session")
def mysql_instance() -> tuple[int, Path]:
    names = (
        "SF_INSPECTOR_MYSQL_TEST",
        "SF_INSPECTOR_TEST_DB_PORT",
        "SF_INSPECTOR_TEST_DB_TOKEN",
        "SF_INSPECTOR_TEST_DB_MARKER",
    )
    values = {name: os.environ.get(name) for name in names}
    if not any(values.values()):
        pytest.skip("Run scripts/test-isolated-mysql.ps1 to enable isolated MySQL tests")
    if values[names[0]] != "1" or not all(values.values()):
        pytest.fail("Incomplete isolated MySQL test configuration; use the test script")
    try:
        port = int(values[names[1]] or "")
        marker = Path(values[names[3]] or "").resolve(strict=True)
        record = json.loads(marker.read_text(encoding="utf-8-sig"))
        datadir = Path(record["datadir"]).resolve(strict=True)
    except (ValueError, OSError, TypeError, KeyError):
        pytest.fail("Invalid isolated MySQL marker; use the test script")
    if not 49152 <= port <= 65535 or not marker.is_relative_to(RUN_ROOT.resolve()):
        pytest.fail("Refusing a MySQL port or marker outside the isolated test scope")
    if datadir != marker.parent / "data" or not marker.parent.name.startswith("mysql-"):
        pytest.fail("Refusing an unrecognized isolated MySQL data directory")
    if record.get("port") != port or not secrets.compare_digest(
        str(record.get("token", "")), values[names[2]] or ""
    ):
        pytest.fail("Isolated MySQL marker does not match this test run")
    with _admin_connection(port) as connection:
        _verify_instance(connection, port, datadir)
    return port, datadir


@pytest.fixture
def mysql_config(mysql_instance: tuple[int, Path]) -> "DatabaseConfig":
    from camera_inspector.db import DatabaseConfig

    port, datadir = mysql_instance
    database = "inspection_p0_test_" + uuid4().hex
    assert re.fullmatch(r"inspection_p0_test_[0-9a-f]{32}", database)
    actual_names = {path.name for path in DDL_ROOT.glob("*.sql")}
    if actual_names != set(DDL_NAMES):
        pytest.fail("The isolated test schema must contain exactly the seven P0 table sources")
    with _admin_connection(port) as connection:
        # Verify again before every schema creation: never write based only on a cached identity.
        _verify_instance(connection, port, datadir)
        with connection.cursor() as cursor:
            cursor.execute(
                f"CREATE DATABASE `{database}` CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"
            )
        connection.select_db(database)
        with connection.cursor() as cursor:
            cursor.execute("SET time_zone = '+00:00'")
            for root, name in (
                *((STORAGE_DDL_ROOT, name) for name in STORAGE_DDL_NAMES),
                *((DDL_ROOT, name) for name in DDL_NAMES),
            ):
                source = (root / name).read_text(encoding="utf-8-sig")
                # The isolated schema includes only these fixed sources; no routines.
                body = "\n".join(
                    line for line in source.splitlines() if not line.lstrip().startswith("--")
                )
                for statement in body.split(";"):
                    if statement.strip():
                        cursor.execute(statement)
    return DatabaseConfig(
        host="127.0.0.1",
        port=port,
        user="root",
        password="",
        database=database,
        timeout_seconds=3,
        allow_insecure_loopback=True,
    )


def _tracked_connections(config: "DatabaseConfig") -> Iterator[Callable[[], Connection]]:
    from camera_inspector.db import make_connection_factory

    create_connection = make_connection_factory(config)
    connections: list[Connection] = []

    def connect() -> Connection:
        if len(connections) >= 256:
            pytest.fail("One contract test exceeded its connection budget")
        connection = create_connection()
        connections.append(connection)
        return connection

    try:
        yield connect
    finally:
        for connection in connections:
            if connection.open:
                connection.close()


@pytest.fixture
def mysql_connect(mysql_config: "DatabaseConfig") -> Iterator[Callable[[], Connection]]:
    yield from _tracked_connections(mysql_config)


@pytest.fixture
def mysql_factory(mysql_connect: Callable[[], Connection]) -> Callable[[], Connection]:
    return mysql_connect


@pytest.fixture
def mysql_worker_config(
    mysql_config: "DatabaseConfig",
    mysql_instance: tuple[int, Path],
    record_testsuite_property: Callable[[str, object], None],
) -> "DatabaseConfig":
    from dataclasses import replace

    port, datadir = mysql_instance
    user = "inspection_w_" + uuid4().hex[:16]
    password = secrets.token_urlsafe(32)
    assert re.fullmatch(r"inspection_w_[0-9a-f]{16}", user)
    with _admin_connection(port) as connection:
        _verify_instance(connection, port, datadir)
        with connection.cursor() as cursor:
            cursor.execute("CREATE USER %s@'127.0.0.1' IDENTIFIED BY %s", (user, password))
            for table, grants in WORKER_GRANTS.items():
                cursor.execute(
                    f"GRANT {', '.join(grants)} ON `{mysql_config.database}`.`{table}` "
                    "TO %s@'127.0.0.1'",
                    (user,),
                )
            cursor.execute("SHOW GRANTS FOR %s@'127.0.0.1'", (user,))
            actual_grants = [str(next(iter(row.values()))) for row in cursor.fetchall()]
            if any("IDENTIFIED" in grant.upper() for grant in actual_grants):
                pytest.fail("Refusing to record an unexpected authentication-bearing grant")
            record_testsuite_property(
                "mysql_worker_grants",
                json.dumps({"database": mysql_config.database, "grants": actual_grants}),
            )
    # The account, schema and data remain only in the retained, stopped test instance.
    return replace(mysql_config, user=user, password=password)


@pytest.fixture
def mysql_worker_connect(
    mysql_worker_config: "DatabaseConfig",
) -> Iterator[Callable[[], Connection]]:
    yield from _tracked_connections(mysql_worker_config)
