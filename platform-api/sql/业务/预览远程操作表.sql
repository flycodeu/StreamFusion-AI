SET NAMES utf8mb4;
DROP TABLE IF EXISTS preview_operation;
CREATE TABLE preview_operation (
    operation_id CHAR(36) NOT NULL COMMENT '幂等操作ID',
    node_id VARCHAR(64) NOT NULL COMMENT '操作时节点身份快照',
    viewer_id CHAR(36) NOT NULL COMMENT '操作时租约身份快照',
    action VARCHAR(16) NOT NULL COMMENT 'CREATE/RENEW/ANSWER/STOP',
    command_digest CHAR(64) NOT NULL COMMENT '命令摘要；不保存URL或SDP',
    deadline_at BIGINT NOT NULL COMMENT '控制期限UTC毫秒',
    state VARCHAR(16) NOT NULL COMMENT 'PENDING/SUCCEEDED/FAILED/CLOSED/UNKNOWN',
    reason_code VARCHAR(80) NULL COMMENT '脱敏原因码',
    PRIMARY KEY (operation_id),
    KEY ix_preview_operation_viewer (viewer_id, state),
    KEY ix_preview_operation_expiry (state, deadline_at),
    CONSTRAINT ck_preview_operation_action CHECK (action IN ('CREATE','RENEW','ANSWER','STOP')),
    CONSTRAINT ck_preview_operation_state CHECK (state IN ('PENDING','SUCCEEDED','FAILED','CLOSED','UNKNOWN'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='有界远程预览控制回执；活动未知操作保留';
