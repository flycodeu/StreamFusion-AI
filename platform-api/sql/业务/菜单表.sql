SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `sys_menu`;
CREATE TABLE sys_menu (
    id BIGINT NOT NULL COMMENT '雪花ID',
    parent_id BIGINT NULL COMMENT '上级菜单ID',
    name VARCHAR(64) NOT NULL COMMENT '菜单名称',
    type VARCHAR(16) NOT NULL COMMENT '类型：DIRECTORY、PAGE',
    route_name VARCHAR(64) NULL COMMENT '路由名称',
    path VARCHAR(200) NULL COMMENT '路由路径',
    component_key VARCHAR(64) NULL COMMENT '组件键',
    module_key VARCHAR(64) NULL COMMENT '模块键',
    icon VARCHAR(64) NULL COMMENT '图标键',
    sort_order INT NOT NULL DEFAULT 0 COMMENT '排序值',
    visible BOOLEAN NOT NULL DEFAULT TRUE COMMENT '导航显示标记',
    enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT '启用标记',
    version BIGINT NOT NULL DEFAULT 0 COMMENT '编辑版本',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间',
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '更新时间',
    created_by BIGINT NULL COMMENT '创建人ID',
    updated_by BIGINT NULL COMMENT '更新人ID',
    PRIMARY KEY (id),
    CONSTRAINT uq_menu_route UNIQUE (route_name),
    CONSTRAINT uq_menu_path UNIQUE (path),
    CONSTRAINT fk_menu_parent FOREIGN KEY (parent_id) REFERENCES sys_menu(id),
    CONSTRAINT ck_menu_version CHECK (version >= 0),
    CONSTRAINT ck_menu_shape CHECK (
        (type = 'DIRECTORY' AND route_name IS NULL AND path IS NULL
         AND component_key IS NULL AND module_key IS NULL)
        OR (type = 'PAGE' AND route_name IS NOT NULL AND path IS NOT NULL
         AND component_key IS NOT NULL AND module_key IS NOT NULL)
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='菜单';
CREATE INDEX ix_menu_parent ON sys_menu(parent_id, sort_order, id);

-- 系统管理初始菜单
INSERT INTO sys_menu (id, parent_id, name, type, icon, sort_order, visible, enabled)
VALUES (1001, NULL, '系统管理', 'DIRECTORY', 'settings', 0, TRUE, TRUE);
INSERT INTO sys_menu (id, parent_id, name, type, route_name, path, component_key, module_key, sort_order, visible, enabled)
VALUES
    (1002, 1001, '用户管理', 'PAGE', 'SystemUsers', '/system/users', 'SYSTEM_USERS', 'user', 0, TRUE, TRUE),
    (1003, 1001, '角色管理', 'PAGE', 'SystemRoles', '/system/roles', 'SYSTEM_ROLES', 'role', 1, TRUE, TRUE),
    (1004, 1001, '菜单管理', 'PAGE', 'SystemMenus', '/system/menus', 'SYSTEM_MENUS', 'menu', 2, TRUE, TRUE),
    (1005, 1001, '部门管理', 'PAGE', 'SystemDepartments', '/system/departments', 'SYSTEM_DEPARTMENTS', 'department', 3, TRUE, TRUE);
