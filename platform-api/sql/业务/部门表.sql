SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `sys_dept`;
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
