SET NAMES utf8mb4;
DROP TABLE IF EXISTS sys_audit_export;
CREATE TABLE sys_audit_export (
    id BIGINT NOT NULL COMMENT '导出任务ID',
    actor_id BIGINT NOT NULL COMMENT '创建人历史ID',
    client_request_id VARCHAR(36) NOT NULL COMMENT '幂等请求编号',
    filter_json TEXT NOT NULL COMMENT '已校验的筛选JSON',
    format VARCHAR(8) NOT NULL COMMENT 'CSV或JSONL',
    state VARCHAR(16) NOT NULL COMMENT '导出状态',
    storage_key VARCHAR(32) NOT NULL COMMENT '本机存储标识',
    upper_time DATETIME(6) NULL COMMENT '创建时排序上界时间',
    upper_id BIGINT NULL COMMENT '创建时排序上界ID',
    row_count INT NOT NULL DEFAULT 0 COMMENT '已写入记录数',
    file_bytes BIGINT NOT NULL DEFAULT 0 COMMENT '文件字节数',
    file_removed BOOLEAN NOT NULL DEFAULT FALSE COMMENT '文件已清理',
    reason_code VARCHAR(64) NULL COMMENT '安全失败原因',
    created_at DATETIME(6) NOT NULL COMMENT '创建时间',
    started_at DATETIME(6) NULL COMMENT '开始时间',
    finished_at DATETIME(6) NULL COMMENT '结束时间',
    expires_at DATETIME(6) NOT NULL COMMENT '文件过期时间',
    lease_until DATETIME(6) NULL COMMENT '执行租约',
    worker_id VARCHAR(32) NULL COMMENT '执行实例令牌',
    PRIMARY KEY (id),
    CONSTRAINT uq_audit_export_request UNIQUE (actor_id,client_request_id),
    CONSTRAINT ck_audit_export_format CHECK (format IN ('CSV','JSONL')),
    CONSTRAINT ck_audit_export_state CHECK (state IN ('QUEUED','RUNNING','COMPLETED','FAILED','CANCELLED','EXPIRED'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='操作审计有界导出任务';
CREATE INDEX ix_audit_export_queue ON sys_audit_export(storage_key,state,created_at,id);
CREATE INDEX ix_audit_export_actor ON sys_audit_export(actor_id,created_at,id);
