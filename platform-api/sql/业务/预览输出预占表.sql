SET NAMES utf8mb4;
DROP TABLE IF EXISTS preview_output;
CREATE TABLE preview_output (
    profile_id BIGINT NOT NULL,
    output_key VARCHAR(80) NOT NULL COMMENT 'direct或固定H264预设与执行配置摘要',
    transcode_units INT NOT NULL,
    PRIMARY KEY (profile_id,output_key),
    CONSTRAINT fk_preview_output_source FOREIGN KEY (profile_id) REFERENCES preview_source(profile_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='共享输出预算；未知启动和清理中仍占用';
