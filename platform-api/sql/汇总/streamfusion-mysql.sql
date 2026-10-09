SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

-- BEGIN MYSQL DEFERRED DROP
SET @camera_default_fk = (SELECT COUNT(*) FROM information_schema.TABLE_CONSTRAINTS WHERE CONSTRAINT_SCHEMA=DATABASE() AND TABLE_NAME='camera_channel' AND CONSTRAINT_NAME='fk_camera_default_profile');
SET @camera_drop_sql = IF(@camera_default_fk > 0, 'ALTER TABLE camera_channel DROP FOREIGN KEY fk_camera_default_profile', 'SELECT 1');
PREPARE camera_drop_statement FROM @camera_drop_sql;
EXECUTE camera_drop_statement;
DEALLOCATE PREPARE camera_drop_statement;
-- END MYSQL DEFERRED DROP
DROP TABLE IF EXISTS `camera_access_import_item`;
DROP TABLE IF EXISTS `camera_access_job`;
DROP TABLE IF EXISTS `camera_user_channel_grant`;
DROP TABLE IF EXISTS `camera_user_group_grant`;
DROP TABLE IF EXISTS `camera_user_scope`;
DROP TABLE IF EXISTS `camera_create_request`;
DROP TABLE IF EXISTS `camera_profile_locator`;
DROP TABLE IF EXISTS `camera_stream_profile`;
DROP TABLE IF EXISTS `camera_channel`;
DROP TABLE IF EXISTS `camera_device`;
DROP TABLE IF EXISTS `camera_group`;
DROP TABLE IF EXISTS `camera_source_endpoint`;
DROP TABLE IF EXISTS `camera_source_credential`;
DROP TABLE IF EXISTS `camera_source`;
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
    component_key VARCHAR(64) NULL COMMENT '页面文件路径',
    module_key VARCHAR(64) NULL COMMENT '模块键',
    icon VARCHAR(64) NULL COMMENT '图标键',
    sort_order INT NOT NULL DEFAULT 0 COMMENT '排序值',
    navigation_group VARCHAR(16) NOT NULL DEFAULT 'BUSINESS' COMMENT '根导航分区，由系统维护',
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
    CONSTRAINT ck_menu_navigation_group CHECK (navigation_group IN ('BUSINESS', 'SYSTEM', 'MONITOR')),
    CONSTRAINT ck_menu_shape CHECK (
        (type = 'DIRECTORY' AND route_name IS NULL AND path IS NULL
         AND component_key IS NULL AND module_key IS NULL)
        OR (type = 'PAGE' AND route_name IS NOT NULL AND path IS NOT NULL
         AND component_key IS NOT NULL AND module_key IS NOT NULL)
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='菜单';
CREATE INDEX ix_menu_parent ON sys_menu(parent_id, sort_order, id);

-- 系统管理初始菜单
INSERT INTO sys_menu (id, parent_id, name, type, icon, sort_order, navigation_group, visible, enabled)
VALUES (1001, NULL, '系统管理', 'DIRECTORY', 'settings', 9000, 'SYSTEM', TRUE, TRUE);
-- 先创建目录，再插入带外键的子页面。
INSERT INTO sys_menu (id, parent_id, name, type, icon, sort_order, navigation_group, visible, enabled)
VALUES (1008, NULL, '监控面板', 'DIRECTORY', 'monitor', 10000, 'MONITOR', TRUE, TRUE);
INSERT INTO sys_menu (id, parent_id, name, type, route_name, path, component_key, module_key, icon, sort_order, visible, enabled)
VALUES
    (1002, 1001, '用户管理', 'PAGE', 'SystemUsers', '/system/users', '/system/User', 'user', 'user', 0, TRUE, TRUE),
    (1003, 1001, '角色管理', 'PAGE', 'SystemRoles', '/system/roles', '/system/Role', 'role', 'role', 1, TRUE, TRUE),
    (1004, 1001, '菜单管理', 'PAGE', 'SystemMenus', '/system/menus', '/system/Menu', 'menu', 'menu', 2, TRUE, TRUE),
    (1005, 1001, '部门管理', 'PAGE', 'SystemDepartments', '/system/departments', '/system/Department', 'department', 'department', 3, TRUE, TRUE),
    (1006, 1008, '操作记录', 'PAGE', 'audit', '/monitor/Audit', '/monitor/Audit', 'audit', 'audit', 0, TRUE, TRUE),
    (1007, 1008, '服务信息', 'PAGE', 'server', '/monitor/Server', '/monitor/Server', 'server', 'server', 1, TRUE, TRUE);

-- 监控面板初始菜单
INSERT INTO sys_menu (id, parent_id, name, type, route_name, path, component_key, module_key, icon, sort_order, visible, enabled)
VALUES (1009, 1008, '接口文档', 'PAGE', 'MonitorApiDocs', '/monitor/ApiDocs', '/monitor/ApiDocs', 'api-docs', 'menu', 2, TRUE, TRUE);

-- BEGIN CAMERA MENU SEEDS
INSERT INTO sys_menu (id, parent_id, name, type, icon, sort_order, visible, enabled)
VALUES (1100, NULL, '视频管理', 'DIRECTORY', 'monitor', 0, TRUE, TRUE);
INSERT INTO sys_menu (id, parent_id, name, type, route_name, path, component_key, module_key, icon, sort_order, visible, enabled)
VALUES
    (1101, 1100, '相机管理', 'PAGE', 'CameraManage', '/camera/manage', '/camera/Manage', 'camera', 'monitor', 0, TRUE, TRUE),
    (1103, 1100, '视频分组', 'PAGE', 'CameraGroups', '/camera/groups', '/camera-group/Manage', 'camera', 'department', 2, TRUE, TRUE),
    (1104, 1100, '相机授权', 'PAGE', 'CameraScopes', '/camera/scopes', '/camera-scope/Manage', 'camera_scope', 'role', 3, TRUE, TRUE);

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

-- 相机来源表.sql
CREATE TABLE camera_source (
 id BIGINT NOT NULL,
 name VARCHAR(100) NOT NULL,
 connection_category VARCHAR(16) NOT NULL DEFAULT 'DEVICE',
 adapter_type VARCHAR(64) NULL,
 vendor_hint VARCHAR(128) NULL,
 vendor_model_family VARCHAR(64) NULL,
 network_policy_key VARCHAR(64) NULL,
 rtsp_port INT NULL,
 enabled TINYINT NOT NULL DEFAULT 1,
 remark VARCHAR(500) NULL,
 version BIGINT NOT NULL DEFAULT 0,
 created_at DATETIME(6) NOT NULL, updated_at DATETIME(6) NOT NULL,
 created_by BIGINT NULL, updated_by BIGINT NULL,
 PRIMARY KEY(id),
 CONSTRAINT ck_camera_source_type CHECK(connection_category IN ('DEVICE','PLATFORM','RTSP') AND (adapter_type IS NULL OR (CHAR_LENGTH(adapter_type) BETWEEN 1 AND 64 AND adapter_type NOT IN ('AUTO','SCAN'))) AND vendor_model_family IS NULL),
 CONSTRAINT ck_camera_source_media_port CHECK(rtsp_port IS NULL OR rtsp_port BETWEEN 1 AND 65535),
 CONSTRAINT ck_camera_source_enabled CHECK(enabled IN (0,1)),
 CONSTRAINT ck_camera_source_version CHECK(version >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='相机接入源及控制协议配置';
CREATE INDEX ix_camera_source_filter ON camera_source(adapter_type,enabled,id);

-- 相机凭据表.sql
CREATE TABLE camera_source_credential (
 id BIGINT NOT NULL, source_id BIGINT NOT NULL, purpose VARCHAR(16) NOT NULL,
 secret_ciphertext VARBINARY(8192) NOT NULL, secret_nonce BINARY(12) NOT NULL, secret_tag BINARY(16) NOT NULL,
 encryption_key_id VARCHAR(64) NOT NULL,
 created_at DATETIME(6) NOT NULL, updated_at DATETIME(6) NOT NULL,
 PRIMARY KEY(id), UNIQUE(source_id,purpose), UNIQUE(source_id,id),
 CONSTRAINT fk_camera_credential_source FOREIGN KEY(source_id) REFERENCES camera_source(id),
 CONSTRAINT ck_camera_credential_purpose CHECK(purpose IN ('RTSP','ONVIF','VENDOR_HTTP','PLATFORM_HTTP','DEVICE_HTTP'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='来源固定用途加密凭据';

-- 相机端点表.sql
CREATE TABLE camera_source_endpoint (
 id BIGINT NOT NULL, source_id BIGINT NOT NULL, purpose VARCHAR(16) NOT NULL,
 scheme VARCHAR(8) NOT NULL, host VARCHAR(253) NOT NULL, port INT NOT NULL,
 base_path VARCHAR(1024) NOT NULL DEFAULT '', auth_mode VARCHAR(20) NOT NULL,
 credential_id BIGINT NULL, tls_policy VARCHAR(16) NOT NULL DEFAULT 'SYSTEM_CA',
 ca_bundle_key VARCHAR(64) NULL, cert_sha256 BINARY(32) NULL,
 created_at DATETIME(6) NOT NULL, updated_at DATETIME(6) NOT NULL,
 PRIMARY KEY(id), UNIQUE(source_id,purpose), UNIQUE(source_id,id),
 CONSTRAINT fk_camera_endpoint_source FOREIGN KEY(source_id) REFERENCES camera_source(id),
 CONSTRAINT fk_camera_endpoint_credential FOREIGN KEY(source_id,credential_id) REFERENCES camera_source_credential(source_id,id),
 CONSTRAINT ck_camera_endpoint_protocol CHECK((purpose='RTSP' AND scheme='rtsp' AND base_path='') OR (purpose='ONVIF' AND scheme IN ('http','https') AND base_path='/onvif/device_service') OR (purpose IN ('VENDOR_HTTP','DEVICE_HTTP') AND scheme IN ('http','https') AND base_path='') OR (purpose='PLATFORM_HTTP' AND scheme IN ('http','https') AND base_path LIKE '/%')),
 CONSTRAINT ck_camera_endpoint_port CHECK(port BETWEEN 1 AND 65535),
 CONSTRAINT ck_camera_endpoint_auth CHECK((auth_mode='NONE' AND credential_id IS NULL AND purpose<>'PLATFORM_HTTP') OR (auth_mode='DRIVER_NEGOTIATED' AND credential_id IS NOT NULL)),
 CONSTRAINT ck_camera_endpoint_tls CHECK(tls_policy='SYSTEM_CA' AND ca_bundle_key IS NULL AND cert_sha256 IS NULL)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='相机来源端点';

-- 视频分组表.sql
CREATE TABLE camera_group (
 id BIGINT NOT NULL COMMENT '雪花视频组ID',
 parent_id BIGINT NULL COMMENT '上级视频组ID',
 name VARCHAR(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_ci NOT NULL COMMENT '同级名称',
 sort_order INT NOT NULL DEFAULT 0 COMMENT '同级排序',
 remark VARCHAR(500) NULL COMMENT '备注',
 version BIGINT NOT NULL DEFAULT 0 COMMENT '编辑版本',
 created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间',
 updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '更新时间',
 created_by BIGINT NULL COMMENT '创建人ID快照',
 updated_by BIGINT NULL COMMENT '更新人ID快照',
 PRIMARY KEY(id),
 CONSTRAINT fk_camera_group_parent FOREIGN KEY(parent_id) REFERENCES camera_group(id),
 CONSTRAINT ck_camera_group_id CHECK(id>0),
 CONSTRAINT ck_camera_group_parent CHECK(parent_id IS NULL OR (parent_id>0 AND parent_id!=id)),
 CONSTRAINT ck_camera_group_version CHECK(version>=0),
 CONSTRAINT ck_camera_group_order CHECK(sort_order>=0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='独立视频分组';
CREATE INDEX ix_camera_group_parent ON camera_group(parent_id,sort_order,id);
CREATE INDEX ix_camera_group_name ON camera_group(parent_id,name);

-- 相机设备表.sql
CREATE TABLE camera_device (
    id BIGINT NOT NULL COMMENT '本地设备雪花ID',
    source_id BIGINT NOT NULL COMMENT '接入源ID',
    external_device_key VARCHAR(512) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin NOT NULL COMMENT '来源内稳定设备键',
    external_device_ref VARCHAR(512) NULL COMMENT '上游原始设备标识，不含秘密',
    device_type VARCHAR(16) NOT NULL DEFAULT 'UNKNOWN' COMMENT 'IPC/NVR/DVR/ENCODER/UNKNOWN',
    source_name VARCHAR(128) NULL COMMENT '上游设备名称',
    local_name VARCHAR(128) NULL COMMENT '本地显示名称，空时沿用来源名称',
    remark VARCHAR(500) NULL COMMENT '本地设备备注',
    manufacturer VARCHAR(128) NULL COMMENT '观测制造商',
    model VARCHAR(128) NULL COMMENT '观测型号',
    serial_number VARCHAR(128) NULL COMMENT '设备序列号',
    firmware_version VARCHAR(128) NULL COMMENT '观测固件版本',
    info_observed_at DATETIME(6) NULL COMMENT '信息观测时间，Asia/Shanghai',
    version BIGINT NOT NULL DEFAULT 0 COMMENT '身份配置编辑版本',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间，Asia/Shanghai',
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '身份配置更新时间，Asia/Shanghai',
    created_by BIGINT NULL COMMENT '创建人ID',
    updated_by BIGINT NULL COMMENT '身份配置修改人ID',
    PRIMARY KEY (id),
    UNIQUE KEY uk_camera_device_external (source_id,external_device_key),
    UNIQUE KEY uk_camera_device_source (source_id,id),
    CONSTRAINT fk_camera_device_source FOREIGN KEY (source_id) REFERENCES camera_source(id),
    CONSTRAINT ck_camera_device_version CHECK (version>=0),
    CONSTRAINT ck_camera_device_type CHECK (device_type IN ('IPC','NVR','DVR','ENCODER','UNKNOWN'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='来源内设备档案';

-- 相机通道表.sql
CREATE TABLE camera_channel (
    id BIGINT NOT NULL COMMENT '独立授权画面雪花ID，即cameraId',
    source_id BIGINT NOT NULL COMMENT '接入源ID',
    device_id BIGINT NULL COMMENT '来源内设备ID，手工可空',
    external_channel_key VARCHAR(512) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin NOT NULL COMMENT '来源内稳定通道键',
    external_channel_ref VARCHAR(512) NULL COMMENT '上游通道原始标识',
    group_id BIGINT NULL COMMENT '视频分组ID，待归档为空',
    default_preview_profile_id BIGINT NULL COMMENT '明确选定的本通道默认码流',
    name VARCHAR(128) NOT NULL COMMENT '本地相机名称',
    source_name VARCHAR(128) NULL COMMENT '来源通道名称',
    remark VARCHAR(500) NULL COMMENT '本地备注，不含秘密',
    lifecycle VARCHAR(24) NOT NULL DEFAULT 'PENDING_ASSIGNMENT' COMMENT 'PENDING_ASSIGNMENT/ENABLED/DISABLED',
    mapping_origin VARCHAR(16) NOT NULL COMMENT 'MANUAL/ADAPTER',
    catalog_observed_at DATETIME(6) NULL COMMENT '目录可靠观测时间，Asia/Shanghai',
    version BIGINT NOT NULL DEFAULT 0 COMMENT '资产编辑版本',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间，Asia/Shanghai',
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '编辑更新时间，Asia/Shanghai',
    created_by BIGINT NULL COMMENT '创建人ID',
    updated_by BIGINT NULL COMMENT '编辑人ID',
    PRIMARY KEY (id),
    UNIQUE KEY uk_camera_channel_external (source_id,external_channel_key),
    UNIQUE KEY uk_camera_channel_source (source_id,id),
    KEY ix_camera_channel_group (group_id,lifecycle,id),
    KEY ix_camera_channel_device (device_id,id),
    KEY ix_camera_channel_lifecycle (lifecycle,id),
    CONSTRAINT fk_camera_channel_source FOREIGN KEY (source_id) REFERENCES camera_source(id),
    CONSTRAINT fk_camera_channel_device FOREIGN KEY (source_id,device_id) REFERENCES camera_device(source_id,id),
    CONSTRAINT fk_camera_channel_group FOREIGN KEY (group_id) REFERENCES camera_group(id),
    CONSTRAINT ck_camera_channel_version CHECK (version>=0),
    CONSTRAINT ck_camera_channel_mapping CHECK (mapping_origin IN ('MANUAL','ADAPTER')),
    CONSTRAINT ck_camera_channel_lifecycle CHECK ((lifecycle='PENDING_ASSIGNMENT' AND group_id IS NULL) OR (lifecycle IN ('ENABLED','DISABLED') AND group_id IS NOT NULL))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='独立授权相机画面';

-- 相机码流表.sql
CREATE TABLE camera_stream_profile (
    id BIGINT NOT NULL COMMENT '码流档案雪花ID',
    source_id BIGINT NOT NULL COMMENT '与通道同源的受约束来源ID',
    channel_id BIGINT NOT NULL COMMENT '相机通道ID',
    external_profile_key VARCHAR(512) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin NOT NULL COMMENT '通道内稳定码流键',
    label VARCHAR(64) NOT NULL COMMENT '本地码流标签',
    source_label VARCHAR(128) NULL COMMENT '上游码流名称',
    usage_hint VARCHAR(16) NOT NULL DEFAULT 'UNKNOWN' COMMENT 'MAIN/SUB/THIRD/CUSTOM/UNKNOWN',
    usage_origin VARCHAR(16) NOT NULL DEFAULT 'UNKNOWN' COMMENT 'DEVICE_REPORTED/MANUAL/UNKNOWN',
    enabled TINYINT(1) NOT NULL DEFAULT 1 COMMENT '允许新使用',
    video_codec VARCHAR(32) NULL COMMENT '可靠观测视频编码',
    width INT NULL COMMENT '可靠观测像素宽',
    height INT NULL COMMENT '可靠观测像素高',
    frame_rate DECIMAL(8,3) NULL COMMENT '可靠观测帧率fps',
    bitrate_kbps BIGINT NULL COMMENT '可靠观测或设备声明码率kbps',
    audio_codec VARCHAR(32) NULL COMMENT '观测音频编码',
    has_audio TINYINT(1) NULL COMMENT 'NULL未知，0无音轨，1有音轨',
    parameters_observed_at DATETIME(6) NULL COMMENT '参数观测时间，Asia/Shanghai',
    parameters_origin VARCHAR(16) NULL COMMENT 'CATALOG/MEDIA',
    version BIGINT NOT NULL DEFAULT 0 COMMENT '标签用途启停及locator共享编辑版本',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间，Asia/Shanghai',
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '编辑更新时间，Asia/Shanghai',
    created_by BIGINT NULL COMMENT '创建人ID',
    updated_by BIGINT NULL COMMENT '编辑人ID',
    PRIMARY KEY (id),
    UNIQUE KEY uk_camera_profile_external (channel_id,external_profile_key),
    UNIQUE KEY uk_camera_profile_channel (channel_id,id),
    UNIQUE KEY uk_camera_profile_source (source_id,id),
    KEY ix_camera_profile_enabled (channel_id,enabled,id),
    CONSTRAINT fk_camera_profile_channel FOREIGN KEY (source_id,channel_id) REFERENCES camera_channel(source_id,id),
    CONSTRAINT ck_camera_profile_version CHECK (version>=0),
    CONSTRAINT ck_camera_profile_enabled CHECK (enabled IN (0,1)),
    CONSTRAINT ck_camera_profile_usage CHECK (usage_hint IN ('MAIN','SUB','THIRD','CUSTOM','UNKNOWN')),
    CONSTRAINT ck_camera_profile_usage_origin CHECK (usage_origin IN ('DEVICE_REPORTED','MANUAL','UNKNOWN')),
    CONSTRAINT ck_camera_profile_parameters CHECK ((width IS NULL OR width>0) AND (height IS NULL OR height>0) AND (frame_rate IS NULL OR frame_rate>0) AND (bitrate_kbps IS NULL OR bitrate_kbps>0) AND (has_audio IS NULL OR has_audio IN (0,1))),
    CONSTRAINT ck_camera_profile_parameters_origin CHECK (parameters_origin IS NULL OR parameters_origin IN ('CATALOG','MEDIA'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='通道多码流档案';

-- 相机码流定位表.sql
CREATE TABLE camera_profile_locator (
 profile_id BIGINT NOT NULL, source_id BIGINT NOT NULL,
 locator_kind VARCHAR(12) NOT NULL, endpoint_purpose VARCHAR(16) NOT NULL,
 rtsp_host_mode VARCHAR(8) NULL, rtsp_host VARCHAR(253) NULL, rtsp_port INT NULL,
 rtsp_secret_ciphertext VARBINARY(16384) NULL, rtsp_secret_nonce BINARY(12) NULL,
 rtsp_secret_tag BINARY(16) NULL, rtsp_encryption_key_id VARCHAR(64) NULL,
 rtsp_transport VARCHAR(8) NULL, identity_digest BINARY(32) NOT NULL,
 protocol_secret_ciphertext VARBINARY(16384) NULL, protocol_secret_nonce BINARY(12) NULL,
 protocol_secret_tag BINARY(16) NULL, protocol_encryption_key_id VARCHAR(64) NULL,
 created_at DATETIME(6) NOT NULL, updated_at DATETIME(6) NOT NULL,
 PRIMARY KEY(profile_id), UNIQUE(source_id,identity_digest),
 CONSTRAINT fk_camera_locator_source FOREIGN KEY(source_id) REFERENCES camera_source(id),
 CONSTRAINT fk_camera_locator_profile FOREIGN KEY(source_id,profile_id) REFERENCES camera_stream_profile(source_id,id),
 CONSTRAINT fk_camera_locator_endpoint FOREIGN KEY(source_id,endpoint_purpose) REFERENCES camera_source_endpoint(source_id,purpose),
 CONSTRAINT ck_camera_locator_kind CHECK((locator_kind='RTSP' AND endpoint_purpose='RTSP' AND rtsp_transport IS NOT NULL AND rtsp_transport='TCP' AND rtsp_host_mode IS NOT NULL AND rtsp_secret_ciphertext IS NOT NULL AND rtsp_secret_nonce IS NOT NULL AND rtsp_secret_tag IS NOT NULL AND rtsp_encryption_key_id IS NOT NULL AND protocol_secret_ciphertext IS NULL AND protocol_secret_nonce IS NULL AND protocol_secret_tag IS NULL AND protocol_encryption_key_id IS NULL) OR (locator_kind IN ('ONVIF','HIKVISION','DAHUA','HIK_PLATFORM') AND ((locator_kind='ONVIF' AND endpoint_purpose IN ('ONVIF','DEVICE_HTTP')) OR (locator_kind IN ('HIKVISION','DAHUA') AND endpoint_purpose IN ('VENDOR_HTTP','DEVICE_HTTP')) OR (locator_kind='HIK_PLATFORM' AND endpoint_purpose='PLATFORM_HTTP')) AND rtsp_host_mode IS NULL AND rtsp_host IS NULL AND rtsp_port IS NULL AND rtsp_secret_ciphertext IS NULL AND rtsp_secret_nonce IS NULL AND rtsp_secret_tag IS NULL AND rtsp_encryption_key_id IS NULL AND rtsp_transport IS NULL AND protocol_secret_ciphertext IS NOT NULL AND protocol_secret_nonce IS NOT NULL AND protocol_secret_tag IS NOT NULL AND protocol_encryption_key_id IS NOT NULL)),
 CONSTRAINT ck_camera_locator_host CHECK(locator_kind<>'RTSP' OR ((rtsp_host_mode='SOURCE' AND rtsp_host IS NULL AND rtsp_port IS NULL) OR (rtsp_host_mode='EXPLICIT' AND rtsp_host IS NOT NULL AND rtsp_port IS NOT NULL AND rtsp_port BETWEEN 1 AND 65535)))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='Profile持久定位：RTSP秘密或协议身份，不保存平台临时播放地址';

-- 相机创建回执表.sql
CREATE TABLE camera_create_request (
    id BIGINT NOT NULL COMMENT '创建回执雪花ID',
    actor_user_id BIGINT NOT NULL COMMENT '实际用户ID，无用户外键',
    actor_session_digest BINARY(32) NOT NULL COMMENT '已验证会话HMAC摘要',
    session_digest_key_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '会话摘要密钥ID',
    client_request_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '13位Unix毫秒-UUIDv4',
    operation VARCHAR(24) NOT NULL COMMENT 'CREATE_SOURCE/CREATE_CAMERA/ADD_PROFILE',
    parent_resource_id BIGINT NULL COMMENT '所属sourceId或cameraId',
    request_fingerprint BINARY(32) NOT NULL COMMENT '规范化请求HMAC摘要',
    fingerprint_key_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '请求摘要密钥ID',
    resource_id BIGINT NOT NULL COMMENT '已创建资源稳定ID',
    result_summary JSON NOT NULL COMMENT '有界安全回执，不含秘密',
    requested_at DATETIME(6) NOT NULL COMMENT '请求键时间，Asia/Shanghai',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '成功创建时间，Asia/Shanghai',
    expires_at DATETIME(6) NOT NULL COMMENT '回执到期时间，Asia/Shanghai',
    PRIMARY KEY (id),
    UNIQUE KEY uk_camera_create_request (actor_user_id,actor_session_digest,client_request_id),
    KEY ix_camera_create_expiry (expires_at,id),
    KEY ix_camera_create_actor (actor_user_id,client_request_id),
    CONSTRAINT ck_camera_create_operation CHECK (operation IN ('CREATE_SOURCE','CREATE_CAMERA','ADD_PROFILE'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='同步相机资产创建回执';

-- 账户相机范围表.sql
CREATE TABLE camera_user_scope (
 user_id BIGINT NOT NULL COMMENT '授权所属用户ID',
 version BIGINT NOT NULL DEFAULT 0 COMMENT '两类关系的聚合编辑版本',
 created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '首次保存时间',
 updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '最近变更时间',
 created_by BIGINT NULL COMMENT '创建人ID快照',
 updated_by BIGINT NULL COMMENT '更新人ID快照',
 PRIMARY KEY(user_id),
 CONSTRAINT fk_camera_scope_user FOREIGN KEY(user_id) REFERENCES sys_user(id) ON DELETE CASCADE,
 CONSTRAINT ck_camera_scope_version CHECK(version>=0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='账户相机范围聚合';

-- 账户视频组授权表.sql
CREATE TABLE camera_user_group_grant (
 user_id BIGINT NOT NULL COMMENT '授权所属用户ID',
 group_id BIGINT NOT NULL COMMENT '授予的视频组ID及动态后代',
 created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '授予时间',
 created_by BIGINT NULL COMMENT '授予人ID快照',
 PRIMARY KEY(user_id,group_id),
 CONSTRAINT fk_camera_group_grant_scope FOREIGN KEY(user_id) REFERENCES camera_user_scope(user_id) ON DELETE CASCADE,
 CONSTRAINT fk_camera_group_grant_group FOREIGN KEY(group_id) REFERENCES camera_group(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='账户视频组授权';
CREATE INDEX ix_camera_group_grant_group ON camera_user_group_grant(group_id,user_id);

-- 账户相机授权表.sql
CREATE TABLE camera_user_channel_grant (
 user_id BIGINT NOT NULL COMMENT '授权所属用户ID',
 channel_id BIGINT NOT NULL COMMENT '授予的业务相机通道ID',
 created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '授予时间',
 created_by BIGINT NULL COMMENT '授予人ID快照',
 PRIMARY KEY(user_id,channel_id),
 CONSTRAINT fk_camera_channel_grant_scope FOREIGN KEY(user_id) REFERENCES camera_user_scope(user_id) ON DELETE CASCADE,
 CONSTRAINT fk_camera_channel_grant_channel FOREIGN KEY(channel_id) REFERENCES camera_channel(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='账户直接相机授权';
CREATE INDEX ix_camera_channel_grant_channel ON camera_user_channel_grant(channel_id,user_id);

-- 相机接入任务表.sql
CREATE TABLE camera_access_job (
    id BIGINT NOT NULL COMMENT '发现任务雪花ID',
    actor_user_id BIGINT NOT NULL COMMENT '发起用户，不继承重建账号身份',
    actor_session_digest BINARY(32) NOT NULL COMMENT '发起会话HMAC，不保存明文会话ID',
    digest_key_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '会话和请求摘要密钥版本',
    client_request_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL COMMENT '带时间戳的幂等请求键',
    request_fingerprint BINARY(32) NOT NULL COMMENT '请求HMAC',
    status VARCHAR(16) NOT NULL COMMENT 'QUEUED/RUNNING/SUCCEEDED/PARTIAL/FAILED/CANCELLED/EXPIRED',
    method VARCHAR(64) NOT NULL COMMENT '请求或实际识别的接入方式',
    job_kind VARCHAR(16) NOT NULL DEFAULT 'CATALOG' COMMENT 'CATALOG/SCAN/BULK_IMPORT',
    source_id BIGINT NULL COMMENT '显式复用或导入后的来源ID，无删除级联',
    source_version BIGINT NULL COMMENT '任务绑定的来源版本',
    bulk_group_id BIGINT NULL COMMENT '整批导入目标组，历史引用',
    bulk_next_page INT NOT NULL DEFAULT 1 COMMENT '整批导入下一页',
    bulk_total BIGINT NULL COMMENT '上游最近报告总量，不是全量快照',
    bulk_processed_count INT NOT NULL DEFAULT 0 COMMENT '已处理不同上游身份数',
    bulk_created_count INT NOT NULL DEFAULT 0 COMMENT '新建数',
    bulk_existing_count INT NOT NULL DEFAULT 0 COMMENT '匹配已有数',
    bulk_failed_count INT NOT NULL DEFAULT 0 COMMENT '逐项失败数，不含读取整页失败',
    bulk_duplicate_count INT NOT NULL DEFAULT 0 COMMENT '分页重复身份数',
    bulk_started_at DATETIME(6) NULL COMMENT '整批导入开始时间',
    version BIGINT NOT NULL DEFAULT 0 COMMENT '状态写入与取消围栏',
    encryption_key_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL COMMENT '暂存密钥版本',
    secret_nonce BINARY(12) NULL COMMENT 'AES-GCM nonce',
    secret_ciphertext MEDIUMBLOB NULL COMMENT '最大1MiB加密暂存；普通目录15分钟，整批30分钟',
    secret_tag BINARY(16) NULL COMMENT 'AES-GCM tag',
    reason_code VARCHAR(64) NULL COMMENT '固定安全诊断码，不存上游响应',
    origin_trace_id VARCHAR(64) NULL COMMENT '发起请求追踪ID',
    result_summary JSON NULL COMMENT '有界导入ID回执，不含凭据或URL',
    import_request_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL COMMENT '导入幂等请求键',
    import_fingerprint BINARY(32) NULL COMMENT '导入版本及选集HMAC',
    created_at DATETIME(6) NOT NULL COMMENT '创建时间',
    updated_at DATETIME(6) NOT NULL COMMENT '状态变化时间',
    expires_at DATETIME(6) NOT NULL COMMENT '目录和凭据保留期限',
    PRIMARY KEY (id),
    UNIQUE KEY uk_camera_access_request (actor_user_id,actor_session_digest,client_request_id),
    KEY ix_camera_access_expiry (expires_at,id),
    KEY ix_camera_access_actor (actor_user_id,created_at),
    KEY ix_camera_access_state (status,expires_at),
    CONSTRAINT ck_camera_access_status CHECK (status IN ('QUEUED','RUNNING','SUCCEEDED','PARTIAL','FAILED','CANCELLED','EXPIRED')),
    CONSTRAINT ck_access_job_kind CHECK (job_kind IN ('CATALOG','SCAN','BULK_IMPORT')),
    CONSTRAINT ck_access_job_bulk_counts CHECK (bulk_next_page BETWEEN 1 AND 101 AND (bulk_total IS NULL OR bulk_total >= 0) AND bulk_processed_count BETWEEN 0 AND 10000 AND bulk_created_count >= 0 AND bulk_existing_count >= 0 AND bulk_failed_count >= 0 AND bulk_duplicate_count >= 0 AND bulk_processed_count = bulk_created_count + bulk_existing_count + bulk_failed_count AND (source_version IS NULL OR source_version >= 0)),
    CONSTRAINT ck_camera_access_version CHECK (version>=0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='相机发现与有界整批导入任务';

-- 相机导入明细表.sql
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

ALTER TABLE camera_channel ADD CONSTRAINT fk_camera_default_profile FOREIGN KEY (id,default_preview_profile_id) REFERENCES camera_stream_profile(channel_id,id);
