CREATE TABLE sys_menu (
    id BIGINT NOT NULL AUTO_INCREMENT,
    parent_id BIGINT NULL,
    name VARCHAR(64) NOT NULL,
    type VARCHAR(16) NOT NULL,
    route_name VARCHAR(64) NULL,
    path VARCHAR(200) NULL,
    component_key VARCHAR(64) NULL,
    required_permission_id BIGINT NULL,
    icon VARCHAR(64) NULL,
    sort_order INT NOT NULL DEFAULT 0,
    visible BOOLEAN NOT NULL DEFAULT TRUE,
    enabled BOOLEAN NOT NULL DEFAULT TRUE,
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE,
    version BIGINT NOT NULL DEFAULT 0,
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    created_by BIGINT NULL,
    updated_by BIGINT NULL,
    PRIMARY KEY (id),
    CONSTRAINT uq_menu_route UNIQUE (route_name),
    CONSTRAINT uq_menu_path UNIQUE (path),
    CONSTRAINT fk_menu_parent FOREIGN KEY (parent_id) REFERENCES sys_menu(id),
    CONSTRAINT fk_menu_permission FOREIGN KEY (required_permission_id) REFERENCES sys_permission(id),
    CONSTRAINT ck_menu_version CHECK (version >= 0),
    CONSTRAINT ck_menu_shape CHECK (
        (type = 'DIRECTORY' AND route_name IS NULL AND path IS NULL
         AND component_key IS NULL AND required_permission_id IS NULL)
        OR (type = 'PAGE' AND route_name IS NOT NULL AND path IS NOT NULL
         AND component_key IS NOT NULL AND required_permission_id IS NOT NULL)
    )
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
CREATE INDEX ix_menu_parent ON sys_menu(parent_id, is_deleted, sort_order, id);
CREATE INDEX ix_menu_permission ON sys_menu(required_permission_id);
