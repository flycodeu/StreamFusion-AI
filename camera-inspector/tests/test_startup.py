"""Startup boundaries must work before a socket or database connection is opened."""

from pathlib import Path
from unittest.mock import Mock

import pytest

from camera_inspector import __main__ as cli
from camera_inspector.settings import Settings


def config(tmp_path: Path, **overrides: object) -> Settings:
    return Settings(
        _env_file=None,
        node_id="node-1",
        auth_token="inspection-local-test-token-not-for-deployment",
        db_user="unused",
        db_password="must-not-appear-in-errors",
        db_database="unused",
        work_dir=tmp_path / "logs",
        **overrides,
    )


def test_default_listener_requires_explicit_transport_configuration(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    monkeypatch.setattr(cli, "Settings", lambda: config(tmp_path))
    server = Mock()
    monkeypatch.setattr(cli.uvicorn, "run", server)
    assert cli.main([]) == 2
    assert "STARTUP_TLS_REQUIRED" in capsys.readouterr().err
    assert not (tmp_path / "logs").exists()
    server.assert_not_called()


@pytest.mark.parametrize("host, expected", [("127.0.0.1", 0), ("0.0.0.0", 2)])
def test_local_http_flag_cannot_expose_an_unencrypted_listener(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path, host: str, expected: int
) -> None:
    monkeypatch.setattr(cli, "Settings", lambda: config(tmp_path, host=host))
    application = object()
    monkeypatch.setattr(cli, "create_app", lambda _settings: application)
    server = Mock()
    monkeypatch.setattr(cli.uvicorn, "run", server)
    assert cli.main(["--allow-loopback-http"]) == expected
    if expected == 0:
        assert server.call_args.args == (application,)
        assert server.call_args.kwargs["workers"] == 1
        assert server.call_args.kwargs["host"] == host
        assert server.call_args.kwargs["access_log"] is False
    else:
        server.assert_not_called()


def test_invalid_database_configuration_never_prints_driver_details(
    monkeypatch: pytest.MonkeyPatch, tmp_path: Path, capsys: pytest.CaptureFixture[str]
) -> None:
    monkeypatch.setattr(cli, "Settings", lambda: config(tmp_path, trusted_proxy=True))

    def failed_creation(_settings: Settings) -> None:
        raise ValueError("must-not-appear-in-errors")

    monkeypatch.setattr(cli, "create_app", failed_creation)
    assert cli.main([]) == 2
    output = capsys.readouterr()
    assert "STARTUP_CONFIGURATION_INVALID" in output.err
    assert "must-not-appear-in-errors" not in output.out + output.err


def test_help_needs_neither_secrets_nor_database(monkeypatch: pytest.MonkeyPatch) -> None:
    loader = Mock(side_effect=AssertionError("configuration must not load"))
    monkeypatch.setattr(cli, "Settings", loader)
    with pytest.raises(SystemExit) as exited:
        cli.main(["--help"])
    assert exited.value.code == 0
    loader.assert_not_called()
