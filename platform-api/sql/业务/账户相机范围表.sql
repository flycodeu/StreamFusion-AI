SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `camera_user_scope`;
CREATE TABLE camera_user_scope (
 user_id BIGINT NOT NULL COMMENT '授权所属用户ID',
 version BIGINT NOT NULL DEFAULT 0 COMMENT '两类关系的聚合编辑版本',
 created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '首次保存时间',
 updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '最近变更时间',
 created_by BIGINT NULL COMMENT '创建人ID快照',
 updated_by BIGINT NULL COMMENT '更新人ID快照',
 PRIMARY KEY(user_id),
 CONSTRAINT fk_camera_scope_user FOREIGN KEY(user_id) REFERENCES sys_user(id) ON DELETE CASCADE,
 CONSTRAINT ck_camera_scope_version CHECK(version>=0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='账户相机范围聚合';
