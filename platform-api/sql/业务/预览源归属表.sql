SET NAMES utf8mb4;
DROP TABLE IF EXISTS preview_source;
CREATE TABLE preview_source (
    profile_id BIGINT NOT NULL COMMENT 'Profile级唯一门，不改变相机关系',
    connection_key VARCHAR(256) NOT NULL,
    node_id VARCHAR(64) NOT NULL,
    config_version VARCHAR(64) NOT NULL,
    source_codec VARCHAR(16) NULL,
    ingress_kbps BIGINT NOT NULL,
    PRIMARY KEY (profile_id),
    CONSTRAINT fk_preview_source_node FOREIGN KEY (node_id) REFERENCES preview_node(node_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='固定源归属；最后观看确认关闭后释放';
