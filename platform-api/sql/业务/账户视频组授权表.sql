SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `camera_user_group_grant`;
CREATE TABLE camera_user_group_grant (
 user_id BIGINT NOT NULL COMMENT '授权所属用户ID',
 group_id BIGINT NOT NULL COMMENT '授予的视频组ID及动态后代',
 created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '授予时间',
 created_by BIGINT NULL COMMENT '授予人ID快照',
 PRIMARY KEY(user_id,group_id),
 CONSTRAINT fk_camera_group_grant_scope FOREIGN KEY(user_id) REFERENCES camera_user_scope(user_id) ON DELETE CASCADE,
 CONSTRAINT fk_camera_group_grant_group FOREIGN KEY(group_id) REFERENCES camera_group(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='账户视频组授权';
CREATE INDEX ix_camera_group_grant_group ON camera_user_group_grant(group_id,user_id);
