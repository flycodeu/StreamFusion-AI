SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `camera_source`;
CREATE TABLE camera_source (
 id BIGINT NOT NULL,
 name VARCHAR(100) NOT NULL,
 connection_category VARCHAR(16) NOT NULL DEFAULT 'DEVICE',
 adapter_type VARCHAR(64) NULL,
 vendor_hint VARCHAR(128) NULL,
 vendor_model_family VARCHAR(64) NULL,
 network_policy_key VARCHAR(64) NULL,
 rtsp_port INT NULL,
 enabled TINYINT NOT NULL DEFAULT 1,
 remark VARCHAR(500) NULL,
 version BIGINT NOT NULL DEFAULT 0,
 created_at DATETIME(6) NOT NULL, updated_at DATETIME(6) NOT NULL,
 created_by BIGINT NULL, updated_by BIGINT NULL,
 PRIMARY KEY(id),
 CONSTRAINT ck_camera_source_type CHECK(connection_category IN ('DEVICE','PLATFORM','RTSP') AND (adapter_type IS NULL OR (CHAR_LENGTH(adapter_type) BETWEEN 1 AND 64 AND adapter_type NOT IN ('AUTO','SCAN'))) AND vendor_model_family IS NULL),
 CONSTRAINT ck_camera_source_media_port CHECK(rtsp_port IS NULL OR rtsp_port BETWEEN 1 AND 65535),
 CONSTRAINT ck_camera_source_enabled CHECK(enabled IN (0,1)),
 CONSTRAINT ck_camera_source_version CHECK(version >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='相机接入源及控制协议配置';
CREATE INDEX ix_camera_source_filter ON camera_source(adapter_type,enabled,id);
