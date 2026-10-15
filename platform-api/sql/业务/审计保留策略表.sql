SET NAMES utf8mb4;
DROP TABLE IF EXISTS sys_audit_retention_policy;
CREATE TABLE sys_audit_retention_policy (
    id BIGINT NOT NULL COMMENT '单例ID',
    enabled BOOLEAN NOT NULL DEFAULT FALSE COMMENT '是否开启清理',
    mode VARCHAR(8) NOT NULL DEFAULT 'AGE' COMMENT 'AGE或COUNT',
    keep_months INT NOT NULL DEFAULT 6 COMMENT '保留自然月数',
    max_records INT NOT NULL DEFAULT 50000 COMMENT '保留条数',
    version BIGINT NOT NULL DEFAULT 0 COMMENT '并发版本',
    updated_by BIGINT NULL COMMENT '配置修改人',
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '修改时间',
    next_at DATETIME(6) NULL COMMENT '下次执行时间',
    last_started_at DATETIME(6) NULL COMMENT '最近开始时间',
    last_finished_at DATETIME(6) NULL COMMENT '最近结束时间',
    last_deleted INT NOT NULL DEFAULT 0 COMMENT '最近删除数量',
    last_result VARCHAR(32) NULL COMMENT '最近执行结果',
    PRIMARY KEY (id),
    CONSTRAINT ck_audit_policy_singleton CHECK (id = 1),
    CONSTRAINT ck_audit_policy_mode CHECK (mode IN ('AGE','COUNT')),
    CONSTRAINT ck_audit_policy_bounds CHECK (keep_months BETWEEN 1 AND 120 AND max_records BETWEEN 1000 AND 1000000 AND version >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='操作审计按需保留策略';
INSERT INTO sys_audit_retention_policy(id) VALUES (1);
