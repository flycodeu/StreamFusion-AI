"""Run with python -m app; settings control Uvicorn as well as the application."""

import uvicorn

from app.main import create_app
from app.settings import Settings


def main() -> None:
    settings = Settings()
    uvicorn.run(
        create_app(settings),
        host=settings.host,
        port=settings.port,
        timeout_keep_alive=settings.http_keepalive_seconds,
        timeout_graceful_shutdown=settings.shutdown_grace_seconds,
        log_config=None,
        access_log=False,
    )


if __name__ == "__main__":
    main()
