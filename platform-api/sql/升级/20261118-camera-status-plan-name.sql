-- CAMERA-STATUS-PLAN-NAME-01: existing MySQL 8 database only; back up and review first.
-- DDL auto-commits. No table recreation, row mutation or automatic execution.
-- Preflight: verify camera_status_plan has no name column with SHOW CREATE TABLE camera_status_plan.
-- Re-run only after checking which statements have already committed.
SET NAMES utf8mb4;
ALTER TABLE camera_status_plan
    ADD COLUMN name VARCHAR(80) NULL COMMENT '巡检任务名称，缺省时按任务编号展示' AFTER actor_user_id,
    ADD CONSTRAINT ck_camera_plan_name CHECK (name IS NULL OR (CHAR_LENGTH(TRIM(name)) BETWEEN 1 AND 80));
