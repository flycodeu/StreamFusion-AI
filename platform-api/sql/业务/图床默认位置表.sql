SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `storage_default`;
CREATE TABLE storage_default (
    `id` BIGINT NOT NULL,
    `scope_id` BIGINT NULL,
    `version` BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (`id`),
    CONSTRAINT fk_storage_default_scope FOREIGN KEY (scope_id) REFERENCES storage_scope(id),
    CONSTRAINT ck_storage_default_id CHECK (id=1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='图床默认位置表';
INSERT INTO storage_default (id,scope_id,version) VALUES (1,NULL,0);
