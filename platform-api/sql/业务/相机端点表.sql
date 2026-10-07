SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `camera_source_endpoint`;
CREATE TABLE camera_source_endpoint (
 id BIGINT NOT NULL, source_id BIGINT NOT NULL, purpose VARCHAR(16) NOT NULL,
 scheme VARCHAR(8) NOT NULL, host VARCHAR(253) NOT NULL, port INT NOT NULL,
 base_path VARCHAR(1024) NOT NULL DEFAULT '', auth_mode VARCHAR(20) NOT NULL,
 credential_id BIGINT NULL, tls_policy VARCHAR(16) NOT NULL DEFAULT 'SYSTEM_CA',
 ca_bundle_key VARCHAR(64) NULL, cert_sha256 BINARY(32) NULL,
 created_at DATETIME(6) NOT NULL, updated_at DATETIME(6) NOT NULL,
 PRIMARY KEY(id), UNIQUE(source_id,purpose), UNIQUE(source_id,id),
 CONSTRAINT fk_camera_endpoint_source FOREIGN KEY(source_id) REFERENCES camera_source(id),
 CONSTRAINT fk_camera_endpoint_credential FOREIGN KEY(source_id,credential_id) REFERENCES camera_source_credential(source_id,id),
 CONSTRAINT ck_camera_endpoint_protocol CHECK((purpose='RTSP' AND scheme='rtsp' AND base_path='') OR (purpose='ONVIF' AND scheme IN ('http','https') AND base_path='/onvif/device_service') OR (purpose IN ('VENDOR_HTTP','DEVICE_HTTP') AND scheme IN ('http','https') AND base_path='') OR (purpose='PLATFORM_HTTP' AND scheme IN ('http','https') AND base_path LIKE '/%')),
 CONSTRAINT ck_camera_endpoint_port CHECK(port BETWEEN 1 AND 65535),
 CONSTRAINT ck_camera_endpoint_auth CHECK((auth_mode='NONE' AND credential_id IS NULL AND purpose<>'PLATFORM_HTTP') OR (auth_mode='DRIVER_NEGOTIATED' AND credential_id IS NOT NULL)),
 CONSTRAINT ck_camera_endpoint_tls CHECK(tls_policy='SYSTEM_CA' AND ca_bundle_key IS NULL AND cert_sha256 IS NULL)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='相机来源端点';
