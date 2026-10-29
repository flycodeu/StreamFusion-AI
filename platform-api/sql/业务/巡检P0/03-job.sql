-- The platform fixture publishes immutable JobSpec JSON. No camera credentials.
CREATE TABLE inspection_job (
    job_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    plan_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    due_at DATETIME(6) NOT NULL,
    latest_start_at DATETIME(6) NOT NULL,
    deadline_at DATETIME(6) NOT NULL,
    spec_json JSON NOT NULL,
    spec_digest CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    state VARCHAR(24) NOT NULL DEFAULT 'QUEUED',
    assignment_epoch INT NOT NULL DEFAULT 0,
    PRIMARY KEY (job_id),
    KEY ix_inspection_job_due (plan_id, state, deadline_at, due_at, job_id),
    CONSTRAINT ck_inspection_job_dates CHECK (
        due_at <= latest_start_at AND latest_start_at < deadline_at),
    CONSTRAINT ck_inspection_job_epoch CHECK (assignment_epoch >= 0),
    CONSTRAINT ck_inspection_job_state CHECK (state IN (
        'QUEUED','CLAIMED','RUNNING','FINISHED','CANCELLED','CLEANUP_PENDING'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
