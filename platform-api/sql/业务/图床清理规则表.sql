SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `storage_cleanup_rule`;
CREATE TABLE storage_cleanup_rule (
    `id` BIGINT NOT NULL,
    `scope_id` BIGINT NOT NULL,
    `name` VARCHAR(64) NOT NULL,
    `relative_prefix` VARCHAR(1024) NOT NULL,
    `retention_days` INT NOT NULL,
    `schedule_time` VARCHAR(5) NOT NULL,
    `max_delete_objects` INT NOT NULL,
    `max_delete_bytes` BIGINT NOT NULL,
    `enabled` TINYINT(1) NOT NULL DEFAULT 0,
    `actor_id` BIGINT NOT NULL,
    `version` BIGINT NOT NULL DEFAULT 0,
    `next_at` DATETIME(3) NULL,
    `last_schedule_date` VARCHAR(10) NULL,
    PRIMARY KEY (`id`),
    KEY ix_storage_rule_due (enabled,next_at,id),
    CONSTRAINT fk_storage_rule_scope FOREIGN KEY (scope_id) REFERENCES storage_scope(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='图床清理规则表';
