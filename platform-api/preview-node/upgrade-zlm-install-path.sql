-- Optional display-only ZLM install/release path. One-time directed upgrade for existing S4 databases.
-- Back up, rehearse on a restored copy, stop Platform writes, then run this script once.
-- Do not replay the DROP/CREATE initialization SQL. DDL commits implicitly.
SET NAMES utf8mb4;
ALTER TABLE preview_node
    ADD COLUMN zlm_install_path VARCHAR(512) NULL COMMENT '独立ZLM的安装或发布目录，仅展示，不用于远程文件操作',
    ADD CONSTRAINT ck_preview_node_zlm_path CHECK (zlm_install_path IS NULL OR node_kind = 'ZLM_DIRECT');
SELECT node_id,node_kind,zlm_install_path FROM preview_node ORDER BY id;
