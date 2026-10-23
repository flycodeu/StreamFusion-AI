SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `storage_task`;
CREATE TABLE storage_task (
    `id` BIGINT NOT NULL,
    `run_id` BIGINT NOT NULL,
    `instance_id` BIGINT NOT NULL,
    `scope_id` BIGINT NULL,
    `actor_id` BIGINT NOT NULL,
    `type` VARCHAR(32) NOT NULL,
    `status` VARCHAR(32) NOT NULL,
    `phase` VARCHAR(48) NOT NULL,
    `request_key` VARCHAR(36) NOT NULL,
    `request_fingerprint` VARCHAR(160) NOT NULL,
    `input_json` MEDIUMTEXT NULL,
    `result_json` MEDIUMTEXT NULL,
    `cursor_json` MEDIUMTEXT NULL,
    `trace_id` VARCHAR(64) NULL,
    `owner` VARCHAR(64) NULL,
    `lease_until` DATETIME(3) NULL,
    `cancel_requested` TINYINT(1) NOT NULL DEFAULT 0,
    `version` BIGINT NOT NULL DEFAULT 0,
    `created_at` DATETIME(3) NOT NULL,
    `started_at` DATETIME(3) NULL,
    `finished_at` DATETIME(3) NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY uk_storage_task_request (actor_id,type,request_key),
    KEY ix_storage_task_queue (status,created_at,id),
    KEY ix_storage_task_instance (instance_id,status,id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='图床任务表';
