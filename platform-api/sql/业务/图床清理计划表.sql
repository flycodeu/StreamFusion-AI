SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `storage_cleanup_plan`;
CREATE TABLE storage_cleanup_plan (
    `id` BIGINT NOT NULL,
    `instance_id` BIGINT NOT NULL,
    `scope_id` BIGINT NOT NULL,
    `actor_id` BIGINT NOT NULL,
    `rule_id` BIGINT NULL,
    `rule_version` BIGINT NULL,
    `instance_version` BIGINT NOT NULL,
    `scope_version` BIGINT NOT NULL,
    `mode` VARCHAR(24) NOT NULL,
    `status` VARCHAR(32) NOT NULL,
    `input_json` MEDIUMTEXT NULL,
    `candidate_count` BIGINT NOT NULL DEFAULT 0,
    `candidate_bytes` BIGINT NOT NULL DEFAULT 0,
    `limit_reached` TINYINT(1) NOT NULL DEFAULT 0,
    `execution_task_id` BIGINT NULL,
    `version` BIGINT NOT NULL DEFAULT 0,
    `created_at` DATETIME(3) NOT NULL,
    `expires_at` DATETIME(3) NOT NULL,
    PRIMARY KEY (`id`),
    KEY ix_storage_plan_scope (scope_id,status,id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='图床清理计划表';
