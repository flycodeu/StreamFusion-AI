-- One attempt owns its exact immutable budget snapshot until release is confirmed.
CREATE TABLE inspection_run (
    run_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    job_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    assignment_epoch INT NOT NULL,
    claim_request_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    control_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    control_seq INT NOT NULL,
    node_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    boot_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    spec_json JSON NOT NULL,
    budget_keys_json JSON NOT NULL,
    state VARCHAR(24) NOT NULL DEFAULT 'CLAIMED',
    claimed_at DATETIME(6) NOT NULL,
    started_at DATETIME(6) NULL,
    lease_expires_at DATETIME(6) NOT NULL,
    resources_released TINYINT NOT NULL DEFAULT 0,
    released_at DATETIME(6) NULL,
    trace_id VARCHAR(64) NULL,
    PRIMARY KEY (run_id),
    UNIQUE KEY uk_inspection_run_attempt (job_id, assignment_epoch),
    UNIQUE KEY uk_inspection_run_claim (node_id, boot_id, claim_request_id),
    KEY ix_inspection_run_active (node_id, resources_released, control_id),
    CONSTRAINT fk_inspection_run_job FOREIGN KEY (job_id) REFERENCES inspection_job(job_id),
    CONSTRAINT fk_inspection_run_control FOREIGN KEY (control_id)
        REFERENCES inspection_control(control_id),
    CONSTRAINT ck_inspection_run_epoch CHECK (assignment_epoch > 0 AND control_seq > 0),
    CONSTRAINT ck_inspection_run_released CHECK (resources_released IN (0,1)),
    CONSTRAINT ck_inspection_run_state CHECK (state IN (
        'CLAIMED','RUNNING','FINISHED','CANCELLED','CLEANUP_PENDING')),
    CONSTRAINT ck_inspection_run_dates CHECK (claimed_at < lease_expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
