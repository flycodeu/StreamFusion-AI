SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `sys_menu`;
CREATE TABLE sys_menu (
    id BIGINT NOT NULL AUTO_INCREMENT COMMENT '主键ID，自增',
    parent_id BIGINT NULL COMMENT '父菜单ID，空表示根节点，父节点应为目录',
    name VARCHAR(64) NOT NULL COMMENT '菜单显示名称',
    type VARCHAR(16) NOT NULL COMMENT '菜单类型：DIRECTORY目录，PAGE页面',
    route_name VARCHAR(64) NULL COMMENT '页面唯一前端路由名称，目录为空',
    path VARCHAR(200) NULL COMMENT '页面唯一站内路由路径，目录为空',
    component_key VARCHAR(64) NULL COMMENT '前端已编译组件白名单标识，目录为空',
    required_permission_id BIGINT NULL COMMENT '页面入口所需权限ID，目录为空',
    icon VARCHAR(64) NULL COMMENT '前端内置图标标识',
    sort_order INT NOT NULL DEFAULT 0 COMMENT '同级展示顺序，数值越小越靠前',
    visible BOOLEAN NOT NULL DEFAULT TRUE COMMENT '是否显示导航：1显示，0隐藏，不撤销API权限',
    enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT '是否启用路由：1启用，0停用，不撤销API权限',
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE COMMENT '逻辑删除标记：0未删除，1已删除',
    version BIGINT NOT NULL DEFAULT 0 COMMENT '编辑版本号，用于乐观锁和并发修改校验',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间，UTC+8（北京时间）',
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '最后更新时间，UTC+8（北京时间），由业务写入维护',
    created_by BIGINT NULL COMMENT '创建人用户ID，系统初始化时可为空',
    updated_by BIGINT NULL COMMENT '最后修改人用户ID，系统初始化时可为空',
    PRIMARY KEY (id),
    CONSTRAINT uq_menu_route UNIQUE (route_name),
    CONSTRAINT uq_menu_path UNIQUE (path),
    CONSTRAINT fk_menu_parent FOREIGN KEY (parent_id) REFERENCES sys_menu(id),
    CONSTRAINT fk_menu_permission FOREIGN KEY (required_permission_id) REFERENCES sys_permission(id),
    CONSTRAINT ck_menu_version CHECK (version >= 0),
    CONSTRAINT ck_menu_shape CHECK (
        (type = 'DIRECTORY' AND route_name IS NULL AND path IS NULL
         AND component_key IS NULL AND required_permission_id IS NULL)
        OR (type = 'PAGE' AND route_name IS NOT NULL AND path IS NOT NULL
         AND component_key IS NOT NULL AND required_permission_id IS NOT NULL)
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='菜单路由表，导航入口关联权限，不独立授予API权限';
CREATE INDEX ix_menu_parent ON sys_menu(parent_id, is_deleted, sort_order, id);
CREATE INDEX ix_menu_permission ON sys_menu(required_permission_id);
