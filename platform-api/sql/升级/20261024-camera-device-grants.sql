-- Existing database only. Back up first and review the approved device-wide authorization change.
-- This adds one relationship table. It never rebuilds assets or deletes existing grants.
-- Each old channel grant now authorizes its entire physical device, including future assigned channels.
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

-- Deterministic provenance: use the earliest old grant; for an exact timestamp tie, the lowest channel ID.
-- Preserve old rows. A later explicit scope save normalizes them to the new relationship.
INSERT INTO camera_user_device_grant(user_id,device_id,created_at,created_by)
SELECT user_id,device_id,created_at,created_by FROM (
 SELECT cg.user_id,c.device_id,cg.created_at,cg.created_by,
 ROW_NUMBER() OVER(PARTITION BY cg.user_id,c.device_id ORDER BY cg.created_at,cg.channel_id) AS row_order
 FROM camera_user_channel_grant cg JOIN camera_channel c ON c.id=cg.channel_id
 WHERE c.device_id IS NOT NULL
) existing_grants WHERE row_order=1;
