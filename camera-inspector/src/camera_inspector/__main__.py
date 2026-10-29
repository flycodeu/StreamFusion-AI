"""Start one inspection service process; installation and schema setup are separate."""

import argparse
import ipaddress
import logging
import sys
from logging.handlers import RotatingFileHandler

import uvicorn

from camera_inspector.app import create_app
from camera_inspector.errors import InspectionError
from camera_inspector.settings import Settings


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description="Independent inspection P0 contract service")
    parser.add_argument(
        "--allow-loopback-http",
        action="store_true",
        help="Allow unencrypted HTTP only on a literal loopback address for local experiments",
    )
    options = parser.parse_args(argv)
    handlers: list[logging.Handler] = []
    logger = logging.getLogger("camera_inspector")
    try:
        settings = Settings()
        has_tls = settings.tls_certfile is not None and settings.tls_keyfile is not None
        loopback = False
        try:
            loopback = ipaddress.ip_address(settings.host).is_loopback
        except ValueError:
            pass
        if (
            not has_tls
            and not settings.trusted_proxy
            and not (options.allow_loopback_http and loopback)
        ):
            print(
                "STARTUP_TLS_REQUIRED: configure TLS or an explicit trusted proxy", file=sys.stderr
            )
            return 2

        settings.work_dir.mkdir(parents=True, exist_ok=True)
        handlers = [
            RotatingFileHandler(
                settings.work_dir / "inspector.log",
                maxBytes=settings.log_max_bytes,
                backupCount=settings.log_backups,
                encoding="utf-8",
            ),
            logging.StreamHandler(),
        ]
        formatter = logging.Formatter("%(asctime)s %(levelname)s %(name)s %(message)s")
        for handler in handlers:
            handler.setFormatter(formatter)
            logger.addHandler(handler)
        logger.setLevel(settings.log_level)
        logger.propagate = False

        uvicorn.run(
            create_app(settings),
            host=settings.host,
            port=settings.port,
            workers=1,
            ssl_certfile=str(settings.tls_certfile) if settings.tls_certfile else None,
            ssl_keyfile=str(settings.tls_keyfile) if settings.tls_keyfile else None,
            timeout_keep_alive=5,
            timeout_graceful_shutdown=15,
            log_config=None,
            access_log=False,
            proxy_headers=False,
        )
        return 0
    except (ValueError, InspectionError, OSError):
        # Configuration/driver messages may contain secrets; never print the raw exception.
        print(
            "STARTUP_CONFIGURATION_INVALID: check the documented settings and file access",
            file=sys.stderr,
        )
        return 2
    finally:
        for handler in handlers:
            logger.removeHandler(handler)
            handler.close()


if __name__ == "__main__":
    raise SystemExit(main())
