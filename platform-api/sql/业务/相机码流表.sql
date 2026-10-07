SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `camera_stream_profile`;
CREATE TABLE camera_stream_profile (
    id BIGINT NOT NULL COMMENT '码流档案雪花ID',
    source_id BIGINT NOT NULL COMMENT '与通道同源的受约束来源ID',
    channel_id BIGINT NOT NULL COMMENT '相机通道ID',
    external_profile_key VARCHAR(512) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin NOT NULL COMMENT '通道内稳定码流键',
    label VARCHAR(64) NOT NULL COMMENT '本地码流标签',
    source_label VARCHAR(128) NULL COMMENT '上游码流名称',
    usage_hint VARCHAR(16) NOT NULL DEFAULT 'UNKNOWN' COMMENT 'MAIN/SUB/THIRD/CUSTOM/UNKNOWN',
    usage_origin VARCHAR(16) NOT NULL DEFAULT 'UNKNOWN' COMMENT 'DEVICE_REPORTED/MANUAL/UNKNOWN',
    enabled TINYINT(1) NOT NULL DEFAULT 1 COMMENT '允许新使用',
    video_codec VARCHAR(32) NULL COMMENT '可靠观测视频编码',
    width INT NULL COMMENT '可靠观测像素宽',
    height INT NULL COMMENT '可靠观测像素高',
    frame_rate DECIMAL(8,3) NULL COMMENT '可靠观测帧率fps',
    bitrate_kbps BIGINT NULL COMMENT '可靠观测或设备声明码率kbps',
    audio_codec VARCHAR(32) NULL COMMENT '观测音频编码',
    has_audio TINYINT(1) NULL COMMENT 'NULL未知，0无音轨，1有音轨',
    parameters_observed_at DATETIME(6) NULL COMMENT '参数观测时间，Asia/Shanghai',
    parameters_origin VARCHAR(16) NULL COMMENT 'CATALOG/MEDIA',
    version BIGINT NOT NULL DEFAULT 0 COMMENT '标签用途启停及locator共享编辑版本',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间，Asia/Shanghai',
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '编辑更新时间，Asia/Shanghai',
    created_by BIGINT NULL COMMENT '创建人ID',
    updated_by BIGINT NULL COMMENT '编辑人ID',
    PRIMARY KEY (id),
    UNIQUE KEY uk_camera_profile_external (channel_id,external_profile_key),
    UNIQUE KEY uk_camera_profile_channel (channel_id,id),
    UNIQUE KEY uk_camera_profile_source (source_id,id),
    KEY ix_camera_profile_enabled (channel_id,enabled,id),
    CONSTRAINT fk_camera_profile_channel FOREIGN KEY (source_id,channel_id) REFERENCES camera_channel(source_id,id),
    CONSTRAINT ck_camera_profile_version CHECK (version>=0),
    CONSTRAINT ck_camera_profile_enabled CHECK (enabled IN (0,1)),
    CONSTRAINT ck_camera_profile_usage CHECK (usage_hint IN ('MAIN','SUB','THIRD','CUSTOM','UNKNOWN')),
    CONSTRAINT ck_camera_profile_usage_origin CHECK (usage_origin IN ('DEVICE_REPORTED','MANUAL','UNKNOWN')),
    CONSTRAINT ck_camera_profile_parameters CHECK ((width IS NULL OR width>0) AND (height IS NULL OR height>0) AND (frame_rate IS NULL OR frame_rate>0) AND (bitrate_kbps IS NULL OR bitrate_kbps>0) AND (has_audio IS NULL OR has_audio IN (0,1))),
    CONSTRAINT ck_camera_profile_parameters_origin CHECK (parameters_origin IS NULL OR parameters_origin IN ('CATALOG','MEDIA'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='通道多码流档案';
