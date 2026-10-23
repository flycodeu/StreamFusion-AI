SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `storage_cleanup_item`;
CREATE TABLE storage_cleanup_item (
    `id` BIGINT NOT NULL,
    `plan_id` BIGINT NOT NULL,
    `object_key` VARCHAR(1024) NOT NULL,
    `etag` VARCHAR(256) NOT NULL,
    `size_bytes` BIGINT NOT NULL,
    `last_modified` DATETIME(3) NULL,
    `status` VARCHAR(32) NOT NULL,
    `reason_code` VARCHAR(80) NULL,
    PRIMARY KEY (`id`),
    KEY ix_storage_item_plan (plan_id,id),
    CONSTRAINT fk_storage_item_plan FOREIGN KEY (plan_id) REFERENCES storage_cleanup_plan(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='图床清理候选表';
