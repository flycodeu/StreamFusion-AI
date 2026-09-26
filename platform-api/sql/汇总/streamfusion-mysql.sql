SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `sys_login_record`;
DROP TABLE IF EXISTS `sys_ip_block`;
DROP TABLE IF EXISTS `sys_operation_log`;
DROP TABLE IF EXISTS `sys_role_menu`;
DROP TABLE IF EXISTS `sys_user_dept`;
DROP TABLE IF EXISTS `sys_user_role`;
DROP TABLE IF EXISTS `sys_menu`;
DROP TABLE IF EXISTS `sys_role`;
DROP TABLE IF EXISTS `sys_user`;
DROP TABLE IF EXISTS `sys_dept`;

-- 部门表.sql
CREATE TABLE sys_dept (
    id BIGINT NOT NULL COMMENT '雪花ID',
    parent_id BIGINT NULL COMMENT '上级部门ID',
    name VARCHAR(64) NOT NULL COMMENT '部门名称',
    sort_order INT NOT NULL DEFAULT 0 COMMENT '排序值',
    version BIGINT NOT NULL DEFAULT 0 COMMENT '编辑版本',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间',
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '更新时间',
    created_by BIGINT NULL COMMENT '创建人ID',
    updated_by BIGINT NULL COMMENT '更新人ID',
    PRIMARY KEY (id),
    CONSTRAINT fk_dept_parent FOREIGN KEY (parent_id) REFERENCES sys_dept(id),
    CONSTRAINT ck_dept_version CHECK (version >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='组织部门';
CREATE INDEX ix_dept_parent ON sys_dept(parent_id, sort_order, id);

-- 示例组织可在初始化前修改，运行后按普通部门管理。
INSERT INTO sys_dept (id, parent_id, name, sort_order)
SELECT 2001, NULL, '飞云科技公司', 0
WHERE NOT EXISTS (SELECT 1 FROM sys_dept WHERE id = 2001);
INSERT INTO sys_dept (id, parent_id, name, sort_order)
SELECT 2002, 2001, '研发部门', 0
WHERE NOT EXISTS (SELECT 1 FROM sys_dept WHERE id = 2002);
INSERT INTO sys_dept (id, parent_id, name, sort_order)
SELECT 2003, 2001, '运维部门', 1
WHERE NOT EXISTS (SELECT 1 FROM sys_dept WHERE id = 2003);

-- 用户表.sql
CREATE TABLE sys_user (
    id BIGINT NOT NULL COMMENT '雪花ID',
    username VARCHAR(32) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_ci NOT NULL COMMENT '登录账号',
    password VARCHAR(255) NOT NULL COMMENT '密码哈希',
    nickname VARCHAR(64) NULL COMMENT '昵称',
    avatar_key VARCHAR(64) NULL COMMENT '头像标识',
    phone VARCHAR(32) NULL COMMENT '联系电话',
    email VARCHAR(254) NULL COMMENT '联系邮箱',
    gender TINYINT NOT NULL DEFAULT 0 COMMENT '性别：0未设置、1男、2女',
    status TINYINT NOT NULL DEFAULT 0 COMMENT '账号状态：0待改密、1正常、2停用',
    must_change_password BOOLEAN NOT NULL DEFAULT TRUE COMMENT '强制改密标记',
    session_version BIGINT NOT NULL DEFAULT 0 COMMENT '会话版本',
    failed_login_count INT NOT NULL DEFAULT 0 COMMENT '连续登录失败次数',
    locked_until DATETIME(6) NULL COMMENT '登录锁定截止时间',
    version BIGINT NOT NULL DEFAULT 0 COMMENT '编辑版本',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间',
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '更新时间',
    created_by BIGINT NULL COMMENT '创建人ID',
    updated_by BIGINT NULL COMMENT '更新人ID',
    PRIMARY KEY (id),
    CONSTRAINT uq_user_username UNIQUE (username),
    CONSTRAINT ck_user_id CHECK (id > 0),
    CONSTRAINT ck_user_gender CHECK (gender IN (0, 1, 2)),
    CONSTRAINT ck_user_status CHECK (status IN (0, 1, 2)),
    CONSTRAINT ck_user_versions CHECK (version >= 0 AND session_version >= 0),
    CONSTRAINT ck_user_failures CHECK (failed_login_count >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='用户';
CREATE INDEX ix_user_created ON sys_user(created_at, id);

-- 首个管理员由 --bootstrap-admin 创建，不预置共享凭据。

-- 角色表.sql
CREATE TABLE sys_role (
    id BIGINT NOT NULL COMMENT '雪花ID',
    code VARCHAR(64) NOT NULL COMMENT '角色编码',
    name VARCHAR(64) NOT NULL COMMENT '角色名称',
    description VARCHAR(255) NULL COMMENT '说明',
    status VARCHAR(16) NOT NULL DEFAULT 'ENABLED' COMMENT '角色状态：ENABLED、DISABLED',
    version BIGINT NOT NULL DEFAULT 0 COMMENT '编辑版本',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间',
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '更新时间',
    created_by BIGINT NULL COMMENT '创建人ID',
    updated_by BIGINT NULL COMMENT '更新人ID',
    PRIMARY KEY (id),
    CONSTRAINT uq_role_code UNIQUE (code),
    CONSTRAINT ck_role_status CHECK (status IN ('ENABLED', 'DISABLED')),
    CONSTRAINT ck_role_version CHECK (version >= 0),
    CONSTRAINT ck_role_super_admin CHECK (
        code <> 'SUPER_ADMIN' OR status = 'ENABLED'
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='角色';

-- 内置超级管理员角色
INSERT INTO sys_role(id, code, name, description)
VALUES (1, 'SUPER_ADMIN', '超级管理员', '内置超级管理员');

-- 菜单表.sql
CREATE TABLE sys_menu (
    id BIGINT NOT NULL COMMENT '雪花ID',
    parent_id BIGINT NULL COMMENT '上级菜单ID',
    name VARCHAR(64) NOT NULL COMMENT '菜单名称',
    type VARCHAR(16) NOT NULL COMMENT '类型：DIRECTORY、PAGE',
    route_name VARCHAR(64) NULL COMMENT '路由名称',
    path VARCHAR(200) NULL COMMENT '路由路径',
    component_key VARCHAR(64) NULL COMMENT '页面文件路径，兼容旧组件键',
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
    (1002, 1001, '用户管理', 'PAGE', 'SystemUsers', '/system/users', '/system/User', 'user', 0, TRUE, TRUE),
    (1003, 1001, '角色管理', 'PAGE', 'SystemRoles', '/system/roles', '/system/Role', 'role', 1, TRUE, TRUE),
    (1004, 1001, '菜单管理', 'PAGE', 'SystemMenus', '/system/menus', '/system/Menu', 'menu', 2, TRUE, TRUE),
    (1005, 1001, '部门管理', 'PAGE', 'SystemDepartments', '/system/departments', '/system/Department', 'department', 3, TRUE, TRUE),
    (1006, 1001, '操作记录', 'PAGE', 'audit', '/system/Audit', '/system/Audit', 'audit', 4, TRUE, TRUE),
    (1007, 1001, '服务信息', 'PAGE', 'server', '/system/Server', '/system/Server', 'server', 5, TRUE, TRUE);

UPDATE sys_menu SET icon = 'audit' WHERE id = 1006;
UPDATE sys_menu SET icon = 'server' WHERE id = 1007;
UPDATE sys_menu SET icon = 'user' WHERE id = 1002;
UPDATE sys_menu SET icon = 'role' WHERE id = 1003;
UPDATE sys_menu SET icon = 'menu' WHERE id = 1004;
UPDATE sys_menu SET icon = 'department' WHERE id = 1005;

-- 监控面板初始菜单
INSERT INTO sys_menu (id, parent_id, name, type, icon, sort_order, visible, enabled)
VALUES (1008, NULL, '监控面板', 'DIRECTORY', 'monitor', 1, TRUE, TRUE);
INSERT INTO sys_menu (id, parent_id, name, type, route_name, path, component_key, module_key, icon, sort_order, visible, enabled)
VALUES (1009, 1008, '接口文档', 'PAGE', 'MonitorApiDocs', '/monitor/ApiDocs', '/monitor/ApiDocs', 'api-docs', 'menu', 0, TRUE, TRUE);

-- 用户角色关联表.sql
CREATE TABLE sys_user_role (
    user_id BIGINT NOT NULL COMMENT '用户ID',
    role_id BIGINT NOT NULL COMMENT '角色ID',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间',
    created_by BIGINT NULL COMMENT '创建人ID',
    PRIMARY KEY (user_id, role_id),
    CONSTRAINT fk_user_role_user FOREIGN KEY (user_id) REFERENCES sys_user(id),
    CONSTRAINT fk_user_role_role FOREIGN KEY (role_id) REFERENCES sys_role(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='用户角色关系';
CREATE INDEX ix_user_role_role ON sys_user_role(role_id, user_id);

-- 用户部门关联表.sql
CREATE TABLE sys_user_dept (
    user_id BIGINT NOT NULL COMMENT '用户ID',
    dept_id BIGINT NOT NULL COMMENT '部门ID',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间',
    created_by BIGINT NULL COMMENT '创建人ID',
    PRIMARY KEY (user_id, dept_id),
    CONSTRAINT fk_user_dept_user FOREIGN KEY (user_id) REFERENCES sys_user(id),
    CONSTRAINT fk_user_dept_dept FOREIGN KEY (dept_id) REFERENCES sys_dept(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='用户部门关系';
CREATE INDEX ix_user_dept_dept ON sys_user_dept(dept_id, user_id);

-- 角色菜单关联表.sql
CREATE TABLE sys_role_menu (
    role_id BIGINT NOT NULL COMMENT '角色ID',
    menu_id BIGINT NOT NULL COMMENT '页面菜单ID',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间',
    created_by BIGINT NULL COMMENT '创建人ID',
    PRIMARY KEY (role_id, menu_id),
    CONSTRAINT fk_role_menu_role FOREIGN KEY (role_id) REFERENCES sys_role(id),
    CONSTRAINT fk_role_menu_menu FOREIGN KEY (menu_id) REFERENCES sys_menu(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='角色菜单关系';
CREATE INDEX ix_role_menu_menu ON sys_role_menu(menu_id, role_id);

-- 为 SUPER_ADMIN 补齐全部 PAGE，目录不入关联，重复执行不覆盖已有授权。
INSERT INTO sys_role_menu (role_id, menu_id)
SELECT r.id, m.id
FROM sys_role r
CROSS JOIN sys_menu m
WHERE r.code = 'SUPER_ADMIN' AND r.status = 'ENABLED'
  AND m.type = 'PAGE'
  AND NOT EXISTS (
      SELECT 1 FROM sys_role_menu rm WHERE rm.role_id = r.id AND rm.menu_id = m.id
  );

-- 操作审计表.sql
CREATE TABLE sys_operation_log (
    id BIGINT NOT NULL COMMENT '雪花ID',
    actor_id BIGINT NULL COMMENT '操作者ID',
    target_type VARCHAR(32) NOT NULL COMMENT '目标类型',
    target_id BIGINT NULL COMMENT '目标ID',
    action VARCHAR(96) NOT NULL COMMENT '动作编码',
    result VARCHAR(16) NOT NULL COMMENT '结果：SUCCESS、FAILURE、DENIED',
    reason_code VARCHAR(64) NULL COMMENT '原因编码',
    changes JSON NULL COMMENT '字段变更',
    trace_id VARCHAR(32) NULL COMMENT '链路ID',
    source_ip VARCHAR(45) NULL COMMENT '来源IP',
    client_summary VARCHAR(255) NULL COMMENT '客户端摘要',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间',
    PRIMARY KEY (id),
    CONSTRAINT ck_operation_result CHECK (result IN ('SUCCESS', 'FAILURE', 'DENIED'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='操作审计';
CREATE INDEX ix_operation_created ON sys_operation_log(created_at, id);
CREATE INDEX ix_operation_target ON sys_operation_log(target_type, target_id, created_at);

-- IP封禁表.sql
CREATE TABLE sys_ip_block (
    id BIGINT NOT NULL COMMENT '雪花ID',
    source_ip VARCHAR(45) NOT NULL COMMENT '规范化的IPv4或IPv6来源地址',
    status VARCHAR(16) NOT NULL COMMENT 'BLOCKED封禁、RELEASED解除',
    reason_code VARCHAR(64) NOT NULL COMMENT '封禁原因编码',
    failed_attempts INT NOT NULL COMMENT '触发封禁时的失败次数',
    window_seconds INT NOT NULL COMMENT '失败计数窗口秒数',
    blocked_at DATETIME(6) NOT NULL COMMENT '最近封禁时间',
    unblocked_at DATETIME(6) NULL COMMENT '最近解除时间',
    unblocked_by BIGINT NULL COMMENT '执行解除的历史用户ID，不关联账号生命周期',
    version BIGINT NOT NULL DEFAULT 0 COMMENT '并发版本及失败计数世代',
    PRIMARY KEY (id),
    CONSTRAINT uk_ip_block_source UNIQUE (source_ip),
    CONSTRAINT ck_ip_block_status CHECK (status IN ('BLOCKED', 'RELEASED')),
    CONSTRAINT ck_ip_block_counters CHECK (failed_attempts >= 0 AND window_seconds > 0 AND version >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='来源IP安全封禁';
CREATE INDEX ix_ip_block_status_time ON sys_ip_block(status, blocked_at, id);

-- 登录记录表.sql
CREATE TABLE sys_login_record (
    id BIGINT NOT NULL COMMENT '独立登录记录雪花ID，不是SessionID',
    user_id BIGINT NOT NULL COMMENT '历史用户ID，不随账号删除，不设外键',
    username VARCHAR(32) NOT NULL COMMENT '登录时账号快照',
    nickname VARCHAR(64) NULL COMMENT '登录时昵称快照',
    session_version BIGINT NOT NULL COMMENT '登录时安全版本，用于判断后续失效',
    source_ip VARCHAR(45) NOT NULL COMMENT '规范化可信来源IP',
    region_type VARCHAR(32) NOT NULL COMMENT '本机内网公网等来源分类',
    region VARCHAR(256) NOT NULL COMMENT '离线地区或配置本地网段名称',
    browser VARCHAR(64) NOT NULL COMMENT '浏览器类别，来自客户端声明',
    os VARCHAR(64) NOT NULL COMMENT '操作系统类别，来自客户端声明',
    login_at DATETIME(6) NOT NULL COMMENT '成功登录时间，北京时间',
    last_activity_at DATETIME(6) NOT NULL COMMENT '节流后最后记录活动时间，北京时间',
    absolute_expires_at DATETIME(6) NOT NULL COMMENT '登录时确定的绝对到期时间，北京时间',
    idle_timeout_seconds INT NOT NULL COMMENT '登录时闲置期限秒数',
    activity_interval_seconds INT NOT NULL COMMENT '登录时活动记录节流秒数',
    ended_at DATETIME(6) NULL COMMENT '明确结束时间，北京时间；超时可查询时推断',
    end_reason VARCHAR(32) NULL COMMENT '明确结束原因；未设置时按期限和账号安全版本判断',
    PRIMARY KEY (id),
    CONSTRAINT ck_login_record_limits CHECK (idle_timeout_seconds > 0 AND activity_interval_seconds > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='成功登录会话历史，无会话凭据';
CREATE INDEX ix_login_record_user_time ON sys_login_record(user_id, login_at, id);
CREATE INDEX ix_login_record_user_end ON sys_login_record(user_id, end_reason);
