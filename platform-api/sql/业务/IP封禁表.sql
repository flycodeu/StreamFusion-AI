SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `sys_ip_block`;
CREATE TABLE sys_ip_block (
    id BIGINT NOT NULL COMMENT '雪花ID',
    source_ip VARCHAR(45) NOT NULL COMMENT '规范化的IPv4或IPv6来源地址',
    status VARCHAR(16) NOT NULL COMMENT 'BLOCKED封禁、RELEASED解除',
    reason_code VARCHAR(64) NOT NULL COMMENT '封禁原因编码',
    failed_attempts INT NOT NULL COMMENT '触发封禁时的失败次数',
    window_seconds INT NOT NULL COMMENT '失败计数窗口秒数',
    blocked_at DATETIME(6) NOT NULL COMMENT '最近封禁时间',
    unblocked_at DATETIME(6) NULL COMMENT '最近解除时间',
    unblocked_by BIGINT NULL COMMENT '执行解除的历史用户ID，不关联账号生命周期',
    version BIGINT NOT NULL DEFAULT 0 COMMENT '并发版本及失败计数世代',
    PRIMARY KEY (id),
    CONSTRAINT uk_ip_block_source UNIQUE (source_ip),
    CONSTRAINT ck_ip_block_status CHECK (status IN ('BLOCKED', 'RELEASED')),
    CONSTRAINT ck_ip_block_counters CHECK (failed_attempts >= 0 AND window_seconds > 0 AND version >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='来源IP安全封禁';
CREATE INDEX ix_ip_block_status_time ON sys_ip_block(status, blocked_at, id);
