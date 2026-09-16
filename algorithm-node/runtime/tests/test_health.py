from pathlib import Path

from fastapi.testclient import TestClient

from app.main import create_app
from app.settings import Settings


def test_health_without_external_services(tmp_path: Path) -> None:
    with TestClient(create_app(Settings(_env_file=None, work_dir=tmp_path))) as client:
        response = client.get("/health")
    assert response.status_code == 200
    assert response.json() == {
        "status": "UP",
        "service": "algorithm-runtime",
        "mode": "skeleton",
    }
