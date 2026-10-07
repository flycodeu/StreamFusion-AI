SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `camera_channel`;
CREATE TABLE camera_channel (
    id BIGINT NOT NULL COMMENT '独立授权画面雪花ID，即cameraId',
    source_id BIGINT NOT NULL COMMENT '接入源ID',
    device_id BIGINT NULL COMMENT '来源内设备ID，手工可空',
    external_channel_key VARCHAR(512) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin NOT NULL COMMENT '来源内稳定通道键',
    external_channel_ref VARCHAR(512) NULL COMMENT '上游通道原始标识',
    group_id BIGINT NULL COMMENT '视频分组ID，待归档为空',
    default_preview_profile_id BIGINT NULL COMMENT '明确选定的本通道默认码流',
    name VARCHAR(128) NOT NULL COMMENT '本地相机名称',
    source_name VARCHAR(128) NULL COMMENT '来源通道名称',
    remark VARCHAR(500) NULL COMMENT '本地备注，不含秘密',
    lifecycle VARCHAR(24) NOT NULL DEFAULT 'PENDING_ASSIGNMENT' COMMENT 'PENDING_ASSIGNMENT/ENABLED/DISABLED',
    mapping_origin VARCHAR(16) NOT NULL COMMENT 'MANUAL/ADAPTER',
    catalog_observed_at DATETIME(6) NULL COMMENT '目录可靠观测时间，Asia/Shanghai',
    version BIGINT NOT NULL DEFAULT 0 COMMENT '资产编辑版本',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间，Asia/Shanghai',
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '编辑更新时间，Asia/Shanghai',
    created_by BIGINT NULL COMMENT '创建人ID',
    updated_by BIGINT NULL COMMENT '编辑人ID',
    PRIMARY KEY (id),
    UNIQUE KEY uk_camera_channel_external (source_id,external_channel_key),
    UNIQUE KEY uk_camera_channel_source (source_id,id),
    KEY ix_camera_channel_group (group_id,lifecycle,id),
    KEY ix_camera_channel_device (device_id,id),
    KEY ix_camera_channel_lifecycle (lifecycle,id),
    CONSTRAINT fk_camera_channel_source FOREIGN KEY (source_id) REFERENCES camera_source(id),
    CONSTRAINT fk_camera_channel_device FOREIGN KEY (source_id,device_id) REFERENCES camera_device(source_id,id),
    CONSTRAINT fk_camera_channel_group FOREIGN KEY (group_id) REFERENCES camera_group(id),
    CONSTRAINT ck_camera_channel_version CHECK (version>=0),
    CONSTRAINT ck_camera_channel_mapping CHECK (mapping_origin IN ('MANUAL','ADAPTER')),
    CONSTRAINT ck_camera_channel_lifecycle CHECK ((lifecycle='PENDING_ASSIGNMENT' AND group_id IS NULL) OR (lifecycle IN ('ENABLED','DISABLED') AND group_id IS NOT NULL))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='独立授权相机画面';

-- DEFERRED CONSTRAINT
ALTER TABLE camera_channel ADD CONSTRAINT fk_camera_default_profile FOREIGN KEY (id,default_preview_profile_id) REFERENCES camera_stream_profile(channel_id,id);
