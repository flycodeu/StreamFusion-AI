"""Read back opt-in real F02 artifacts; never start business services or use camera secrets."""

from __future__ import annotations

import importlib.util
import json
import os
from pathlib import Path
from typing import Any

import pytest

from camera_inspector.media_types import MediaReport

pytestmark = pytest.mark.media
WORKSPACE = Path(__file__).resolve().parents[2]
EVIDENCE_ROOT = (WORKSPACE / ".run" / "p0-f02").resolve()


@pytest.fixture(scope="module")
def evidence() -> tuple[list[Path], dict[str, Any]]:
    configured = os.environ.get("SF_INSPECTOR_MEDIA_EVIDENCE")
    if not configured:
        pytest.skip(
            "Run verify-media.py and set SF_INSPECTOR_MEDIA_EVIDENCE to its run directories"
        )
    roots = [Path(item).resolve(strict=True) for item in configured.split(os.pathsep)]
    combined: dict[str, Any] = {}
    for root in roots:
        assert root.is_relative_to(EVIDENCE_ROOT) and root.name.startswith("media-")
        environment = json.loads((root / "environment.json").read_text(encoding="utf-8"))
        assert environment["source_label"] == "SYNTHETIC"
        matrix = json.loads((root / "matrix.json").read_text(encoding="utf-8"))
        assert matrix["source_label"] == "SYNTHETIC"
        assert matrix["measurement_layer"] == "EXECUTOR_DECODE_PATH"
        assert not set(combined).intersection(matrix["cases"]), "Use one passing run per case"
        combined.update(matrix["cases"])
    assert set(combined) == {"M1", "M2", "M3", "M4", "M5", "M6"}
    return roots, combined


@pytest.mark.parametrize("case", ["M1", "M2", "M3", "M4", "M5", "M6"])
def test_each_real_matrix_case_has_passed_evidence(
    evidence: tuple[list[Path], dict[str, Any]], case: str
) -> None:
    assert evidence[1][case]["passed"] is True


def test_every_recorded_process_cleanup_is_confirmed(
    evidence: tuple[list[Path], dict[str, Any]],
) -> None:
    for root in evidence[0]:
        cleanup = json.loads((root / "owned-process-cleanup.json").read_text(encoding="utf-8"))
        assert cleanup
        assert all(item["cleanup_confirmed"] and item["returncode"] is not None for item in cleanup)
        for path in root.glob("*/report.json"):
            report = MediaReport.model_validate_json(path.read_text(encoding="utf-8"))
            assert report.cleanup_confirmed and report.process_exit.cleanup_confirmed
            assert report.source_label == "SYNTHETIC"
            assert report.measurement_layer == "EXECUTOR_DECODE_PATH"
            assert all(value is None for value in report.unsupported_metrics.values())


def test_actual_saved_clips_are_playable_and_original_trigger_rgb_is_unchanged(
    evidence: tuple[list[Path], dict[str, Any]],
) -> None:
    path = WORKSPACE / "camera-inspector" / "scripts" / "verify-media.py"
    specification = importlib.util.spec_from_file_location("inspection_media_verifier", path)
    assert specification and specification.loader
    module = importlib.util.module_from_spec(specification)
    specification.loader.exec_module(module)
    root = next(item for item in evidence[0] if (item / "M4-long-gop" / "report.json").exists())
    report = MediaReport.model_validate_json(
        (root / "M4-long-gop" / "report.json").read_text(encoding="utf-8")
    )
    assert len(report.evidence) == 4
    ffmpeg, ffprobe = module.executable("ffmpeg"), module.executable("ffprobe")
    import hashlib

    for index, item in enumerate(report.evidence):
        assert item.frame_path and item.clip_path
        assert item.frame_path.resolve().is_relative_to(root)
        assert item.clip_path.resolve().is_relative_to(root)
        pixels = module.command(
            [
                ffmpeg,
                "-v",
                "error",
                "-i",
                str(item.frame_path),
                "-frames:v",
                "1",
                "-pix_fmt",
                "rgb24",
                "-threads",
                "1",
                "-f",
                "rawvideo",
                "pipe:1",
            ],
            root,
            10,
        )
        assert hashlib.sha256(pixels).hexdigest() == item.frame_sha256
        metadata = json.loads(
            module.command(
                [
                    ffprobe,
                    "-v",
                    "error",
                    "-show_entries",
                    "format=duration:stream=codec_name",
                    "-of",
                    "json",
                    str(item.clip_path),
                ],
                root,
                10,
            )
        )
        assert metadata["streams"][0]["codec_name"] == "h264"
        duration = float(metadata["format"]["duration"])
        assert duration > 0 and item.actual_start is not None and item.actual_end is not None
        assert abs(duration - (item.actual_end - item.actual_start)) < 0.5
        if index > 0:
            requested = item.requested_end - item.requested_start
            assert duration >= requested - 0.2
            assert item.missing_pre is not None and item.missing_pre < 0.2
            assert item.missing_post is not None and item.missing_post < 0.2


def test_concurrency_has_measurements_and_all_timeout_is_explicit(
    evidence: tuple[list[Path], dict[str, Any]],
) -> None:
    measured = evidence[1]["M6"]
    assert [stage["concurrency"] for stage in measured["stages"]] == [1, 2, 4, 8]
    for stage in measured["stages"]:
        assert len(stage["reports"]) == stage["concurrency"]
        assert all(report["decoded_frames"] > 0 for report in stage["reports"])
        resources = stage["resources"]
        assert resources["network_bytes"] is None
        assert resources["upload_throughput"] is None and resources["inference_throughput"] is None
        if os.name == "nt":
            assert resources["cpu_seconds"] > 0 and resources["peak_rss_bytes"] > 0
    assert len(measured["all_timeout"]) == 8
    assert all(
        item["status"] == "FAILED"
        and item["decoded_frames"] == 0
        and "FIRST_FRAME_TIMEOUT" in item["reason_codes"]
        for item in measured["all_timeout"]
    )
