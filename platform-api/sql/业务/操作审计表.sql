SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `sys_operation_log`;
CREATE TABLE sys_operation_log (
    id BIGINT NOT NULL COMMENT '雪花ID',
    actor_id BIGINT NULL COMMENT '操作者ID',
    target_type VARCHAR(32) NOT NULL COMMENT '目标类型',
    target_id BIGINT NULL COMMENT '目标ID',
    action VARCHAR(96) NOT NULL COMMENT '动作编码',
    result VARCHAR(16) NOT NULL COMMENT '结果：SUCCESS、FAILURE、DENIED',
    reason_code VARCHAR(64) NULL COMMENT '原因编码',
    changes JSON NULL COMMENT '字段变更',
    trace_id VARCHAR(32) NULL COMMENT '链路ID',
    source_ip VARCHAR(45) NULL COMMENT '来源IP',
    client_summary VARCHAR(255) NULL COMMENT '客户端摘要',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间',
    PRIMARY KEY (id),
    CONSTRAINT ck_operation_result CHECK (result IN ('SUCCESS', 'FAILURE', 'DENIED'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='操作审计';
CREATE INDEX ix_operation_created ON sys_operation_log(created_at, id);
CREATE INDEX ix_operation_target ON sys_operation_log(target_type, target_id, created_at);
