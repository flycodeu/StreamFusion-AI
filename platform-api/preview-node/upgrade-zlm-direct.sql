-- One-time upgrade for an existing S3/S4 preview database. Back up first and stop Platform.
-- Canonical new-database schema remains sql/业务/预览节点表.sql and sql/业务/菜单表.sql.
-- Preflight: preview_node must exist with its S3/S4 columns and without node_kind.
-- MySQL DDL commits implicitly: stop on any error, inspect the partial result, do not replay blindly.
SET NAMES utf8mb4;
SET time_zone = '+08:00';

ALTER TABLE preview_node
  ADD COLUMN node_kind VARCHAR(16) NOT NULL DEFAULT 'MANAGED'
  COMMENT 'LOCAL/MANAGED/ZLM_DIRECT' AFTER media_address;
UPDATE preview_node SET node_kind = 'LOCAL' WHERE node_id = 'local';
ALTER TABLE preview_node
  ADD CONSTRAINT ck_preview_node_kind
  CHECK (node_kind IN ('LOCAL', 'MANAGED', 'ZLM_DIRECT'));

UPDATE sys_menu SET name = '节点管理'
WHERE id = 1007 AND route_name = 'server' AND name = '服务信息';
INSERT INTO sys_menu
  (id, parent_id, name, type, route_name, path, component_key, module_key,
   icon, sort_order, visible, enabled)
SELECT 1011, 1008, '预览节点', 'PAGE', 'PreviewServiceNodes',
       '/monitor/PreviewServiceNodes', '/preview/Nodes', 'server',
       'connection', 2, TRUE, TRUE
WHERE NOT EXISTS (SELECT 1 FROM sys_menu WHERE id = 1011);
INSERT INTO sys_role_menu (role_id, menu_id)
SELECT r.id, 1011 FROM sys_role r
WHERE r.code = 'SUPER_ADMIN' AND r.status = 'ENABLED'
  AND EXISTS (SELECT 1 FROM sys_menu WHERE id = 1011 AND route_name = 'PreviewServiceNodes')
  AND NOT EXISTS (SELECT 1 FROM sys_role_menu rm WHERE rm.role_id = r.id AND rm.menu_id = 1011);

-- Read back before restarting Platform:
SELECT node_id, node_kind, mode FROM preview_node ORDER BY id;
SELECT id, name, route_name, path FROM sys_menu WHERE id IN (1007, 1011) ORDER BY id;
