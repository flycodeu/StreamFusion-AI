"""Local durability tests only; synthetic grants do not prove S3 acceptance."""

import base64
import hashlib
import json
import os
from datetime import UTC, datetime, timedelta
from pathlib import Path
from typing import Literal
from urllib.parse import urlencode

import pytest

from camera_inspector.errors import InspectionError
from camera_inspector.spool import Spool, SpoolEntry
from camera_inspector.storage_types import UploadGrant, UploadResult


def grant_for(data: bytes = b"evidence", evidence_id: str = "evidence-1") -> UploadGrant:
    issued_at = datetime.now(UTC).replace(microsecond=0)
    checksum = base64.b64encode(hashlib.sha256(data).digest()).decode("ascii")
    headers = {
        "content-length": str(len(data)),
        "content-type": "image/png",
        "if-none-match": "*",
        "x-amz-checksum-sha256": checksum,
    }
    query = urlencode(
        {
            "X-Amz-Algorithm": "AWS4-HMAC-SHA256",
            "X-Amz-Credential": f"UNIT_TEST_ONLY/{issued_at:%Y%m%d}/us-east-1/s3/aws4_request",
            "X-Amz-Date": issued_at.strftime("%Y%m%dT%H%M%SZ"),
            "X-Amz-Expires": "180",
            "X-Amz-SignedHeaders": ";".join(sorted({"host", *headers})),
            "X-Amz-Signature": "0" * 64,
        }
    )
    return UploadGrant(
        evidence_id=evidence_id,
        run_id=f"run-{evidence_id}",
        assignment_epoch=1,
        placement_revision=1,
        trace_id="trace-spool",
        instance_id="storage-1",
        scope_id="scope-1",
        bucket="test-evidence",
        object_key=f"owned/{evidence_id}.png",
        endpoint_revision=1,
        expires_at=issued_at + timedelta(seconds=180),
        size_bytes=len(data),
        content_type="image/png",
        checksum_base64=checksum,
        put_url=f"https://storage.invalid/test-evidence/owned/{evidence_id}.png?{query}",
        required_headers=headers,
    )


def receipt(
    grant: UploadGrant,
    status: Literal["NOT_SENT", "PUT_ACCEPTED", "REJECTED", "UNKNOWN"] = "PUT_ACCEPTED",
    *,
    cleanup_confirmed: bool = True,
) -> UploadResult:
    return UploadResult(
        evidence_id=grant.evidence_id,
        run_id=grant.run_id,
        assignment_epoch=grant.assignment_epoch,
        placement_revision=grant.placement_revision,
        trace_id=grant.trace_id,
        status=status,
        reason_code="UPLOAD_AWAITING_CONFIRMATION"
        if status == "PUT_ACCEPTED"
        else "UPLOAD_IO_FAILED",
        http_status=200 if status == "PUT_ACCEPTED" else None,
        bytes_sent=grant.size_bytes,
        cleanup_confirmed=cleanup_confirmed,
        execution_elapsed_seconds=0.2,
        cleanup_elapsed_seconds=0.1,
    )


def own(root: Path, name: str = "one.png", data: bytes = b"evidence") -> Path:
    path = root / name
    path.write_bytes(data)
    return path


def register(spool: Spool, grant: UploadGrant, path: Path) -> None:
    spool.register(grant, path, retain_until=datetime.now(UTC) + timedelta(hours=1))


def confirm(spool: Spool, grant: UploadGrant) -> SpoolEntry:
    return spool.confirm(
        grant,
        size_bytes=grant.size_bytes,
        checksum_base64=grant.checksum_base64,
        content_type=grant.content_type,
        confirmation_id="authority-confirmation-1",
    )


def test_register_is_durable_idempotent_and_does_not_persist_bearer(tmp_path: Path) -> None:
    grant = grant_for()
    with Spool(tmp_path, max_items=2, max_bytes=100) as spool:
        path = own(tmp_path)
        register(spool, grant, path)
        register(spool, grant, path)
        assert len(spool.entries()) == 1
        documents = list(tmp_path.glob("*.json"))
        assert len(documents) == 1
        raw = documents[0].read_text(encoding="utf-8")
        assert "X-Amz-" not in raw
        assert "UNIT_TEST_ONLY" not in raw
        assert "storage.invalid" not in raw
        assert grant.checksum_base64 in raw
        assert json.loads(raw)
    with Spool(tmp_path, max_items=2, max_bytes=100) as reopened:
        assert len(reopened.entries()) == 1
        assert reopened.entries()[0].state == "READY"


def test_rebinding_existing_evidence_rejects_changed_run_or_placement(tmp_path: Path) -> None:
    grant = grant_for()
    with Spool(tmp_path, max_items=3, max_bytes=100) as spool:
        path = own(tmp_path)
        register(spool, grant, path)
        for changed in (
            grant.model_copy(update={"assignment_epoch": 2}),
            grant.model_copy(update={"placement_revision": 2}),
        ):
            with pytest.raises(InspectionError):
                register(spool, changed, path)
        assert len(spool.entries()) == 1
        assert path.read_bytes() == b"evidence"


def test_item_and_byte_limits_reject_new_records_without_deleting_files(tmp_path: Path) -> None:
    for label, max_items, max_bytes in (("items", 1, 100), ("bytes", 3, 8)):
        root = tmp_path / label
        root.mkdir()
        with Spool(root, max_items=max_items, max_bytes=max_bytes) as spool:
            register(spool, grant_for(), own(root))
            second = own(root, "two.png")
            with pytest.raises(InspectionError):
                register(spool, grant_for(evidence_id="evidence-2"), second)
            assert second.read_bytes() == b"evidence"
            assert len(spool.entries()) == 1


def test_free_disk_floor_refuses_admission(tmp_path: Path) -> None:
    with Spool(tmp_path, max_items=2, max_bytes=100, min_free_bytes=2**62) as spool:
        path = own(tmp_path)
        with pytest.raises(InspectionError):
            register(spool, grant_for(), path)
        assert path.exists()
        assert spool.entries() == ()


@pytest.mark.parametrize(
    "limits",
    [
        {"max_items": True, "max_bytes": 100},
        {"max_items": 2, "max_bytes": float("nan")},
        {"max_items": 2, "max_bytes": float("inf")},
        {"max_items": 2, "max_bytes": 100, "min_free_bytes": float("nan")},
    ],
)
def test_spool_limits_require_finite_integer_counts(tmp_path: Path, limits: dict) -> None:
    with pytest.raises(ValueError):
        Spool(tmp_path, **limits)
    assert list(tmp_path.iterdir()) == []


def test_spool_never_adopts_external_or_nested_files(tmp_path: Path) -> None:
    root = tmp_path / "owned"
    root.mkdir()
    outside = own(tmp_path)
    nested = root / "nested"
    nested.mkdir()
    inner = own(nested)
    with Spool(root, max_items=2, max_bytes=100) as spool:
        for path in (outside, inner):
            with pytest.raises(InspectionError):
                register(spool, grant_for(), path)
        assert spool.entries() == ()
    assert outside.exists() and inner.exists()


def test_restart_quarantines_in_flight_and_never_replays_it(tmp_path: Path) -> None:
    first, second = grant_for(), grant_for(evidence_id="evidence-2")
    with Spool(tmp_path, max_items=3, max_bytes=100) as spool:
        register(spool, first, own(tmp_path))
        register(spool, second, own(tmp_path, "two.png"))
        assert spool.begin(first).state == "IN_FLIGHT"
    with Spool(tmp_path, max_items=3, max_bytes=100) as reopened:
        recovered = next(entry for entry in reopened.entries() if entry.state == "UNKNOWN")
        assert recovered.cleanup_confirmed is False
        with pytest.raises(InspectionError):
            reopened.begin(second)
        reopened.confirm_cleanup(first)
        with pytest.raises(InspectionError):
            reopened.begin(first)
        assert reopened.begin(second).state == "IN_FLIGHT"


def test_remote_unknown_with_confirmed_cleanup_allows_other_objects(tmp_path: Path) -> None:
    first, second = grant_for(), grant_for(evidence_id="evidence-2")
    with Spool(tmp_path, max_items=3, max_bytes=100) as spool:
        path = own(tmp_path)
        register(spool, first, path)
        register(spool, second, own(tmp_path, "two.png"))
        spool.begin(first)
        result = receipt(first, "UNKNOWN")
        assert spool.record(result).state == "UNKNOWN"
        assert spool.record(result).state == "UNKNOWN"
        assert path.exists()
        with pytest.raises(InspectionError):
            spool.begin(first)
        assert spool.begin(second).state == "IN_FLIGHT"


def test_late_receipt_cannot_revoke_independent_cleanup_confirmation(tmp_path: Path) -> None:
    first, second = grant_for(), grant_for(evidence_id="evidence-2")
    with Spool(tmp_path, max_items=3, max_bytes=100) as spool:
        register(spool, first, own(tmp_path))
        register(spool, second, own(tmp_path, "two.png"))
        spool.begin(first)
        interrupted = receipt(first, "UNKNOWN", cleanup_confirmed=False)
        spool.record(interrupted)
        spool.confirm_cleanup(first)
        # The same completion callback may be redelivered after external OS proof.
        # It must not reoccupy a resource that has already been verified released.
        assert spool.record(interrupted).cleanup_confirmed is True
        assert spool.begin(second).state == "IN_FLIGHT"


@pytest.mark.parametrize("status", ["NOT_SENT", "REJECTED"])
def test_late_negative_receipt_cannot_erase_remote_uncertainty(
    tmp_path: Path, status: Literal["NOT_SENT", "REJECTED"]
) -> None:
    grant = grant_for()
    with Spool(tmp_path, max_items=2, max_bytes=100) as spool:
        path = own(tmp_path)
        register(spool, grant, path)
        spool.begin(grant)
        spool.record(receipt(grant, "UNKNOWN"))
        with pytest.raises(InspectionError):
            spool.record(receipt(grant, status))
        assert spool.entries()[0].state == "UNKNOWN"
        assert path.exists()
        # A positive late PUT receipt is still provisional until remote confirmation.
        assert spool.record(receipt(grant)).state == "PUT_ACCEPTED"
        assert path.exists()


def test_expiry_never_unlinks_while_worker_cleanup_is_unconfirmed(tmp_path: Path) -> None:
    grant = grant_for()
    cutoff = datetime.now(UTC) + timedelta(seconds=10)
    with Spool(tmp_path, max_items=2, max_bytes=100) as spool:
        path = own(tmp_path)
        spool.register(grant, path, retain_until=cutoff)
        spool.begin(grant)
        assert spool.expire(now=cutoff + timedelta(seconds=1)) == []
        spool.record(receipt(grant, "UNKNOWN", cleanup_confirmed=False))
        assert spool.expire(now=cutoff + timedelta(seconds=1)) == []
        assert path.exists()
        spool.confirm_cleanup(grant)
        expired = spool.expire(now=cutoff + timedelta(seconds=1))
        assert len(expired) == 1
        assert expired[0].state == "UNKNOWN"
        assert expired[0].local_state == "EXPIRED"
        assert not path.exists()


def test_failed_begin_commit_keeps_object_unstarted_and_restartable(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    grant = grant_for()
    with Spool(tmp_path, max_items=2, max_bytes=100) as spool:
        path = own(tmp_path)
        register(spool, grant, path)
        manifest = tmp_path / "uploads.json"
        persisted = manifest.read_bytes()

        def failed_replace(source: Path, destination: Path) -> None:
            raise OSError("simulated journal commit failure")

        with monkeypatch.context() as patch:
            patch.setattr(os, "replace", failed_replace)
            with pytest.raises(OSError):
                spool.begin(grant)
        assert spool.entries()[0].state == "READY"
        assert manifest.read_bytes() == persisted
        assert path.exists()
    with Spool(tmp_path, max_items=2, max_bytes=100) as reopened:
        assert reopened.entries()[0].state == "READY"
        assert reopened.begin(grant).state == "IN_FLIGHT"


def test_receipt_cannot_complete_a_different_run(tmp_path: Path) -> None:
    grant = grant_for()
    with Spool(tmp_path, max_items=2, max_bytes=100) as spool:
        path = own(tmp_path)
        register(spool, grant, path)
        spool.begin(grant)
        with pytest.raises(InspectionError):
            spool.record(receipt(grant).model_copy(update={"assignment_epoch": 2}))
        assert spool.entries()[0].state == "IN_FLIGHT"
        assert path.exists()


def test_put_acceptance_keeps_file_until_matching_authority_confirmation(tmp_path: Path) -> None:
    grant = grant_for()
    with Spool(tmp_path, max_items=2, max_bytes=100) as spool:
        path = own(tmp_path)
        register(spool, grant, path)
        spool.begin(grant)
        assert spool.record(receipt(grant)).state == "PUT_ACCEPTED"
        assert path.exists()
        with pytest.raises(InspectionError):
            spool.confirm(
                grant,
                size_bytes=grant.size_bytes + 1,
                checksum_base64=grant.checksum_base64,
                content_type=grant.content_type,
                confirmation_id="authority-confirmation-1",
            )
        assert path.exists()
        confirmed = confirm(spool, grant)
        assert confirmed.state == "CONFIRMED"
        assert confirmed.local_state == "RELEASED"
        assert not path.exists()
    with Spool(tmp_path, max_items=2, max_bytes=100) as reopened:
        assert confirm(reopened, grant).local_state == "RELEASED"


def test_confirmation_does_not_delete_a_replacement_file(tmp_path: Path) -> None:
    grant = grant_for()
    with Spool(tmp_path, max_items=2, max_bytes=100) as spool:
        path = own(tmp_path)
        register(spool, grant, path)
        spool.begin(grant)
        spool.record(receipt(grant))
        path.unlink()
        path.write_bytes(b"unrelated replacement")
        with pytest.raises(InspectionError):
            confirm(spool, grant)
        assert path.read_bytes() == b"unrelated replacement"


def test_confirmation_is_durable_before_unlink_and_cleanup_can_resume(
    tmp_path: Path, monkeypatch: pytest.MonkeyPatch
) -> None:
    grant = grant_for()
    original_unlink = Path.unlink
    original_replace = os.replace
    for stage in ("before_unlink", "after_unlink"):
        root = tmp_path / stage
        root.mkdir()
        path = root / "one.png"
        replaced = 0

        def interrupted_unlink(target: Path, missing_ok: bool = False) -> None:
            if target == path:
                raise OSError("simulated interruption before local cleanup")
            original_unlink(target, missing_ok=missing_ok)

        def interrupted_replace(source: Path, target: Path) -> None:
            nonlocal replaced
            replaced += 1
            if replaced == 2:
                raise OSError("simulated interruption after unlink before journal commit")
            original_replace(source, target)

        with Spool(root, max_items=2, max_bytes=100) as spool:
            register(spool, grant, own(root))
            spool.begin(grant)
            spool.record(receipt(grant))
            with monkeypatch.context() as patch:
                if stage == "before_unlink":
                    patch.setattr(Path, "unlink", interrupted_unlink)
                else:
                    patch.setattr(os, "replace", interrupted_replace)
                with pytest.raises(OSError):
                    confirm(spool, grant)
            assert spool.entries()[0].state == "CONFIRMED"
            assert path.exists() == (stage == "before_unlink")
        with Spool(root, max_items=2, max_bytes=100) as reopened:
            assert reopened.entries()[0].state == "CONFIRMED"
            assert confirm(reopened, grant).local_state == "RELEASED"
            assert not path.exists()


def test_expiry_releases_local_bytes_but_retains_unknown_responsibility(tmp_path: Path) -> None:
    grant = grant_for()
    now = datetime.now(UTC)
    with Spool(tmp_path, max_items=1, max_bytes=100) as spool:
        path = own(tmp_path)
        spool.register(grant, path, retain_until=now + timedelta(seconds=10))
        spool.begin(grant)
        spool.record(receipt(grant, "UNKNOWN"))
        spool.expire(now=now + timedelta(seconds=20))
        entry = spool.entries()[0]
        assert entry.state == "UNKNOWN"
        assert entry.local_state == "EXPIRED"
        assert not path.exists()
        with pytest.raises(InspectionError):
            register(spool, grant_for(evidence_id="evidence-2"), own(tmp_path, "two.png"))
        assert len(spool.entries()) == 1


def test_stop_deadline_survives_restart_and_cannot_be_extended(tmp_path: Path) -> None:
    grant = grant_for()
    cutoff = datetime.now(UTC) + timedelta(seconds=20)
    with Spool(tmp_path, max_items=2, max_bytes=100) as spool:
        register(spool, grant, own(tmp_path))
        spool.stop(drain_until=cutoff)
    with Spool(tmp_path, max_items=2, max_bytes=100) as reopened:
        with pytest.raises(InspectionError):
            reopened.stop(drain_until=cutoff + timedelta(hours=1))
        with pytest.raises(InspectionError):
            reopened.begin(grant, now=cutoff + timedelta(seconds=1))
        with pytest.raises(InspectionError):
            register(reopened, grant_for(evidence_id="evidence-2"), own(tmp_path, "two.png"))


def test_corrupted_manifest_is_not_replaced_by_an_empty_queue(tmp_path: Path) -> None:
    for defect in ("invalid_json", "missing_identity", "impossible_cleanup"):
        root = tmp_path / defect
        root.mkdir()
        with Spool(root, max_items=2, max_bytes=100) as spool:
            register(spool, grant_for(), own(root))
            manifest = next(root.glob("*.json"))
        if defect == "invalid_json":
            damaged = '{"partial":'
        else:
            document = json.loads(manifest.read_text(encoding="utf-8"))
            entry = next(iter(document["records"].values()))
            if defect == "missing_identity":
                del entry["binding"]["object_key"]
            else:
                entry["cleanup_confirmed"] = False
            damaged = json.dumps(document)
        manifest.write_text(damaged, encoding="utf-8")
        with pytest.raises(InspectionError):
            with Spool(root, max_items=2, max_bytes=100):
                pass
        assert manifest.read_text(encoding="utf-8") == damaged


def test_corrupted_manifest_cannot_assign_one_local_file_to_two_objects(tmp_path: Path) -> None:
    with Spool(tmp_path, max_items=3, max_bytes=100) as spool:
        register(spool, grant_for(), own(tmp_path))
        register(spool, grant_for(evidence_id="evidence-2"), own(tmp_path, "two.png"))
    manifest = tmp_path / "uploads.json"
    document = json.loads(manifest.read_text(encoding="utf-8"))
    first, second = document["records"].values()
    second["filename"] = first["filename"]
    second["file_identity"] = first["file_identity"]
    damaged = json.dumps(document)
    manifest.write_text(damaged, encoding="utf-8")
    with pytest.raises(InspectionError):
        with Spool(tmp_path, max_items=3, max_bytes=100):
            pass
    assert manifest.read_text(encoding="utf-8") == damaged
    assert (tmp_path / "one.png").exists()
    assert (tmp_path / "two.png").exists()


@pytest.mark.parametrize(
    "status",
    ["READY", "NOT_SENT", "REJECTED", "UNKNOWN", "PUT_ACCEPTED", "CONFIRMED"],
)
def test_valid_terminal_and_pending_records_survive_restart(tmp_path: Path, status: str) -> None:
    grant = grant_for()
    with Spool(tmp_path, max_items=2, max_bytes=100) as spool:
        path = own(tmp_path)
        register(spool, grant, path)
        if status != "READY":
            spool.begin(grant)
            result = receipt(grant)
            if status in {"NOT_SENT", "REJECTED", "UNKNOWN"}:
                result = result.model_copy(update={"status": status})
            spool.record(result)
            if status == "CONFIRMED":
                confirm(spool, grant)
        before = spool.entries()
    with Spool(tmp_path, max_items=2, max_bytes=100) as reopened:
        assert reopened.entries() == before
        assert reopened.entries()[0].state == status
        assert path.exists() == (status != "CONFIRMED")


def test_spool_has_one_lifetime_owner(tmp_path: Path) -> None:
    grant = grant_for()
    with Spool(tmp_path, max_items=2, max_bytes=100) as first_owner:
        path = own(tmp_path)
        register(first_owner, grant, path)
        first_owner.begin(grant)
        first_owner.record(receipt(grant))
        with pytest.raises(InspectionError):
            with Spool(tmp_path, max_items=2, max_bytes=100):
                pass
    with Spool(tmp_path, max_items=2, max_bytes=100) as reopened:
        assert reopened.entries()[0].state == "PUT_ACCEPTED"
        with pytest.raises(InspectionError):
            first_owner.expire(now=datetime.now(UTC) + timedelta(days=2))
        assert path.exists()
        with pytest.raises(InspectionError):
            confirm(first_owner, grant)
        assert path.exists()
