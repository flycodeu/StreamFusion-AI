SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `camera_status_snapshot`;
CREATE TABLE camera_status_snapshot (
    profile_id BIGINT NOT NULL COMMENT '码流ID，每码流仅一份CONTROL事实',
    camera_id BIGINT NOT NULL COMMENT '快照所属通道ID，与码流关系受复合外键约束',
    layer VARCHAR(16) NOT NULL DEFAULT 'CONTROL' COMMENT '本期固定CONTROL，不代表媒体收流',
    probe_kind VARCHAR(32) NOT NULL DEFAULT 'RTSP_DESCRIBE' COMMENT '观察来源RTSP_DESCRIBE',
    observation_value VARCHAR(16) NOT NULL COMMENT 'ACCESSIBLE/FAILED/UNSUPPORTED',
    reason_code VARCHAR(64) NOT NULL COMMENT '受控事实原因码',
    observed_at DATETIME(3) NOT NULL COMMENT '观测时间，Asia/Shanghai',
    state_since DATETIME(3) NULL COMMENT '当前相同结果首次检出时间，不是实际上下线时间',
    last_accessible_at DATETIME(3) NULL COMMENT '同一连接配置最近成功时间，未知为空',
    last_failed_at DATETIME(3) NULL COMMENT '同一连接配置最近失败时间，未知为空',
    read_at DATETIME(3) NOT NULL COMMENT '采集器读取事实时间，GET不更新',
    valid_until DATETIME(3) NOT NULL COMMENT '事实有效截止，过期不改写成离线',
    check_requested_at DATETIME(3) NOT NULL COMMENT '原检查接受时间，拒绝旧请求覆盖新事实',
    updated_at DATETIME(3) NOT NULL COMMENT '实际快照写入时间，Asia/Shanghai',
    check_id BIGINT NOT NULL COMMENT '原检查ID，无外键，回执清理不删除事实',
    connection_key_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '实际连接语义HMAC密钥ID',
    connection_digest BINARY(32) NOT NULL COMMENT '连接语义HMAC，不进入响应或日志',
    PRIMARY KEY (profile_id),
    KEY ix_camera_status_snapshot_camera (camera_id,profile_id),
    CONSTRAINT fk_camera_status_snapshot_profile FOREIGN KEY (camera_id,profile_id) REFERENCES camera_stream_profile(channel_id,id) ON DELETE CASCADE,
    CONSTRAINT ck_camera_status_layer CHECK (layer='CONTROL'),
    CONSTRAINT ck_camera_status_snapshot_probe CHECK (probe_kind='RTSP_DESCRIBE'),
    CONSTRAINT ck_camera_status_snapshot_value CHECK (observation_value IN ('ACCESSIBLE','FAILED','UNSUPPORTED'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='按码流保存的最新合法控制层事实';
