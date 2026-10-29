"""Connection configuration and the pinned driver's pre-authentication TLS guard."""

import ssl

import pymysql
import pytest
from pymysql.connections import Connection
from pymysql.constants import CLIENT

from camera_inspector.db import DatabaseConfig, _StrictTlsConnection


def test_database_password_is_not_in_configuration_repr():
    config = DatabaseConfig(
        "127.0.0.1", 35000, "test", "do-not-log", "inspection", allow_insecure_loopback=True
    )
    assert "do-not-log" not in repr(config)


@pytest.mark.parametrize("host", ["localhost", "db.example.test", "192.0.2.10"])
def test_plaintext_requires_explicit_literal_loopback(host):
    with pytest.raises(ValueError, match="TLS is required"):
        DatabaseConfig(host, 35000, "test", "secret", "inspection", allow_insecure_loopback=True)


def test_tls_downgrade_is_rejected_before_driver_authentication(monkeypatch):
    authenticated = []
    packets = []
    monkeypatch.setattr(Connection, "_request_authentication", lambda self: authenticated.append(1))
    monkeypatch.setattr(Connection, "write_packet", lambda self, data: packets.append(data))
    connection = _StrictTlsConnection(
        user="test",
        password="must-not-be-sent",
        ssl=ssl.create_default_context(),
        defer_connect=True,
    )
    connection.server_capabilities = 0
    with pytest.raises(pymysql.OperationalError, match="DATABASE_TLS_UNAVAILABLE"):
        connection._request_authentication()
    assert authenticated == []
    assert packets == []


def test_tls_capable_server_continues_to_driver_certificate_validation(monkeypatch):
    authenticated = []
    monkeypatch.setattr(Connection, "_request_authentication", lambda self: authenticated.append(1))
    connection = _StrictTlsConnection(ssl=ssl.create_default_context(), defer_connect=True)
    connection.server_capabilities = CLIENT.SSL
    connection._request_authentication()
    assert authenticated == [1]
