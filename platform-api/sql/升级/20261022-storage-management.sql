-- P2-STORAGE-IMPL-01. For the verified pre-storage schema only. Back up first.
-- Stop the platform before applying; MySQL DDL implicitly commits. Never run the rebuild script.
-- If any storage_* table or menu id 1013 already exists, STOP and reconcile before proceeding.
SET NAMES utf8mb4;
SET time_zone = '+08:00';

CREATE TABLE storage_instance (
    `id` BIGINT NOT NULL,
    `name` VARCHAR(64) NOT NULL,
    `endpoint` VARCHAR(2048) NOT NULL,
    `region` VARCHAR(64) NOT NULL,
    `network_policy_key` VARCHAR(64) NOT NULL,
    `metrics_endpoint` VARCHAR(2048) NULL,
    `remark` VARCHAR(500) NULL,
    `encryption_key_id` VARCHAR(64) NOT NULL,
    `secret_nonce` VARBINARY(12) NOT NULL,
    `secret_ciphertext` VARBINARY(8192) NOT NULL,
    `secret_tag` VARBINARY(16) NOT NULL,
    `write_enabled` TINYINT(1) NOT NULL DEFAULT 0,
    `observation` MEDIUMTEXT NULL,
    `usage_snapshot` MEDIUMTEXT NULL,
    `metrics_snapshot` MEDIUMTEXT NULL,
    `version` BIGINT NOT NULL DEFAULT 0,
    `created_by` BIGINT NOT NULL,
    `created_at` DATETIME(3) NOT NULL,
    `updated_at` DATETIME(3) NOT NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY uk_storage_instance_name (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='图床实例表';

CREATE TABLE storage_scope (
    `id` BIGINT NOT NULL,
    `instance_id` BIGINT NOT NULL,
    `bucket` VARCHAR(63) NOT NULL,
    `prefix` VARCHAR(1024) NOT NULL,
    `display_name` VARCHAR(64) NULL,
    `probe_prefix` VARCHAR(1024) NOT NULL,
    `identity_digest` BINARY(32) NOT NULL,
    `management_mode` VARCHAR(24) NOT NULL,
    `observation` MEDIUMTEXT NULL,
    `version` BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (`id`),
    UNIQUE KEY uk_storage_scope_identity (instance_id,identity_digest),
    CONSTRAINT fk_storage_scope_instance FOREIGN KEY (instance_id) REFERENCES storage_instance(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='图床范围表';

CREATE TABLE storage_default (
    `id` BIGINT NOT NULL,
    `scope_id` BIGINT NULL,
    `version` BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (`id`),
    CONSTRAINT fk_storage_default_scope FOREIGN KEY (scope_id) REFERENCES storage_scope(id),
    CONSTRAINT ck_storage_default_id CHECK (id=1)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='图床默认位置表';
INSERT INTO storage_default (id,scope_id,version) VALUES (1,NULL,0);

CREATE TABLE storage_task (
    `id` BIGINT NOT NULL,
    `run_id` BIGINT NOT NULL,
    `instance_id` BIGINT NOT NULL,
    `scope_id` BIGINT NULL,
    `actor_id` BIGINT NOT NULL,
    `type` VARCHAR(32) NOT NULL,
    `status` VARCHAR(32) NOT NULL,
    `phase` VARCHAR(48) NOT NULL,
    `request_key` VARCHAR(36) NOT NULL,
    `request_fingerprint` VARCHAR(160) NOT NULL,
    `input_json` MEDIUMTEXT NULL,
    `result_json` MEDIUMTEXT NULL,
    `cursor_json` MEDIUMTEXT NULL,
    `trace_id` VARCHAR(64) NULL,
    `owner` VARCHAR(64) NULL,
    `lease_until` DATETIME(3) NULL,
    `cancel_requested` TINYINT(1) NOT NULL DEFAULT 0,
    `version` BIGINT NOT NULL DEFAULT 0,
    `created_at` DATETIME(3) NOT NULL,
    `started_at` DATETIME(3) NULL,
    `finished_at` DATETIME(3) NULL,
    PRIMARY KEY (`id`),
    UNIQUE KEY uk_storage_task_request (actor_id,type,request_key),
    KEY ix_storage_task_queue (status,created_at,id),
    KEY ix_storage_task_instance (instance_id,status,id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='图床任务表';

CREATE TABLE storage_cleanup_plan (
    `id` BIGINT NOT NULL,
    `instance_id` BIGINT NOT NULL,
    `scope_id` BIGINT NOT NULL,
    `actor_id` BIGINT NOT NULL,
    `rule_id` BIGINT NULL,
    `rule_version` BIGINT NULL,
    `instance_version` BIGINT NOT NULL,
    `scope_version` BIGINT NOT NULL,
    `mode` VARCHAR(24) NOT NULL,
    `status` VARCHAR(32) NOT NULL,
    `input_json` MEDIUMTEXT NULL,
    `candidate_count` BIGINT NOT NULL DEFAULT 0,
    `candidate_bytes` BIGINT NOT NULL DEFAULT 0,
    `limit_reached` TINYINT(1) NOT NULL DEFAULT 0,
    `execution_task_id` BIGINT NULL,
    `version` BIGINT NOT NULL DEFAULT 0,
    `created_at` DATETIME(3) NOT NULL,
    `expires_at` DATETIME(3) NOT NULL,
    PRIMARY KEY (`id`),
    KEY ix_storage_plan_scope (scope_id,status,id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='图床清理计划表';

CREATE TABLE storage_cleanup_item (
    `id` BIGINT NOT NULL,
    `plan_id` BIGINT NOT NULL,
    `object_key` VARCHAR(1024) NOT NULL,
    `etag` VARCHAR(256) NOT NULL,
    `size_bytes` BIGINT NOT NULL,
    `last_modified` DATETIME(3) NULL,
    `status` VARCHAR(32) NOT NULL,
    `reason_code` VARCHAR(80) NULL,
    PRIMARY KEY (`id`),
    KEY ix_storage_item_plan (plan_id,id),
    CONSTRAINT fk_storage_item_plan FOREIGN KEY (plan_id) REFERENCES storage_cleanup_plan(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='图床清理候选表';

CREATE TABLE storage_cleanup_rule (
    `id` BIGINT NOT NULL,
    `scope_id` BIGINT NOT NULL,
    `name` VARCHAR(64) NOT NULL,
    `relative_prefix` VARCHAR(1024) NOT NULL,
    `retention_days` INT NOT NULL,
    `schedule_time` VARCHAR(5) NOT NULL,
    `max_delete_objects` INT NOT NULL,
    `max_delete_bytes` BIGINT NOT NULL,
    `enabled` TINYINT(1) NOT NULL DEFAULT 0,
    `actor_id` BIGINT NOT NULL,
    `version` BIGINT NOT NULL DEFAULT 0,
    `next_at` DATETIME(3) NULL,
    `last_schedule_date` VARCHAR(10) NULL,
    PRIMARY KEY (`id`),
    KEY ix_storage_rule_due (enabled,next_at,id),
    CONSTRAINT fk_storage_rule_scope FOREIGN KEY (scope_id) REFERENCES storage_scope(id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COMMENT='图床清理规则表';

INSERT INTO sys_menu (id,parent_id,name,type,route_name,path,component_key,module_key,icon,sort_order,visible,enabled)
VALUES (1013,1008,'图床管理','PAGE','StorageManage','/storage/manage','/storage/Manage','storage','connection',5,TRUE,TRUE);

INSERT INTO sys_role_menu (role_id,menu_id) SELECT 1,1013 WHERE EXISTS (SELECT 1 FROM sys_role WHERE id=1 AND code='SUPER_ADMIN') AND NOT EXISTS (SELECT 1 FROM sys_role_menu WHERE role_id=1 AND menu_id=1013);
