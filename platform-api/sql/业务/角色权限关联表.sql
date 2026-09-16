SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `sys_role_permission`;
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
