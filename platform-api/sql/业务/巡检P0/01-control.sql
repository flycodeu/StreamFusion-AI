-- P0 isolated contract schema. UTC DATETIME values, never apply to an existing business DB.
CREATE TABLE inspection_control (
    control_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    plan_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    node_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    target_boot_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    control_seq INT NOT NULL,
    desired_state VARCHAR(16) NOT NULL,
    expires_at DATETIME(6) NOT NULL,
    accepted_seq INT NULL,
    accepted_boot_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    accepted_expires_at DATETIME(6) NULL,
    command_digest CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    observed_state VARCHAR(24) NOT NULL DEFAULT 'DISARMED',
    available_slots INT NOT NULL DEFAULT 0,
    reason_code VARCHAR(64) NULL,
    trace_id VARCHAR(64) NULL,
    claim_after_deadline DATETIME(6) NULL,
    claim_after_due DATETIME(6) NULL,
    claim_after_job_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    PRIMARY KEY (control_id),
    UNIQUE KEY uk_inspection_control_scope (plan_id, node_id),
    CONSTRAINT ck_inspection_control_seq CHECK (control_seq > 0),
    CONSTRAINT ck_inspection_control_desired CHECK (desired_state IN ('RUNNING','STOPPED')),
    CONSTRAINT ck_inspection_control_observed CHECK (observed_state IN (
        'DISARMED','PREPARING','READY','WAITING_RESOURCE','STOPPING','STOPPED')),
    CONSTRAINT ck_inspection_control_slots CHECK (available_slots BETWEEN 0 AND 4096),
    CONSTRAINT ck_inspection_control_cursor CHECK (
        (claim_after_deadline IS NULL AND claim_after_due IS NULL AND claim_after_job_id IS NULL)
        OR (claim_after_deadline IS NOT NULL AND claim_after_due IS NOT NULL
            AND claim_after_job_id IS NOT NULL))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
