SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `sys_dept`;
CREATE TABLE sys_dept (
    id BIGINT NOT NULL AUTO_INCREMENT COMMENT '主键ID，自增',
    parent_id BIGINT NULL COMMENT '父部门ID，空表示根节点',
    name VARCHAR(64) NOT NULL COMMENT '部门或组织节点名称',
    sort_order INT NOT NULL DEFAULT 0 COMMENT '同级展示顺序，数值越小越靠前',
    is_deleted BOOLEAN NOT NULL DEFAULT FALSE COMMENT '逻辑删除标记：0未删除，1已删除',
    version BIGINT NOT NULL DEFAULT 0 COMMENT '编辑版本号，用于乐观锁和并发修改校验',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间，UTC+8（北京时间）',
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '最后更新时间，UTC+8（北京时间），由业务写入维护',
    created_by BIGINT NULL COMMENT '创建人用户ID，系统初始化时可为空',
    updated_by BIGINT NULL COMMENT '最后修改人用户ID，系统初始化时可为空',
    PRIMARY KEY (id),
    CONSTRAINT fk_dept_parent FOREIGN KEY (parent_id) REFERENCES sys_dept(id),
    CONSTRAINT ck_dept_version CHECK (version >= 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='部门组织表，公司、部门、组统一为树节点，不参与授权';
CREATE INDEX ix_dept_parent ON sys_dept(parent_id, is_deleted, sort_order, id);
