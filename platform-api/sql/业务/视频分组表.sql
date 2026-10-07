SET NAMES utf8mb4;
SET time_zone = '+08:00';
SET FOREIGN_KEY_CHECKS = 1;

DROP TABLE IF EXISTS `camera_group`;
CREATE TABLE camera_group (
 id BIGINT NOT NULL COMMENT '雪花视频组ID',
 parent_id BIGINT NULL COMMENT '上级视频组ID',
 name VARCHAR(64) CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_as_ci NOT NULL COMMENT '同级名称',
 sort_order INT NOT NULL DEFAULT 0 COMMENT '同级排序',
 remark VARCHAR(500) NULL COMMENT '备注',
 version BIGINT NOT NULL DEFAULT 0 COMMENT '编辑版本',
 created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '创建时间',
 updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) COMMENT '更新时间',
 created_by BIGINT NULL COMMENT '创建人ID快照',
 updated_by BIGINT NULL COMMENT '更新人ID快照',
 PRIMARY KEY(id),
 CONSTRAINT fk_camera_group_parent FOREIGN KEY(parent_id) REFERENCES camera_group(id),
 CONSTRAINT ck_camera_group_id CHECK(id>0),
 CONSTRAINT ck_camera_group_parent CHECK(parent_id IS NULL OR (parent_id>0 AND parent_id!=id)),
 CONSTRAINT ck_camera_group_version CHECK(version>=0),
 CONSTRAINT ck_camera_group_order CHECK(sort_order>=0)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='独立视频分组';
CREATE INDEX ix_camera_group_parent ON camera_group(parent_id,sort_order,id);
CREATE INDEX ix_camera_group_name ON camera_group(parent_id,name);
