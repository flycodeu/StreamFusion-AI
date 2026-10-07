SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `camera_access_import_item`;
CREATE TABLE camera_access_import_item (
    id BIGINT NOT NULL COMMENT '雪花ID',
    job_id BIGINT NOT NULL COMMENT '所属导入任务',
    external_key VARCHAR(512) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_bin NOT NULL COMMENT '本任务上游唯一身份',
    page_number INT NOT NULL COMMENT '上游页号',
    item_index INT NOT NULL COMMENT '页内序号，0开始',
    status VARCHAR(16) NOT NULL COMMENT 'CREATED/EXISTING/FAILED',
    camera_id BIGINT NULL COMMENT '保存后的相机ID，历史引用',
    name VARCHAR(128) NULL COMMENT '点位名称快照',
    reason_code VARCHAR(64) NULL COMMENT '固定安全错误码',
    created_at DATETIME(6) NOT NULL COMMENT '处理时间',
    PRIMARY KEY (id),
    UNIQUE KEY uk_access_import_identity (job_id,external_key),
    KEY idx_access_import_page (job_id,status,id),
    CONSTRAINT fk_access_import_job FOREIGN KEY (job_id) REFERENCES camera_access_job(id) ON DELETE CASCADE,
    CONSTRAINT ck_access_import_item CHECK (page_number BETWEEN 1 AND 100 AND item_index BETWEEN 0 AND 99 AND status IN ('CREATED','EXISTING','FAILED') AND ((status = 'FAILED' AND camera_id IS NULL) OR (status <> 'FAILED' AND camera_id IS NOT NULL)))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='整批导入逐项结果及分页去重';
