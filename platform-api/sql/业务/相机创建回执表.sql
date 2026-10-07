SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `camera_create_request`;
CREATE TABLE camera_create_request (
    id BIGINT NOT NULL COMMENT '创建回执雪花ID',
    actor_user_id BIGINT NOT NULL COMMENT '实际用户ID，无用户外键',
    actor_session_digest BINARY(32) NOT NULL COMMENT '已验证会话HMAC摘要',
    session_digest_key_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '会话摘要密钥ID',
    client_request_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '13位Unix毫秒-UUIDv4',
    operation VARCHAR(24) NOT NULL COMMENT 'CREATE_SOURCE/CREATE_CAMERA/ADD_PROFILE',
    parent_resource_id BIGINT NULL COMMENT '所属sourceId或cameraId',
    request_fingerprint BINARY(32) NOT NULL COMMENT '规范化请求HMAC摘要',
    fingerprint_key_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '请求摘要密钥ID',
    resource_id BIGINT NOT NULL COMMENT '已创建资源稳定ID',
    result_summary JSON NOT NULL COMMENT '有界安全回执，不含秘密',
    requested_at DATETIME(6) NOT NULL COMMENT '请求键时间，Asia/Shanghai',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '成功创建时间，Asia/Shanghai',
    expires_at DATETIME(6) NOT NULL COMMENT '回执到期时间，Asia/Shanghai',
    PRIMARY KEY (id),
    UNIQUE KEY uk_camera_create_request (actor_user_id,actor_session_digest,client_request_id),
    KEY ix_camera_create_expiry (expires_at,id),
    KEY ix_camera_create_actor (actor_user_id,client_request_id),
    CONSTRAINT ck_camera_create_operation CHECK (operation IN ('CREATE_SOURCE','CREATE_CAMERA','ADD_PROFILE'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='同步相机资产创建回执';
