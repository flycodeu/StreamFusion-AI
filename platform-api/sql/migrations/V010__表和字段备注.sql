-- Add metadata comments without changing previously applied migrations.
-- MySQL executable comments keep this metadata-only migration a no-op on H2.
-- Column types, nullability, defaults and keys must remain unchanged.

/*!80016 ALTER TABLE sys_dept
    COMMENT = '部门组织表，公司、部门、组统一为树节点，不参与授权',
    MODIFY COLUMN id BIGINT NOT NULL AUTO_INCREMENT COMMENT '主键ID，自增',
    MODIFY COLUMN parent_id BIGINT NULL COMMENT '父部门ID，空表示根节点',
    MODIFY COLUMN name VARCHAR(64) NOT NULL COMMENT '部门或组织节点名称',
    MODIFY COLUMN sort_order INT NOT NULL DEFAULT 0 COMMENT '同级展示顺序，数值越小越靠前',
    MODIFY COLUMN is_deleted BOOLEAN NOT NULL DEFAULT FALSE COMMENT '逻辑删除标记：0未删除，1已删除',
    MODIFY COLUMN version BIGINT NOT NULL DEFAULT 0 COMMENT '编辑版本号，用于乐观锁和并发修改校验',
    MODIFY COLUMN created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间，UTC',
    MODIFY COLUMN updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '最后更新时间，UTC，由业务写入维护',
    MODIFY COLUMN created_by BIGINT NULL COMMENT '创建人用户ID，系统初始化时可为空',
    MODIFY COLUMN updated_by BIGINT NULL COMMENT '最后修改人用户ID，系统初始化时可为空'
*/;

/*!80016 ALTER TABLE sys_user
    COMMENT = '用户账号表，保存基础资料和安全状态',
    MODIFY COLUMN id BIGINT NOT NULL AUTO_INCREMENT COMMENT '主键ID，自增',
    MODIFY COLUMN username VARCHAR(32) NOT NULL COMMENT '登录账号，全表唯一，包含已删除账号',
    MODIFY COLUMN password_hash VARCHAR(255) NOT NULL COMMENT '带盐密码哈希，禁止存储明文或对外返回',
    MODIFY COLUMN nickname VARCHAR(64) NOT NULL COMMENT '用户显示昵称',
    MODIFY COLUMN avatar_key VARCHAR(64) NULL COMMENT '内置头像资源标识，空表示默认头像',
    MODIFY COLUMN dept_id BIGINT NULL COMMENT '所属部门ID，可为空，仅表示组织归属',
    MODIFY COLUMN status VARCHAR(16) NOT NULL DEFAULT 'ENABLED' COMMENT '账号状态：ENABLED正常，DISABLED禁用',
    MODIFY COLUMN is_deleted BOOLEAN NOT NULL DEFAULT FALSE COMMENT '逻辑删除标记：0未删除，1已删除',
    MODIFY COLUMN must_change_password BOOLEAN NOT NULL DEFAULT TRUE COMMENT '是否必须修改密码：1是，0否',
    MODIFY COLUMN session_version BIGINT NOT NULL DEFAULT 0 COMMENT '会话安全版本，安全事件递增以使旧会话失效',
    MODIFY COLUMN failed_login_count INT NOT NULL DEFAULT 0 COMMENT '连续登录失败次数',
    MODIFY COLUMN locked_until DATETIME(6) NULL COMMENT '登录冷却截止时间，UTC，空表示无冷却',
    MODIFY COLUMN version BIGINT NOT NULL DEFAULT 0 COMMENT '编辑版本号，用于乐观锁和并发修改校验',
    MODIFY COLUMN created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间，UTC',
    MODIFY COLUMN updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '最后更新时间，UTC，由业务写入维护',
    MODIFY COLUMN created_by BIGINT NULL COMMENT '创建人用户ID，系统初始化时可为空',
    MODIFY COLUMN updated_by BIGINT NULL COMMENT '最后修改人用户ID，系统初始化时可为空'
*/;

/*!80016 ALTER TABLE sys_role
    COMMENT = '角色表，通过用户角色关系授予个人权限',
    MODIFY COLUMN id BIGINT NOT NULL AUTO_INCREMENT COMMENT '主键ID，自增',
    MODIFY COLUMN code VARCHAR(64) NOT NULL COMMENT '唯一角色编码，SUPER_ADMIN为受保护超级管理员',
    MODIFY COLUMN name VARCHAR(64) NOT NULL COMMENT '角色显示名称',
    MODIFY COLUMN description VARCHAR(255) NULL COMMENT '角色用途说明',
    MODIFY COLUMN status VARCHAR(16) NOT NULL DEFAULT 'ENABLED' COMMENT '角色状态：ENABLED启用，DISABLED停用',
    MODIFY COLUMN is_deleted BOOLEAN NOT NULL DEFAULT FALSE COMMENT '逻辑删除标记：0未删除，1已删除',
    MODIFY COLUMN version BIGINT NOT NULL DEFAULT 0 COMMENT '编辑版本号，用于乐观锁和并发修改校验',
    MODIFY COLUMN created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间，UTC',
    MODIFY COLUMN updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '最后更新时间，UTC，由业务写入维护',
    MODIFY COLUMN created_by BIGINT NULL COMMENT '创建人用户ID，系统初始化时可为空',
    MODIFY COLUMN updated_by BIGINT NULL COMMENT '最后修改人用户ID，系统初始化时可为空'
*/;

/*!80016 ALTER TABLE sys_permission
    COMMENT = '权限目录表，只登记已实现的后端功能权限',
    MODIFY COLUMN id BIGINT NOT NULL AUTO_INCREMENT COMMENT '主键ID，自增',
    MODIFY COLUMN code VARCHAR(96) NOT NULL COMMENT '唯一权限编码，例如system:user:read',
    MODIFY COLUMN name VARCHAR(64) NOT NULL COMMENT '权限显示名称',
    MODIFY COLUMN resource VARCHAR(64) NOT NULL COMMENT '所属资源分组，用于权限目录展示',
    MODIFY COLUMN description VARCHAR(255) NULL COMMENT '权限能力与使用边界说明'
*/;

/*!80016 ALTER TABLE sys_menu
    COMMENT = '菜单路由表，导航入口关联权限，不独立授予API权限',
    MODIFY COLUMN id BIGINT NOT NULL AUTO_INCREMENT COMMENT '主键ID，自增',
    MODIFY COLUMN parent_id BIGINT NULL COMMENT '父菜单ID，空表示根节点，父节点应为目录',
    MODIFY COLUMN name VARCHAR(64) NOT NULL COMMENT '菜单显示名称',
    MODIFY COLUMN type VARCHAR(16) NOT NULL COMMENT '菜单类型：DIRECTORY目录，PAGE页面',
    MODIFY COLUMN route_name VARCHAR(64) NULL COMMENT '页面唯一前端路由名称，目录为空',
    MODIFY COLUMN path VARCHAR(200) NULL COMMENT '页面唯一站内路由路径，目录为空',
    MODIFY COLUMN component_key VARCHAR(64) NULL COMMENT '前端已编译组件白名单标识，目录为空',
    MODIFY COLUMN required_permission_id BIGINT NULL COMMENT '页面入口所需权限ID，目录为空',
    MODIFY COLUMN icon VARCHAR(64) NULL COMMENT '前端内置图标标识',
    MODIFY COLUMN sort_order INT NOT NULL DEFAULT 0 COMMENT '同级展示顺序，数值越小越靠前',
    MODIFY COLUMN visible BOOLEAN NOT NULL DEFAULT TRUE COMMENT '是否显示导航：1显示，0隐藏，不撤销API权限',
    MODIFY COLUMN enabled BOOLEAN NOT NULL DEFAULT TRUE COMMENT '是否启用路由：1启用，0停用，不撤销API权限',
    MODIFY COLUMN is_deleted BOOLEAN NOT NULL DEFAULT FALSE COMMENT '逻辑删除标记：0未删除，1已删除',
    MODIFY COLUMN version BIGINT NOT NULL DEFAULT 0 COMMENT '编辑版本号，用于乐观锁和并发修改校验',
    MODIFY COLUMN created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间，UTC',
    MODIFY COLUMN updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '最后更新时间，UTC，由业务写入维护',
    MODIFY COLUMN created_by BIGINT NULL COMMENT '创建人用户ID，系统初始化时可为空',
    MODIFY COLUMN updated_by BIGINT NULL COMMENT '最后修改人用户ID，系统初始化时可为空'
*/;

/*!80016 ALTER TABLE sys_user_role
    COMMENT = '用户角色关联表，仅个人赋权，不经部门继承',
    MODIFY COLUMN user_id BIGINT NOT NULL COMMENT '用户ID，与角色ID组成联合主键',
    MODIFY COLUMN role_id BIGINT NOT NULL COMMENT '授予的角色ID，与用户ID组成联合主键',
    MODIFY COLUMN created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '角色授予时间，UTC',
    MODIFY COLUMN created_by BIGINT NULL COMMENT '角色授予人用户ID，初始化时可为空'
*/;

/*!80016 ALTER TABLE sys_role_permission
    COMMENT = '角色权限关联表，保存普通角色的显式权限集合',
    MODIFY COLUMN role_id BIGINT NOT NULL COMMENT '角色ID，与权限ID组成联合主键',
    MODIFY COLUMN permission_id BIGINT NOT NULL COMMENT '权限ID，与角色ID组成联合主键',
    MODIFY COLUMN created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '权限授予时间，UTC',
    MODIFY COLUMN created_by BIGINT NULL COMMENT '权限授予人用户ID，初始化时可为空'
*/;

/*!80016 ALTER TABLE sys_operation_log
    COMMENT = '操作审计表，记录安全操作和受控字段差异',
    MODIFY COLUMN id BIGINT NOT NULL AUTO_INCREMENT COMMENT '主键ID，自增',
    MODIFY COLUMN actor_id BIGINT NULL COMMENT '操作者用户ID，匿名或系统操作可为空',
    MODIFY COLUMN target_type VARCHAR(32) NOT NULL COMMENT '操作目标类型，例如USER、ROLE、DEPT',
    MODIFY COLUMN target_id BIGINT NULL COMMENT '操作目标ID，无具体目标时为空',
    MODIFY COLUMN action VARCHAR(96) NOT NULL COMMENT '审计动作编码',
    MODIFY COLUMN result VARCHAR(16) NOT NULL COMMENT '操作结果：SUCCESS成功，FAILURE失败，DENIED拒绝',
    MODIFY COLUMN reason_code VARCHAR(64) NULL COMMENT '失败或拒绝原因编码，不保存敏感原文',
    MODIFY COLUMN changes JSON NULL COMMENT '白名单字段变更差异，禁止写入密码或会话凭证',
    MODIFY COLUMN trace_id VARCHAR(32) NULL COMMENT '请求跟踪标识，关联服务日志',
    MODIFY COLUMN created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '审计记录时间，UTC'
*/;
