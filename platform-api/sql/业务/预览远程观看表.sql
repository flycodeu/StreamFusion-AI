SET NAMES utf8mb4;
DROP TABLE IF EXISTS preview_viewer;
CREATE TABLE preview_viewer (
    viewer_id CHAR(36) NOT NULL COMMENT '既有预览租约ID',
    node_id VARCHAR(64) NOT NULL COMMENT '固定归属节点',
    boot_id CHAR(36) NOT NULL COMMENT '创建时节点启动身份',
    config_version CHAR(64) NOT NULL COMMENT '创建时配置摘要',
    profile_id BIGINT NULL COMMENT '源身份快照，不增加资产关系',
    connection_key VARCHAR(256) NULL COMMENT '既有脱敏连接版本摘要',
    output_key VARCHAR(80) NULL,
    egress_kbps BIGINT NOT NULL DEFAULT 0,
    is_starting INT NOT NULL DEFAULT 1,
    state VARCHAR(16) NOT NULL COMMENT 'RESERVED/READY/CLOSING',
    PRIMARY KEY (viewer_id),
    KEY ix_preview_viewer_source (profile_id, state),
    CONSTRAINT fk_preview_viewer_node FOREIGN KEY (node_id) REFERENCES preview_node(node_id),
    CONSTRAINT ck_preview_viewer_state CHECK (state IN ('RESERVED','READY','CLOSING'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='所有预览观看未释放责任；确认关闭后删除';
