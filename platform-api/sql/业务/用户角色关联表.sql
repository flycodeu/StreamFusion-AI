SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `sys_user_role`;
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
