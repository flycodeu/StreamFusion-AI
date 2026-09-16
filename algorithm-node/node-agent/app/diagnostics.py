"""Read-only interpreter and settings report for doctor.ps1."""

import json
import sys

import fastapi
from pydantic import ValidationError

from app.settings import Settings


def main() -> None:
    try:
        settings = Settings()
    except ValidationError as exc:
        fields = [str(error["loc"][0]) for error in exc.errors(include_input=False)]
        print("Invalid settings fields: " + ", ".join(fields))
        raise SystemExit(1) from None
    print(
        json.dumps(
            {
                "python": sys.version.split()[0],
                "executable": sys.executable,
                "fastapi": fastapi.__version__,
                "host": settings.host,
                "port": settings.port,
                "work_dir": str(settings.work_dir),
                "settings": "valid",
            }
        )
    )


if __name__ == "__main__":
    main()
