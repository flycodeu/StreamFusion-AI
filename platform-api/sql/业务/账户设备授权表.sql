SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `camera_user_device_grant`;
CREATE TABLE camera_user_device_grant (
 user_id BIGINT NOT NULL COMMENT '授权所属用户ID',
 device_id BIGINT NOT NULL COMMENT '授予的物理设备ID，动态包含其全部已归档通道',
 created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '授予时间',
 created_by BIGINT NULL COMMENT '授予人ID快照',
 PRIMARY KEY(user_id,device_id),
 CONSTRAINT fk_camera_device_grant_scope FOREIGN KEY(user_id) REFERENCES camera_user_scope(user_id) ON DELETE CASCADE,
 CONSTRAINT fk_camera_device_grant_device FOREIGN KEY(device_id) REFERENCES camera_device(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='账户直接设备授权';
CREATE INDEX ix_camera_device_grant_device ON camera_user_device_grant(device_id,user_id);
