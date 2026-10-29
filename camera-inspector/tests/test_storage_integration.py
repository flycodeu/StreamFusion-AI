"""Independent readback of explicit real SeaweedFS F03 experiment artifacts.

These checks do not start servers, inspect existing credentials, or infer that
the production platform implements the experiment authority and budget fixture.
"""

from __future__ import annotations

import hashlib
import json
import os
from pathlib import Path
from typing import Any

import pytest

from camera_inspector.storage_types import UploadResult

pytestmark = pytest.mark.storage
WORKSPACE = Path(__file__).resolve().parents[2]


@pytest.fixture(scope="module")
def evidence() -> tuple[Path, dict[str, Any]]:
    configured = os.environ.get("SF_INSPECTOR_STORAGE_EVIDENCE")
    if not configured:
        pytest.skip(
            "Run verify-storage.py and set SF_INSPECTOR_STORAGE_EVIDENCE to its run directory"
        )
    root = Path(configured).resolve(strict=True)
    assert root.is_relative_to(WORKSPACE / ".run" / "p0-f03")
    assert root.name.startswith("storage-")
    environment = json.loads((root / "environment.json").read_text(encoding="utf-8"))
    assert environment["source_label"] == "ISOLATED_SYNTHETIC_FIXTURE"
    assert environment["production_configuration_read"] is False
    assert "4.48" in environment["weed_version"]
    assert len(environment["weed_sha256"]) == 64
    assert len(environment["ports"]) == len(set(environment["ports"].values()))
    assert all(49152 <= port <= 65535 for port in environment["ports"].values())
    matrix = json.loads((root / "matrix.json").read_text(encoding="utf-8"))
    assert matrix["source_label"] == "ISOLATED_SYNTHETIC_FIXTURE"
    assert matrix["authority"].startswith("EXPERIMENT_FIXTURE_ONLY")
    assert set(matrix["cases"]) == {"S1", "S2", "S3", "S4", "S5", "S6"}
    return root, matrix["cases"]


@pytest.mark.parametrize("case", ["S1", "S2", "S3", "S4", "S5", "S6"])
def test_real_matrix_cases_have_passed_evidence(
    evidence: tuple[Path, dict[str, Any]], case: str
) -> None:
    assert evidence[1][case]["passed"] is True


def test_verified_tls_upload_requires_remote_confirmation(
    evidence: tuple[Path, dict[str, Any]],
) -> None:
    root, cases = evidence
    record = cases["S1"]
    result = UploadResult.model_validate(record["upload"])
    assert result.status == "PUT_ACCEPTED" and result.http_status == 200
    assert record["untrusted_ca_rejected"] and record["wrong_hostname_rejected"]
    assert record["source_retained_until_remote_verification"]
    assert record["source_retained_after_lost_confirmation_ack"]
    assert record["confirmation_replay_idempotent"]
    assert record["network"].startswith("localhost DNS")
    assert result.bytes_sent == record["size_bytes"]
    assert Path(record["source"]).read_bytes().startswith(b"\x89PNG\r\n\x1a\n")
    confirmation = json.loads(
        (root / ("fixture-index-" + result.evidence_id + ".json")).read_text(encoding="utf-8")
    )
    assert confirmation["method"] == "ACTUAL_GET_REHASH_AND_HEAD_METADATA"
    journal = json.loads((root / "spool-success" / "uploads.json").read_text(encoding="utf-8"))
    entry = journal["records"][f"{result.evidence_id}:1"]
    assert entry["state"] == "CONFIRMED" and entry["local_state"] == "RELEASED"
    assert entry["confirmation_id"] == confirmation["confirmation_id"]
    assert not (root / "spool-success" / entry["filename"]).exists()


def test_real_server_enforces_signed_conditions_and_sha256(
    evidence: tuple[Path, dict[str, Any]],
) -> None:
    root, matrix = evidence
    critical = json.loads((root / "critical.json").read_text(encoding="utf-8"))
    cases = {item["case"]: item for item in critical["cases"]}
    assert critical["passed"] and len(cases) == matrix["S2"]["case_count"] == 10
    expected = {
        "correct_put": 200,
        "same_grant_replay": 412,
        "same_key_different_body": 412,
        "server_validates_sha256": 400,
    }
    assert all(cases[name]["http_status"] == status for name, status in expected.items())
    assert all(
        item["http_status"] == 403
        for name, item in cases.items()
        if name.startswith("signed_header_")
    )
    assert sorted(cases["concurrent_no_overwrite"]["statuses"]) == [200, 412]
    assert cases["truncated_body_disconnect"]["passed"]
    boundary = matrix["S2"]
    upload = UploadResult.model_validate(boundary["boundary_upload"])
    assert boundary["maximum_single_put_bytes_tested"] == 64 * 1024 * 1024
    assert upload.status == "PUT_ACCEPTED" and upload.bytes_sent == 64 * 1024 * 1024
    assert boundary["boundary_confirmation"]["size_bytes"] == 64 * 1024 * 1024


def test_unknown_retains_reservation_through_head_errors_and_url_expiry(
    evidence: tuple[Path, dict[str, Any]],
) -> None:
    root, matrix = evidence
    record = matrix["S3"]
    assert UploadResult.model_validate(record["upload"]).status == "UNKNOWN"
    events = record["snapshots"]
    assert [item["event"] for item in events] == [
        "HEAD_503",
        "HEAD_404",
        "HEAD_503",
        "RECONCILIATION_LIMIT_REACHED",
        "URL_EXPIRED",
        "LATE_REMOTE_CONFIRMATION",
    ]
    assert all(item["reserved_bytes"] > 0 and item["used_bytes"] == 0 for item in events[:-1])
    assert len({item["reserved_bytes"] for item in events[:-1]}) == 1
    assert events[3]["state"] == events[4]["state"] == "NEEDS_ATTENTION"
    assert record["automatic_head_attempts"] == events[3]["max_attempts"] == 3
    retained = json.loads(
        (root / "fixture-unresolved-reservation.json").read_text(encoding="utf-8")
    )
    assert retained["state"] == "NEEDS_ATTENTION" and retained["reserved_bytes"] > 0
    assert events[-1]["reserved_bytes"] == 0
    assert events[-1]["used_bytes"] == events[0]["reserved_bytes"]
    assert record["automatic_replay_rejected"] == "SPOOL_REPLAY_REQUIRES_RECONCILIATION"
    journal = json.loads((root / "spool-unknown" / "uploads.json").read_text(encoding="utf-8"))
    entry = next(iter(journal["records"].values()))
    assert entry["state"] == "CONFIRMED" and entry["local_state"] == "RELEASED"
    assert entry["confirmation_id"] == record["confirmation"]["confirmation_id"]


def test_real_video_range_readback_prefix_restrictions_and_exact_delete(
    evidence: tuple[Path, dict[str, Any]],
) -> None:
    root, matrix = evidence
    record = matrix["S4"]
    readback = root / record["readback"]
    assert readback.is_relative_to(root) and readback.stat().st_size == record["size_bytes"]
    assert hashlib.sha256(readback.read_bytes()).hexdigest() == record["sha256"]
    assert record["range_status"] == 206 and record["range_bytes"] == 1024
    assert len(record["prefix_rejections"]) == 6
    assert set(record["prefix_rejections"].values()) == {403}
    assert record["delete_status"] == 204 and record["adjacent_sentinel_retained"]
    assert "NOT_IMPLEMENTED" in record["camera_authorization"]
    assert record["lifecycle_configuration_status"] == 404


def test_capacity_is_explicitly_limited_to_logical_observations(
    evidence: tuple[Path, dict[str, Any]],
) -> None:
    root, matrix = evidence
    record = matrix["S5"]
    assert record["status"] == "LIMITED"
    assert record["logical_managed_object_bytes"] > 0 and record["units"] == "bytes"
    assert record["physical_available_bytes"] is None
    assert record["effective_writable_volume_bytes"] is None
    assert record["physical_freshness_seconds"] is None
    assert record["metrics_http_status"] == 200
    assert len((root / "metrics-observation.txt").read_text(encoding="utf-8").splitlines()) <= 100


def test_failed_uploads_stop_and_deadline_leave_bounded_local_evidence(
    evidence: tuple[Path, dict[str, Any]],
) -> None:
    root, matrix = evidence
    record = matrix["S6"]
    assert record["bounded_items"] == len(record["failed_uploads"]) == 2
    assert record["retained_bytes"] == record["max_bytes"]
    journal = json.loads((root / "spool-failed" / "uploads.json").read_text(encoding="utf-8"))
    assert journal["stopped"] and len(journal["records"]) == 2
    assert all(
        entry["state"] == "UNKNOWN" and entry["local_state"] == "PRESENT"
        for entry in journal["records"].values()
    )
    assert (
        sum(path.stat().st_size for path in (root / "spool-failed").glob("*.bin"))
        == record["retained_bytes"]
    )
    assert record["item_limit_code"] == "SPOOL_ITEM_LIMIT"
    assert record["nearfull_code"] == "SPOOL_BYTE_LIMIT"
    assert not record["unknown_objects_auto_retried"] and not record["physical_disk_filled"]
    statuses = [UploadResult.model_validate(item) for item in record["stop_and_deadline"]]
    assert {item.reason_code for item in statuses} == {"UPLOAD_DEADLINE_EXCEEDED", "UPLOAD_STOPPED"}
    assert all(
        item.status == "UNKNOWN" and item.cleanup_confirmed and item.execution_elapsed_seconds < 1.2
        for item in statuses
    )


def test_owned_servers_proxy_and_upload_children_were_cleaned_up(
    evidence: tuple[Path, dict[str, Any]],
) -> None:
    root, matrix = evidence
    processes = json.loads((root / "cleanup.json").read_text(encoding="utf-8"))
    assert processes and all(
        item["cleanup_confirmed"]
        and item["pipe_threads_stopped"]
        and item["returncode"] is not None
        for item in processes
    )
    proxy = json.loads((root / "proxy-cleanup.json").read_text(encoding="utf-8"))
    assert proxy["server_thread_stopped"] and proxy["active_handlers"] == 0
    uploads = [
        matrix["S1"]["upload"],
        matrix["S2"]["boundary_upload"],
        matrix["S3"]["upload"],
        matrix["S4"]["upload"],
        *matrix["S6"]["failed_uploads"],
        *matrix["S6"]["stop_and_deadline"],
    ]
    assert all(UploadResult.model_validate(item).cleanup_confirmed for item in uploads)


def test_local_journals_never_persist_upload_capabilities(
    evidence: tuple[Path, dict[str, Any]],
) -> None:
    for journal in evidence[0].glob("spool-*/uploads.json"):
        raw = journal.read_text(encoding="utf-8")
        assert "X-Amz-" not in raw and "put_url" not in raw and "required_headers" not in raw
        assert "accessKey" not in raw and "secretKey" not in raw
