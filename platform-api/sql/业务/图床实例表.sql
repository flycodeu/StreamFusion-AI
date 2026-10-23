SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `storage_instance`;
CREATE TABLE storage_instance (
    `id` BIGINT NOT NULL,
    `name` VARCHAR(64) NOT NULL,
    `endpoint` VARCHAR(2048) NOT NULL,
    `region` VARCHAR(64) NOT NULL,
    `network_policy_key` VARCHAR(64) NOT NULL,
    `metrics_endpoint` VARCHAR(2048) NULL,
    `management_url` VARCHAR(2048) NULL,
    `remark` VARCHAR(500) NULL,
    `encryption_key_id` VARCHAR(64) NOT NULL,
    `secret_nonce` VARBINARY(12) NOT NULL,
    `secret_ciphertext` VARBINARY(8192) NOT NULL,
    `secret_tag` VARBINARY(16) NOT NULL,
    `write_enabled` TINYINT(1) NOT NULL DEFAULT 0,
    `observation` MEDIUMTEXT NULL,
    `usage_snapshot` MEDIUMTEXT NULL,
    `metrics_snapshot` MEDIUMTEXT NULL,
    `version` BIGINT NOT NULL DEFAULT 0,
    `created_by` BIGINT NOT NULL,
    `created_at` DATETIME(3) NOT NULL,
    `updated_at` DATETIME(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY uk_storage_instance_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='图床实例表';
