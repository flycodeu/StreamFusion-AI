SET NAMES utf8mb4;
DROP TABLE IF EXISTS preview_node;
CREATE TABLE preview_node (
    id BIGINT NOT NULL COMMENT '配置ID，本机为0，远程从1开始',
    node_id VARCHAR(64) NOT NULL COMMENT '机器身份',
    name VARCHAR(80) NOT NULL COMMENT '显示名',
    control_uri VARCHAR(512) NULL COMMENT 'HTTPS控制入口，无凭据',
    certificate_sha256 CHAR(64) NULL COMMENT '绑定证书指纹',
    token_path VARCHAR(1024) NULL COMMENT '平台本地机器凭据文件路径，不保存凭据',
    media_address VARCHAR(253) NULL COMMENT '浏览器可达的媒体公布地址',
    zlm_install_path VARCHAR(512) NULL COMMENT '独立ZLM的安装或发布目录，仅展示，不用于远程文件操作',
    node_kind VARCHAR(16) NOT NULL DEFAULT 'MANAGED' COMMENT 'LOCAL/MANAGED/ZLM_DIRECT',
    mode VARCHAR(16) NOT NULL DEFAULT 'LOCAL' COMMENT '期望模式LOCAL/REMOTE/DRAINING',
    config_version CHAR(64) NOT NULL COMMENT '登记时节点配置摘要',
    priority INT NOT NULL DEFAULT 100 COMMENT '升序调度，本机默认0',
    max_sources INT NOT NULL DEFAULT 64,
    max_viewers INT NOT NULL DEFAULT 64,
    max_transcodes INT NOT NULL DEFAULT 64,
    max_starting INT NOT NULL DEFAULT 4,
    ingress_kbps BIGINT NOT NULL DEFAULT 655360,
    egress_kbps BIGINT NOT NULL DEFAULT 655360,
    source_upper_kbps BIGINT NOT NULL DEFAULT 8192,
    output_upper_kbps BIGINT NOT NULL DEFAULT 8192,
    version BIGINT NOT NULL DEFAULT 0 COMMENT '并发版本',
    ops_controller_id VARCHAR(64) NULL COMMENT 'Ops稳定控制器身份，旧记录为空',
    ops_target_id VARCHAR(64) NULL COMMENT 'Ops目标身份，不使用IP推导',
    ops_instance_id VARCHAR(64) NULL COMMENT 'Ops实例身份',
    ops_release_id VARCHAR(64) NULL COMMENT '实际已应用的Ops发行版本',
    ops_synced_at DATETIME(3) NULL COMMENT '最近同步时间，Asia/Shanghai',
    PRIMARY KEY (id),
    UNIQUE KEY uk_preview_node_identity (node_id),
    UNIQUE KEY uk_preview_node_ops (ops_controller_id,ops_target_id,ops_instance_id),
    CONSTRAINT ck_preview_node_ops CHECK (
        (ops_controller_id IS NULL AND ops_target_id IS NULL AND ops_instance_id IS NULL AND ops_release_id IS NULL AND ops_synced_at IS NULL)
        OR (node_kind <> 'LOCAL' AND ops_controller_id IS NOT NULL AND ops_target_id IS NOT NULL AND ops_instance_id IS NOT NULL AND ops_release_id IS NOT NULL AND ops_synced_at IS NOT NULL)),
    CONSTRAINT ck_preview_node_zlm_path CHECK (zlm_install_path IS NULL OR node_kind = 'ZLM_DIRECT'),
    CONSTRAINT ck_preview_node_mode CHECK (mode IN ('LOCAL','REMOTE','DRAINING')),
    CONSTRAINT ck_preview_node_kind CHECK (node_kind IN ('LOCAL','MANAGED','ZLM_DIRECT'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='本机及可选远程预览节点；预算不代表实测能力';
INSERT INTO preview_node(id,node_id,name,node_kind,mode,config_version,priority) VALUES(0,'local','本机','LOCAL','REMOTE','local',0);
