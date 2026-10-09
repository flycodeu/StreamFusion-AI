SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `camera_device`;
CREATE TABLE camera_device (
    id BIGINT NOT NULL COMMENT '本地设备雪花ID',
    source_id BIGINT NOT NULL COMMENT '接入源ID',
    external_device_key VARCHAR(512) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin NOT NULL COMMENT '来源内稳定设备键',
    external_device_ref VARCHAR(512) NULL COMMENT '上游原始设备标识，不含秘密',
    device_type VARCHAR(16) NOT NULL DEFAULT 'UNKNOWN' COMMENT 'IPC/NVR/DVR/ENCODER/UNKNOWN',
    source_name VARCHAR(128) NULL COMMENT '上游设备名称',
    local_name VARCHAR(128) NULL COMMENT '本地显示名称，空时沿用来源名称',
    remark VARCHAR(500) NULL COMMENT '本地设备备注',
    manufacturer VARCHAR(128) NULL COMMENT '观测制造商',
    model VARCHAR(128) NULL COMMENT '观测型号',
    serial_number VARCHAR(128) NULL COMMENT '设备序列号',
    firmware_version VARCHAR(128) NULL COMMENT '观测固件版本',
    info_observed_at DATETIME(6) NULL COMMENT '信息观测时间，Asia/Shanghai',
    version BIGINT NOT NULL DEFAULT 0 COMMENT '身份配置编辑版本',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间，Asia/Shanghai',
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '身份配置更新时间，Asia/Shanghai',
    created_by BIGINT NULL COMMENT '创建人ID',
    updated_by BIGINT NULL COMMENT '身份配置修改人ID',
    PRIMARY KEY (id),
    UNIQUE KEY uk_camera_device_external (source_id,external_device_key),
    UNIQUE KEY uk_camera_device_source (source_id,id),
    CONSTRAINT fk_camera_device_source FOREIGN KEY (source_id) REFERENCES camera_source(id),
    CONSTRAINT ck_camera_device_version CHECK (version>=0),
    CONSTRAINT ck_camera_device_type CHECK (device_type IN ('IPC','NVR','DVR','ENCODER','UNKNOWN'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='来源内设备档案';
