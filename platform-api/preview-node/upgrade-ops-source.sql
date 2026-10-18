-- One-time directed upgrade. Stop Platform and back up the existing database first.
-- Requires the current node_kind/S3/S4 schema. Never replay initialization SQL.
-- MySQL DDL commits implicitly: stop on error and inspect before any retry.
SET NAMES utf8mb4;
SET time_zone = '+08:00';
ALTER TABLE preview_node
    ADD COLUMN ops_controller_id VARCHAR(64) NULL,
    ADD COLUMN ops_target_id VARCHAR(64) NULL,
    ADD COLUMN ops_instance_id VARCHAR(64) NULL,
    ADD COLUMN ops_release_id VARCHAR(64) NULL,
    ADD COLUMN ops_synced_at DATETIME(3) NULL,
    ADD UNIQUE KEY uk_preview_node_ops (ops_controller_id,ops_target_id,ops_instance_id),
    ADD CONSTRAINT ck_preview_node_ops CHECK (
        (ops_controller_id IS NULL AND ops_target_id IS NULL AND ops_instance_id IS NULL AND ops_release_id IS NULL AND ops_synced_at IS NULL)
        OR (node_kind <> 'LOCAL' AND ops_controller_id IS NOT NULL AND ops_target_id IS NOT NULL AND ops_instance_id IS NOT NULL AND ops_release_id IS NOT NULL AND ops_synced_at IS NOT NULL));
SELECT node_id,node_kind,mode,version,ops_controller_id,ops_target_id,ops_instance_id,ops_release_id,ops_synced_at
FROM preview_node ORDER BY id;
-- Existing rows intentionally remain unassociated. Do not infer provenance from IP/name.
