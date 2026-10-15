SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `camera_status_plan_target`;
CREATE TABLE camera_status_plan_target (
    id BIGINT NOT NULL,
    plan_id BIGINT NOT NULL,
    camera_id BIGINT NOT NULL,
    profile_id BIGINT NOT NULL,
    connection_key_id VARCHAR(64) NOT NULL,
    connection_digest BINARY(32) NOT NULL,
    next_at DATETIME(3) NOT NULL,
    reason_code VARCHAR(64) NULL,
    blocked TINYINT(1) NOT NULL DEFAULT 0,
    last_check_id BIGINT NULL,
    PRIMARY KEY (id),
    UNIQUE KEY uk_camera_plan_target (plan_id,camera_id),
    KEY ix_camera_plan_due (blocked,next_at,id),
    CONSTRAINT fk_camera_plan_target FOREIGN KEY (plan_id) REFERENCES camera_status_plan(id),
    CONSTRAINT ck_camera_plan_target_blocked CHECK (blocked IN (0,1))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='固定通道代表流和错峰时间槽，不保存URL或凭据';
