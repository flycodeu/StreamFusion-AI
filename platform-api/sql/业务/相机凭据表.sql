SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `camera_source_credential`;
CREATE TABLE camera_source_credential (
 id BIGINT NOT NULL, source_id BIGINT NOT NULL, purpose VARCHAR(16) NOT NULL,
 secret_ciphertext VARBINARY(8192) NOT NULL, secret_nonce BINARY(12) NOT NULL, secret_tag BINARY(16) NOT NULL,
 encryption_key_id VARCHAR(64) NOT NULL,
 created_at DATETIME(6) NOT NULL, updated_at DATETIME(6) NOT NULL,
 PRIMARY KEY(id), UNIQUE(source_id,purpose), UNIQUE(source_id,id),
 CONSTRAINT fk_camera_credential_source FOREIGN KEY(source_id) REFERENCES camera_source(id),
 CONSTRAINT ck_camera_credential_purpose CHECK(purpose IN ('RTSP','ONVIF','VENDOR_HTTP','PLATFORM_HTTP','DEVICE_HTTP'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='来源固定用途加密凭据';
