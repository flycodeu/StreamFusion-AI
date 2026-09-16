SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `sys_role`;
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
