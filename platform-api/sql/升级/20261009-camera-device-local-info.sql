-- CAMERA-GROUP-UX-03: existing MySQL 8 database upgrade, execute once after backup.
-- Stop application writes first. DDL commits implicitly; do not replay initialization SQL.
-- Confirm DATABASE() and verify both columns are absent before executing.
SELECT DATABASE();
SELECT COLUMN_NAME FROM information_schema.COLUMNS
 WHERE TABLE_SCHEMA=DATABASE() AND TABLE_NAME='camera_device'
   AND COLUMN_NAME IN ('local_name','remark');
ALTER TABLE camera_device
 ADD COLUMN local_name VARCHAR(128) NULL COMMENT '本地显示名称，空时沿用来源名称' AFTER source_name,
 ADD COLUMN remark VARCHAR(500) NULL COMMENT '本地设备备注' AFTER local_name;
-- Existing rows remain NULL and retain their previous display names; no data backfill.
-- For application rollback, keep these nullable columns. Export any local edits before removing them.
