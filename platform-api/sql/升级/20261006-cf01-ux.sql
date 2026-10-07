-- CF-01-UX-08: only for the 23-table MANAGEMENT-07 schema with connection_category.
-- Verify backup and stop writers. sys_menu.navigation_group and import_item must be absent.
-- This is a one-time incremental upgrade, not initialization or a rollback script.
SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

ALTER TABLE sys_menu ADD COLUMN navigation_group VARCHAR(16) NOT NULL DEFAULT 'BUSINESS' COMMENT '根导航分区，由系统维护' AFTER sort_order, ADD CONSTRAINT ck_menu_navigation_group CHECK (navigation_group IN ('BUSINESS', 'SYSTEM', 'MONITOR'));
ALTER TABLE camera_access_job
 ADD COLUMN job_kind VARCHAR(16) NOT NULL DEFAULT 'CATALOG' COMMENT 'CATALOG/SCAN/BULK_IMPORT',
 ADD COLUMN source_version BIGINT NULL COMMENT '任务绑定的来源版本',
 ADD COLUMN bulk_group_id BIGINT NULL COMMENT '整批导入目标组，历史引用',
 ADD COLUMN bulk_next_page INT NOT NULL DEFAULT 1 COMMENT '整批导入下一页',
 ADD COLUMN bulk_total BIGINT NULL COMMENT '上游最近报告总量，不是全量快照',
 ADD COLUMN bulk_processed_count INT NOT NULL DEFAULT 0 COMMENT '已处理不同上游身份数',
 ADD COLUMN bulk_created_count INT NOT NULL DEFAULT 0 COMMENT '新建数',
 ADD COLUMN bulk_existing_count INT NOT NULL DEFAULT 0 COMMENT '匹配已有数',
 ADD COLUMN bulk_failed_count INT NOT NULL DEFAULT 0 COMMENT '逐项失败数，不含读取整页失败',
 ADD COLUMN bulk_duplicate_count INT NOT NULL DEFAULT 0 COMMENT '分页重复身份数',
 ADD COLUMN bulk_started_at DATETIME(6) NULL COMMENT '整批导入开始时间',
 MODIFY secret_ciphertext MEDIUMBLOB NULL COMMENT '最大1MiB加密暂存；普通目录15分钟，整批30分钟',
 ADD CONSTRAINT ck_access_job_kind CHECK (job_kind IN ('CATALOG','SCAN','BULK_IMPORT')),
 ADD CONSTRAINT ck_access_job_bulk_counts CHECK (bulk_next_page BETWEEN 1 AND 101 AND (bulk_total IS NULL OR bulk_total >= 0) AND bulk_processed_count BETWEEN 0 AND 10000 AND bulk_created_count >= 0 AND bulk_existing_count >= 0 AND bulk_failed_count >= 0 AND bulk_duplicate_count >= 0 AND bulk_processed_count = bulk_created_count + bulk_existing_count + bulk_failed_count AND (source_version IS NULL OR source_version >= 0));
CREATE TABLE camera_access_import_item (
    id BIGINT NOT NULL COMMENT '雪花ID',
    job_id BIGINT NOT NULL COMMENT '所属导入任务',
    external_key VARCHAR(512) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin NOT NULL COMMENT '本任务上游唯一身份',
    page_number INT NOT NULL COMMENT '上游页号',
    item_index INT NOT NULL COMMENT '页内序号，0开始',
    status VARCHAR(16) NOT NULL COMMENT 'CREATED/EXISTING/FAILED',
    camera_id BIGINT NULL COMMENT '保存后的相机ID，历史引用',
    name VARCHAR(128) NULL COMMENT '点位名称快照',
    reason_code VARCHAR(64) NULL COMMENT '固定安全错误码',
    created_at DATETIME(6) NOT NULL COMMENT '处理时间',
    PRIMARY KEY (id),
    UNIQUE KEY uk_access_import_identity (job_id,external_key),
    KEY idx_access_import_page (job_id,status,id),
    CONSTRAINT fk_access_import_job FOREIGN KEY (job_id) REFERENCES camera_access_job(id) ON DELETE CASCADE,
    CONSTRAINT ck_access_import_item CHECK (page_number BETWEEN 1 AND 100 AND item_index BETWEEN 0 AND 99 AND status IN ('CREATED','EXISTING','FAILED') AND ((status = 'FAILED' AND camera_id IS NULL) OR (status <> 'FAILED' AND camera_id IS NOT NULL)))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='整批导入逐项结果及分页去重';

-- Normalize only identified built-in navigation roots; labels and historical IDs may differ.
START TRANSACTION;
UPDATE camera_access_job SET job_kind='SCAN' WHERE method='SCAN';
UPDATE sys_menu root JOIN sys_menu page ON page.parent_id=root.id
 SET root.navigation_group='SYSTEM',root.sort_order=9000,root.version=root.version+1,root.updated_at=CURRENT_TIMESTAMP(6)
 WHERE root.parent_id IS NULL AND root.type='DIRECTORY' AND page.path='/system/menus' AND page.module_key='menu';
UPDATE sys_menu root JOIN sys_menu page ON page.parent_id=root.id
 SET root.navigation_group='MONITOR',root.sort_order=10000,root.version=root.version+1,root.updated_at=CURRENT_TIMESTAMP(6)
 WHERE root.parent_id IS NULL AND root.type='DIRECTORY' AND page.path='/monitor/Server' AND page.module_key='server';
UPDATE sys_menu root JOIN sys_menu page ON page.parent_id=root.id
 SET root.sort_order=0,root.version=root.version+1,root.updated_at=CURRENT_TIMESTAMP(6)
 WHERE root.parent_id IS NULL AND root.type='DIRECTORY' AND page.path='/camera/manage' AND page.module_key='camera';
-- Both pages already authorize the same camera module. Carry page-only role relations forward.
UPDATE sys_role role JOIN sys_role_menu relation ON relation.role_id=role.id
 JOIN sys_menu old_page ON old_page.id=relation.menu_id
 SET role.version=role.version+1,role.updated_at=CURRENT_TIMESTAMP(6)
 WHERE old_page.path='/camera/sources' AND old_page.component_key='/camera-source/Manage' AND old_page.module_key='camera';
INSERT INTO sys_role_menu (role_id,menu_id,created_at,created_by)
 SELECT old_rel.role_id,current_page.id,CURRENT_TIMESTAMP(6),NULL
 FROM sys_role_menu old_rel JOIN sys_menu old_page ON old_page.id=old_rel.menu_id
 JOIN sys_menu current_page ON current_page.path='/camera/manage' AND current_page.module_key='camera'
 LEFT JOIN sys_role_menu existing ON existing.role_id=old_rel.role_id AND existing.menu_id=current_page.id
 WHERE old_page.path='/camera/sources' AND old_page.component_key='/camera-source/Manage'
 AND old_page.module_key='camera' AND existing.role_id IS NULL;
DELETE old_rel FROM sys_role_menu old_rel JOIN sys_menu old_page ON old_page.id=old_rel.menu_id
 WHERE old_page.path='/camera/sources' AND old_page.component_key='/camera-source/Manage' AND old_page.module_key='camera';
DELETE FROM sys_menu WHERE path='/camera/sources' AND component_key='/camera-source/Manage' AND module_key='camera';
COMMIT;
