CREATE TABLE sys_user (
    id BIGINT NOT NULL AUTO_INCREMENT,
    username VARCHAR(32) NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    nickname VARCHAR(64) NOT NULL,
    avatar_key VARCHAR(64) NULL,
    dept_id BIGINT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'ENABLED',
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    must_change_password BOOLEAN NOT NULL DEFAULT TRUE,
    session_version BIGINT NOT NULL DEFAULT 0,
    failed_login_count INT NOT NULL DEFAULT 0,
    locked_until DATETIME(6) NULL,
    version BIGINT NOT NULL DEFAULT 0,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    created_by BIGINT NULL,
    updated_by BIGINT NULL,
    PRIMARY KEY (id),
    CONSTRAINT uq_user_username UNIQUE (username),
    CONSTRAINT fk_user_dept FOREIGN KEY (dept_id) REFERENCES sys_dept(id),
    CONSTRAINT ck_user_status CHECK (status IN ('ENABLED', 'DISABLED')),
    CONSTRAINT ck_user_versions CHECK (version >= 0 AND session_version >= 0),
    CONSTRAINT ck_user_failures CHECK (failed_login_count >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE INDEX ix_user_dept ON sys_user(dept_id, is_deleted);
CREATE INDEX ix_user_created ON sys_user(is_deleted, created_at, id);
