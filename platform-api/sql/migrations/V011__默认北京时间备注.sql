-- Metadata only: existing UTC DATETIME data must be converted offline before cutover.
-- Fresh databases already use +08:00; never add eight hours unconditionally here.

/*!80016 ALTER TABLE sys_dept
    MODIFY COLUMN created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间，UTC+8（北京时间）',
    MODIFY COLUMN updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '最后更新时间，UTC+8（北京时间），由业务写入维护'
*/;

/*!80016 ALTER TABLE sys_user
    MODIFY COLUMN locked_until DATETIME(6) NULL COMMENT '登录冷却截止时间，UTC+8（北京时间），空表示无冷却',
    MODIFY COLUMN created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间，UTC+8（北京时间）',
    MODIFY COLUMN updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '最后更新时间，UTC+8（北京时间），由业务写入维护'
*/;

/*!80016 ALTER TABLE sys_role
    MODIFY COLUMN created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间，UTC+8（北京时间）',
    MODIFY COLUMN updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '最后更新时间，UTC+8（北京时间），由业务写入维护'
*/;

/*!80016 ALTER TABLE sys_menu
    MODIFY COLUMN created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间，UTC+8（北京时间）',
    MODIFY COLUMN updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '最后更新时间，UTC+8（北京时间），由业务写入维护'
*/;

/*!80016 ALTER TABLE sys_user_role
    MODIFY COLUMN created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '角色授予时间，UTC+8（北京时间）'
*/;

/*!80016 ALTER TABLE sys_role_permission
    MODIFY COLUMN created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '权限授予时间，UTC+8（北京时间）'
*/;

/*!80016 ALTER TABLE sys_operation_log
    MODIFY COLUMN created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '审计记录时间，UTC+8（北京时间）'
*/;
