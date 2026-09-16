SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `sys_operation_log`;
CREATE TABLE sys_operation_log (
    id BIGINT NOT NULL AUTO_INCREMENT COMMENT '主键ID，自增',
    actor_id BIGINT NULL COMMENT '操作者用户ID，匿名或系统操作可为空',
    target_type VARCHAR(32) NOT NULL COMMENT '操作目标类型，例如USER、ROLE、DEPT',
    target_id BIGINT NULL COMMENT '操作目标ID，无具体目标时为空',
    action VARCHAR(96) NOT NULL COMMENT '审计动作编码',
    result VARCHAR(16) NOT NULL COMMENT '操作结果：SUCCESS成功，FAILURE失败，DENIED拒绝',
    reason_code VARCHAR(64) NULL COMMENT '失败或拒绝原因编码，不保存敏感原文',
    changes JSON NULL COMMENT '白名单字段变更差异，禁止写入密码或会话凭证',
    trace_id VARCHAR(32) NULL COMMENT '请求跟踪标识，关联服务日志',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '审计记录时间，UTC+8（北京时间）',
    PRIMARY KEY (id),
    CONSTRAINT ck_operation_result CHECK (result IN ('SUCCESS', 'FAILURE', 'DENIED'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='操作审计表，记录安全操作和受控字段差异';
CREATE INDEX ix_operation_created ON sys_operation_log(created_at, id);
CREATE INDEX ix_operation_target ON sys_operation_log(target_type, target_id, created_at);
