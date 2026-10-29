"""The isolated evidence table keeps placement and storage identities distinct."""

import pymysql
import pytest
from test_repository_mysql import claimed, execute, prepare

pytestmark = pytest.mark.mysql


def test_evidence_placement_keys_and_storage_boundaries(mysql_factory) -> None:
    lease = claimed(prepare(mysql_factory))
    execute(
        mysql_factory,
        "INSERT INTO storage_instance (id,name,endpoint,region,network_policy_key,"
        "encryption_key_id,secret_nonce,secret_ciphertext,secret_tag,created_by,created_at,"
        "updated_at) VALUES (1,'test-store','https://storage.invalid','local','test',"
        "'test',%s,%s,%s,1,UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))",
        (b"0" * 12, b"cipher", b"0" * 16),
    )
    execute(
        mysql_factory,
        "INSERT INTO storage_scope (id,instance_id,bucket,prefix,probe_prefix,"
        "identity_digest,management_mode) VALUES (2,1,'test-evidence','owned/',"
        "'probe/',%s,'MANAGED')",
        (b"0" * 32,),
    )
    fields = (
        lease.run_id,
        lease.assignment_epoch,
        3,
        4,
        "IMAGE",
        1,
        1,
        2,
        "test-evidence",
        "owned/evidence-1.png",
        1,
        1024,
        "image/png",
        "a" * 64,
        "request-1",
    )
    sql = (
        "INSERT INTO inspection_evidence (id,evidence_id,run_id,assignment_epoch,"
        "camera_id,profile_id,kind,placement_revision,instance_id,scope_id,bucket,"
        "object_key,endpoint_revision,size_bytes,content_type,sha256,request_id,"
        "expires_at,created_at,updated_at) VALUES (%s,%s,%s,%s,%s,%s,%s,%s,%s,%s,"
        "%s,%s,%s,%s,%s,%s,%s,UTC_TIMESTAMP(6)+INTERVAL 5 MINUTE,"
        "UTC_TIMESTAMP(6),UTC_TIMESTAMP(6))"
    )
    execute(mysql_factory, sql, (10, "evidence-1", *fields))
    with pytest.raises(pymysql.IntegrityError) as duplicate:
        execute(mysql_factory, sql, (11, "evidence-1", *fields))
    assert duplicate.value.args[0] == 1062

    with pytest.raises(pymysql.IntegrityError) as missing_scope:
        execute(mysql_factory, sql, (12, "evidence-2", *fields[:7], 999, *fields[8:]))
    assert missing_scope.value.args[0] == 1452

    with pytest.raises(pymysql.OperationalError) as premature_commit:
        execute(
            mysql_factory,
            "UPDATE inspection_evidence SET state='COMMITTED' WHERE id=10",
        )
    assert premature_commit.value.args[0] == 3819

    execute(
        mysql_factory,
        "UPDATE inspection_evidence SET state='UNKNOWN',reason_code='REMOTE_UNVERIFIED' "
        "WHERE id=10",
    )
    assert (
        execute(
            mysql_factory,
            "SELECT reason_code FROM inspection_evidence WHERE id=10",
        )[0]["reason_code"]
        == "REMOTE_UNVERIFIED"
    )
    execute(
        mysql_factory,
        "UPDATE inspection_evidence SET state='COMMITTED',reason_code='CONFIRMED',"
        "committed_at=UTC_TIMESTAMP(6) WHERE id=10",
    )
    row = execute(
        mysql_factory,
        "SELECT evidence_id,placement_revision,state,reason_code "
        "FROM inspection_evidence WHERE id=10",
    )[0]
    assert row == {
        "evidence_id": "evidence-1",
        "placement_revision": 1,
        "state": "COMMITTED",
        "reason_code": "CONFIRMED",
    }
