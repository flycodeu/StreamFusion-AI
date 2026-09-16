CREATE TABLE sys_role (
    id BIGINT NOT NULL AUTO_INCREMENT,
    code VARCHAR(64) NOT NULL,
    name VARCHAR(64) NOT NULL,
    description VARCHAR(255) NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'ENABLED',
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    version BIGINT NOT NULL DEFAULT 0,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    created_by BIGINT NULL,
    updated_by BIGINT NULL,
    PRIMARY KEY (id),
    CONSTRAINT uq_role_code UNIQUE (code),
    CONSTRAINT ck_role_status CHECK (status IN ('ENABLED', 'DISABLED')),
    CONSTRAINT ck_role_version CHECK (version >= 0),
    CONSTRAINT ck_role_super_admin CHECK (
        code <> 'SUPER_ADMIN' OR (status = 'ENABLED' AND is_deleted = FALSE)
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
