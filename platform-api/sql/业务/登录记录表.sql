SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `sys_login_record`;
CREATE TABLE sys_login_record (
    id BIGINT NOT NULL COMMENT '独立登录记录雪花ID，不是SessionID',
    user_id BIGINT NOT NULL COMMENT '历史用户ID，不随账号删除，不设外键',
    username VARCHAR(32) NOT NULL COMMENT '登录时账号快照',
    nickname VARCHAR(64) NULL COMMENT '登录时昵称快照',
    session_version BIGINT NOT NULL COMMENT '登录时安全版本，用于判断后续失效',
    source_ip VARCHAR(45) NOT NULL COMMENT '规范化可信来源IP',
    region_type VARCHAR(32) NOT NULL COMMENT '本机内网公网等来源分类',
    region VARCHAR(256) NOT NULL COMMENT '离线地区或配置本地网段名称',
    browser VARCHAR(64) NOT NULL COMMENT '浏览器类别，来自客户端声明',
    os VARCHAR(64) NOT NULL COMMENT '操作系统类别，来自客户端声明',
    login_at DATETIME(6) NOT NULL COMMENT '成功登录时间，北京时间',
    last_activity_at DATETIME(6) NOT NULL COMMENT '节流后最后记录活动时间，北京时间',
    absolute_expires_at DATETIME(6) NOT NULL COMMENT '登录时确定的绝对到期时间，北京时间',
    idle_timeout_seconds INT NOT NULL COMMENT '登录时闲置期限秒数',
    activity_interval_seconds INT NOT NULL COMMENT '登录时活动记录节流秒数',
    ended_at DATETIME(6) NULL COMMENT '明确结束时间，北京时间；超时可查询时推断',
    end_reason VARCHAR(32) NULL COMMENT '明确结束原因；未设置时按期限和账号安全版本判断',
    PRIMARY KEY (id),
    CONSTRAINT ck_login_record_limits CHECK (idle_timeout_seconds > 0 AND activity_interval_seconds > 0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='成功登录会话历史，无会话凭据';
CREATE INDEX ix_login_record_user_time ON sys_login_record(user_id, login_at, id);
CREATE INDEX ix_login_record_user_end ON sys_login_record(user_id, end_reason);
