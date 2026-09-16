SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `sys_user`;
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
