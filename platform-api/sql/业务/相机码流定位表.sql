SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `camera_profile_locator`;
CREATE TABLE camera_profile_locator (
 profile_id BIGINT NOT NULL, source_id BIGINT NOT NULL,
 locator_kind VARCHAR(12) NOT NULL, endpoint_purpose VARCHAR(16) NOT NULL,
 rtsp_host_mode VARCHAR(8) NULL, rtsp_host VARCHAR(253) NULL, rtsp_port INT NULL,
 rtsp_secret_ciphertext VARBINARY(16384) NULL, rtsp_secret_nonce BINARY(12) NULL,
 rtsp_secret_tag BINARY(16) NULL, rtsp_encryption_key_id VARCHAR(64) NULL,
 rtsp_transport VARCHAR(8) NULL, identity_digest BINARY(32) NOT NULL,
 protocol_secret_ciphertext VARBINARY(16384) NULL, protocol_secret_nonce BINARY(12) NULL,
 protocol_secret_tag BINARY(16) NULL, protocol_encryption_key_id VARCHAR(64) NULL,
 created_at DATETIME(6) NOT NULL, updated_at DATETIME(6) NOT NULL,
 PRIMARY KEY(profile_id), UNIQUE(source_id,identity_digest),
 CONSTRAINT fk_camera_locator_source FOREIGN KEY(source_id) REFERENCES camera_source(id),
 CONSTRAINT fk_camera_locator_profile FOREIGN KEY(source_id,profile_id) REFERENCES camera_stream_profile(source_id,id),
 CONSTRAINT fk_camera_locator_endpoint FOREIGN KEY(source_id,endpoint_purpose) REFERENCES camera_source_endpoint(source_id,purpose),
 CONSTRAINT ck_camera_locator_kind CHECK((locator_kind='RTSP' AND endpoint_purpose='RTSP' AND rtsp_transport IS NOT NULL AND rtsp_transport='TCP' AND rtsp_host_mode IS NOT NULL AND rtsp_secret_ciphertext IS NOT NULL AND rtsp_secret_nonce IS NOT NULL AND rtsp_secret_tag IS NOT NULL AND rtsp_encryption_key_id IS NOT NULL AND protocol_secret_ciphertext IS NULL AND protocol_secret_nonce IS NULL AND protocol_secret_tag IS NULL AND protocol_encryption_key_id IS NULL) OR (locator_kind IN ('ONVIF','HIKVISION','DAHUA','HIK_PLATFORM') AND ((locator_kind='ONVIF' AND endpoint_purpose IN ('ONVIF','DEVICE_HTTP')) OR (locator_kind IN ('HIKVISION','DAHUA') AND endpoint_purpose IN ('VENDOR_HTTP','DEVICE_HTTP')) OR (locator_kind='HIK_PLATFORM' AND endpoint_purpose='PLATFORM_HTTP')) AND rtsp_host_mode IS NULL AND rtsp_host IS NULL AND rtsp_port IS NULL AND rtsp_secret_ciphertext IS NULL AND rtsp_secret_nonce IS NULL AND rtsp_secret_tag IS NULL AND rtsp_encryption_key_id IS NULL AND rtsp_transport IS NULL AND protocol_secret_ciphertext IS NOT NULL AND protocol_secret_nonce IS NOT NULL AND protocol_secret_tag IS NOT NULL AND protocol_encryption_key_id IS NOT NULL)),
 CONSTRAINT ck_camera_locator_host CHECK(locator_kind<>'RTSP' OR ((rtsp_host_mode='SOURCE' AND rtsp_host IS NULL AND rtsp_port IS NULL) OR (rtsp_host_mode='EXPLICIT' AND rtsp_host IS NOT NULL AND rtsp_port IS NOT NULL AND rtsp_port BETWEEN 1 AND 65535)))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Profile持久定位：RTSP秘密或协议身份，不保存平台临时播放地址';
