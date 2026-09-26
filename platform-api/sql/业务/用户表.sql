SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `sys_user`;
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
