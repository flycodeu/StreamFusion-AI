CREATE TABLE sys_permission (
    id BIGINT NOT NULL AUTO_INCREMENT,
    code VARCHAR(96) NOT NULL,
    name VARCHAR(64) NOT NULL,
    resource VARCHAR(64) NOT NULL,
    description VARCHAR(255) NULL,
    PRIMARY KEY (id),
    CONSTRAINT uq_permission_code UNIQUE (code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE INDEX ix_permission_resource ON sys_permission(resource);
