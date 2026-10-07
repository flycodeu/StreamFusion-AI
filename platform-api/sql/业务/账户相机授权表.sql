SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `camera_user_channel_grant`;
CREATE TABLE camera_user_channel_grant (
 user_id BIGINT NOT NULL COMMENT '授权所属用户ID',
 channel_id BIGINT NOT NULL COMMENT '授予的业务相机通道ID',
 created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '授予时间',
 created_by BIGINT NULL COMMENT '授予人ID快照',
 PRIMARY KEY(user_id,channel_id),
 CONSTRAINT fk_camera_channel_grant_scope FOREIGN KEY(user_id) REFERENCES camera_user_scope(user_id) ON DELETE CASCADE,
 CONSTRAINT fk_camera_channel_grant_channel FOREIGN KEY(channel_id) REFERENCES camera_channel(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='账户直接相机授权';
CREATE INDEX ix_camera_channel_grant_channel ON camera_user_channel_grant(channel_id,user_id);
