SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `camera_status_plan`;
CREATE TABLE camera_status_plan (
    id BIGINT NOT NULL,
    actor_user_id BIGINT NOT NULL COMMENT '创建人历史ID，执行时复核账号和权限，不绑定Session',
    client_request_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    request_key_id VARCHAR(64) NOT NULL,
    request_fingerprint BINARY(32) NOT NULL,
    state VARCHAR(16) NOT NULL COMMENT 'ACTIVE/STOPPED/EXPIRED',
    layer VARCHAR(16) NOT NULL DEFAULT 'CONTROL' COMMENT 'CONTROL或MEDIA',
    check_video TINYINT NOT NULL DEFAULT 0 COMMENT '连接通过后检查直连视频',
    dispatch_mode VARCHAR(16) NOT NULL DEFAULT 'POLLING' COMMENT 'POLLING逐个或PARALLEL有界并行',
    parallelism INT NOT NULL DEFAULT 1 COMMENT '任务最大并发数',
    media_target_count INT NOT NULL DEFAULT 0 COMMENT '媒体收帧目标数，不含平台查询',
    interval_minutes INT NOT NULL,
    target_count INT NOT NULL,
    start_at DATETIME(3) NOT NULL,
    end_at DATETIME(3) NOT NULL,
    created_at DATETIME(3) NOT NULL,
    updated_at DATETIME(3) NOT NULL,
    version BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (id),
    UNIQUE KEY uk_camera_plan_request (actor_user_id,client_request_id),
    KEY ix_camera_plan_expiry (state,end_at,id),
    CONSTRAINT ck_camera_plan_state CHECK (state IN ('ACTIVE','STOPPED','EXPIRED')),
    CONSTRAINT ck_camera_plan_layer CHECK (layer IN ('CONTROL','MEDIA')),
    CONSTRAINT ck_camera_plan_dispatch CHECK ((dispatch_mode='POLLING' AND parallelism=1) OR (dispatch_mode='PARALLEL' AND parallelism BETWEEN 1 AND 4)),
    CONSTRAINT ck_camera_plan_video CHECK (check_video IN (0,1) AND (check_video=0 OR layer='CONTROL')),
    CONSTRAINT ck_camera_plan_media_limit CHECK ((layer<>'MEDIA' AND check_video=0) OR parallelism=1),
    CONSTRAINT ck_camera_plan_media_count CHECK (media_target_count BETWEEN 0 AND target_count AND (layer='MEDIA' OR check_video=1 OR media_target_count=0)),
    CONSTRAINT ck_camera_plan_interval CHECK (interval_minutes BETWEEN 5 AND 1440),
    CONSTRAINT ck_camera_plan_count CHECK (target_count BETWEEN 1 AND 2000),
    CONSTRAINT ck_camera_plan_dates CHECK (end_at>start_at),
    CONSTRAINT ck_camera_plan_version CHECK (version>=0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='按需有限期相机巡检计划';
