CREATE TABLE sys_operation_log (
    id BIGINT NOT NULL AUTO_INCREMENT,
    actor_id BIGINT NULL,
    target_type VARCHAR(32) NOT NULL,
    target_id BIGINT NULL,
    action VARCHAR(96) NOT NULL,
    result VARCHAR(16) NOT NULL,
    reason_code VARCHAR(64) NULL,
    changes JSON NULL,
    trace_id VARCHAR(32) NULL,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    CONSTRAINT ck_operation_result CHECK (result IN ('SUCCESS', 'FAILURE', 'DENIED'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE INDEX ix_operation_created ON sys_operation_log(created_at, id);
CREATE INDEX ix_operation_target ON sys_operation_log(target_type, target_id, created_at);
