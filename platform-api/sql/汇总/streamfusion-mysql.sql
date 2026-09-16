SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `sys_operation_log`;
DROP TABLE IF EXISTS `sys_role_permission`;
DROP TABLE IF EXISTS `sys_user_role`;
DROP TABLE IF EXISTS `sys_menu`;
DROP TABLE IF EXISTS `sys_permission`;
DROP TABLE IF EXISTS `sys_role`;
DROP TABLE IF EXISTS `sys_user`;
DROP TABLE IF EXISTS `sys_dept`;

-- 部门表.sql
CREATE TABLE sys_dept (
    id BIGINT NOT NULL AUTO_INCREMENT COMMENT '主键ID，自增',
    parent_id BIGINT NULL COMMENT '父部门ID，空表示根节点',
    name VARCHAR(64) NOT NULL COMMENT '部门或组织节点名称',
    sort_order INT NOT NULL DEFAULT 0 COMMENT '同级展示顺序，数值越小越靠前',
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE COMMENT '逻辑删除标记：0未删除，1已删除',
    version BIGINT NOT NULL DEFAULT 0 COMMENT '编辑版本号，用于乐观锁和并发修改校验',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间，UTC+8（北京时间）',
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '最后更新时间，UTC+8（北京时间），由业务写入维护',
    created_by BIGINT NULL COMMENT '创建人用户ID，系统初始化时可为空',
    updated_by BIGINT NULL COMMENT '最后修改人用户ID，系统初始化时可为空',
    PRIMARY KEY (id),
    CONSTRAINT fk_dept_parent FOREIGN KEY (parent_id) REFERENCES sys_dept(id),
    CONSTRAINT ck_dept_version CHECK (version >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='部门组织表，公司、部门、组统一为树节点，不参与授权';
CREATE INDEX ix_dept_parent ON sys_dept(parent_id, is_deleted, sort_order, id);

-- 用户表.sql
CREATE TABLE sys_user (
    id BIGINT NOT NULL AUTO_INCREMENT COMMENT '主键ID，自增',
    username VARCHAR(32) NOT NULL COMMENT '登录账号，全表唯一，包含已删除账号',
    password_hash VARCHAR(255) NOT NULL COMMENT '带盐密码哈希，禁止存储明文或对外返回',
    nickname VARCHAR(64) NOT NULL COMMENT '用户显示昵称',
    avatar_key VARCHAR(64) NULL COMMENT '内置头像资源标识，空表示默认头像',
    dept_id BIGINT NULL COMMENT '所属部门ID，可为空，仅表示组织归属',
    status VARCHAR(16) NOT NULL DEFAULT 'ENABLED' COMMENT '账号状态：ENABLED正常，DISABLED禁用',
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE COMMENT '逻辑删除标记：0未删除，1已删除',
    must_change_password BOOLEAN NOT NULL DEFAULT TRUE COMMENT '是否必须修改密码：1是，0否',
    session_version BIGINT NOT NULL DEFAULT 0 COMMENT '会话安全版本，安全事件递增以使旧会话失效',
    failed_login_count INT NOT NULL DEFAULT 0 COMMENT '连续登录失败次数',
    locked_until DATETIME(6) NULL COMMENT '登录冷却截止时间，UTC+8（北京时间），空表示无冷却',
    version BIGINT NOT NULL DEFAULT 0 COMMENT '编辑版本号，用于乐观锁和并发修改校验',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间，UTC+8（北京时间）',
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '最后更新时间，UTC+8（北京时间），由业务写入维护',
    created_by BIGINT NULL COMMENT '创建人用户ID，系统初始化时可为空',
    updated_by BIGINT NULL COMMENT '最后修改人用户ID，系统初始化时可为空',
    PRIMARY KEY (id),
    CONSTRAINT uq_user_username UNIQUE (username),
    CONSTRAINT fk_user_dept FOREIGN KEY (dept_id) REFERENCES sys_dept(id),
    CONSTRAINT ck_user_status CHECK (status IN ('ENABLED', 'DISABLED')),
    CONSTRAINT ck_user_versions CHECK (version >= 0 AND session_version >= 0),
    CONSTRAINT ck_user_failures CHECK (failed_login_count >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='用户账号表，保存基础资料和安全状态';
CREATE INDEX ix_user_dept ON sys_user(dept_id, is_deleted);
CREATE INDEX ix_user_created ON sys_user(is_deleted, created_at, id);

-- 角色表.sql
CREATE TABLE sys_role (
    id BIGINT NOT NULL AUTO_INCREMENT COMMENT '主键ID，自增',
    code VARCHAR(64) NOT NULL COMMENT '唯一角色编码，SUPER_ADMIN为受保护超级管理员',
    name VARCHAR(64) NOT NULL COMMENT '角色显示名称',
    description VARCHAR(255) NULL COMMENT '角色用途说明',
    status VARCHAR(16) NOT NULL DEFAULT 'ENABLED' COMMENT '角色状态：ENABLED启用，DISABLED停用',
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE COMMENT '逻辑删除标记：0未删除，1已删除',
    version BIGINT NOT NULL DEFAULT 0 COMMENT '编辑版本号，用于乐观锁和并发修改校验',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间，UTC+8（北京时间）',
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '最后更新时间，UTC+8（北京时间），由业务写入维护',
    created_by BIGINT NULL COMMENT '创建人用户ID，系统初始化时可为空',
    updated_by BIGINT NULL COMMENT '最后修改人用户ID，系统初始化时可为空',
    PRIMARY KEY (id),
    CONSTRAINT uq_role_code UNIQUE (code),
    CONSTRAINT ck_role_status CHECK (status IN ('ENABLED', 'DISABLED')),
    CONSTRAINT ck_role_version CHECK (version >= 0),
    CONSTRAINT ck_role_super_admin CHECK (
        code <> 'SUPER_ADMIN' OR (status = 'ENABLED' AND is_deleted = FALSE)
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='角色表，通过用户角色关系授予个人权限';

-- 内置超级管理员角色
INSERT INTO sys_role(code, name, description)
VALUES ('SUPER_ADMIN', '超级管理员', '全部已注册功能权限，包含后续新增权限');

-- 权限表.sql
CREATE TABLE sys_permission (
    id BIGINT NOT NULL AUTO_INCREMENT COMMENT '主键ID，自增',
    code VARCHAR(96) NOT NULL COMMENT '唯一权限编码，例如system:user:read',
    name VARCHAR(64) NOT NULL COMMENT '权限显示名称',
    resource VARCHAR(64) NOT NULL COMMENT '所属资源分组，用于权限目录展示',
    description VARCHAR(255) NULL COMMENT '权限能力与使用边界说明',
    PRIMARY KEY (id),
    CONSTRAINT uq_permission_code UNIQUE (code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='权限目录表，只登记已实现的后端功能权限';
CREATE INDEX ix_permission_resource ON sys_permission(resource);

-- 菜单表.sql
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

-- 用户角色关联表.sql
CREATE TABLE sys_user_role (
    user_id BIGINT NOT NULL COMMENT '用户ID，与角色ID组成联合主键',
    role_id BIGINT NOT NULL COMMENT '授予的角色ID，与用户ID组成联合主键',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '角色授予时间，UTC+8（北京时间）',
    created_by BIGINT NULL COMMENT '角色授予人用户ID，初始化时可为空',
    PRIMARY KEY (user_id, role_id),
    CONSTRAINT fk_user_role_user FOREIGN KEY (user_id) REFERENCES sys_user(id),
    CONSTRAINT fk_user_role_role FOREIGN KEY (role_id) REFERENCES sys_role(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='用户角色关联表，仅个人赋权，不经部门继承';
CREATE INDEX ix_user_role_role ON sys_user_role(role_id, user_id);

-- 角色权限关联表.sql
CREATE TABLE sys_role_permission (
    role_id BIGINT NOT NULL COMMENT '角色ID，与权限ID组成联合主键',
    permission_id BIGINT NOT NULL COMMENT '权限ID，与角色ID组成联合主键',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '权限授予时间，UTC+8（北京时间）',
    created_by BIGINT NULL COMMENT '权限授予人用户ID，初始化时可为空',
    PRIMARY KEY (role_id, permission_id),
    CONSTRAINT fk_role_permission_role FOREIGN KEY (role_id) REFERENCES sys_role(id),
    CONSTRAINT fk_role_permission_permission FOREIGN KEY (permission_id) REFERENCES sys_permission(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='角色权限关联表，保存普通角色的显式权限集合';
CREATE INDEX ix_role_permission_permission ON sys_role_permission(permission_id, role_id);

-- 操作审计表.sql
CREATE TABLE sys_operation_log (
    id BIGINT NOT NULL AUTO_INCREMENT COMMENT '主键ID，自增',
    actor_id BIGINT NULL COMMENT '操作者用户ID，匿名或系统操作可为空',
    target_type VARCHAR(32) NOT NULL COMMENT '操作目标类型，例如USER、ROLE、DEPT',
    target_id BIGINT NULL COMMENT '操作目标ID，无具体目标时为空',
    action VARCHAR(96) NOT NULL COMMENT '审计动作编码',
    result VARCHAR(16) NOT NULL COMMENT '操作结果：SUCCESS成功，FAILURE失败，DENIED拒绝',
    reason_code VARCHAR(64) NULL COMMENT '失败或拒绝原因编码，不保存敏感原文',
    changes JSON NULL COMMENT '白名单字段变更差异，禁止写入密码或会话凭证',
    trace_id VARCHAR(32) NULL COMMENT '请求跟踪标识，关联服务日志',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '审计记录时间，UTC+8（北京时间）',
    PRIMARY KEY (id),
    CONSTRAINT ck_operation_result CHECK (result IN ('SUCCESS', 'FAILURE', 'DENIED'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='操作审计表，记录安全操作和受控字段差异';
CREATE INDEX ix_operation_created ON sys_operation_log(created_at, id);
CREATE INDEX ix_operation_target ON sys_operation_log(target_type, target_id, created_at);
