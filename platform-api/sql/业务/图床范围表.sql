SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `storage_scope`;
CREATE TABLE storage_scope (
    `id` BIGINT NOT NULL,
    `instance_id` BIGINT NOT NULL,
    `bucket` VARCHAR(63) NOT NULL,
    `prefix` VARCHAR(1024) NOT NULL,
    `display_name` VARCHAR(64) NULL,
    `probe_prefix` VARCHAR(1024) NOT NULL,
    `identity_digest` BINARY(32) NOT NULL,
    `management_mode` VARCHAR(24) NOT NULL,
    `observation` MEDIUMTEXT NULL,
    `version` BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (`id`),
    UNIQUE KEY uk_storage_scope_identity (instance_id,identity_digest),
    CONSTRAINT fk_storage_scope_instance FOREIGN KEY (instance_id) REFERENCES storage_instance(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='图床范围表';
