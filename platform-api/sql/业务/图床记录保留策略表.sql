SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `storage_history_policy`;
CREATE TABLE storage_history_policy (
    `id` BIGINT NOT NULL COMMENT '0为全局，其他值为图床实例ID',
    `inherit_global` TINYINT(1) NOT NULL DEFAULT 1,
    `enabled` TINYINT(1) NOT NULL DEFAULT 0,
    `retention_days` INT NULL,
    `version` BIGINT NOT NULL DEFAULT 0,
    `updated_by` BIGINT NULL,
    `updated_at` DATETIME(3) NOT NULL,
    PRIMARY KEY (`id`),
    CONSTRAINT ck_storage_history_days CHECK (retention_days IS NULL OR retention_days BETWEEN 1 AND 3650),
    CONSTRAINT ck_storage_history_enabled CHECK (enabled = 0 OR retention_days IS NOT NULL),
    CONSTRAINT ck_storage_history_global CHECK (id <> 0 OR inherit_global = 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='图床记录保留策略';
INSERT INTO storage_history_policy(id,inherit_global,enabled,retention_days,version,updated_at)
VALUES(0,0,0,NULL,0,CURRENT_TIMESTAMP(3));
