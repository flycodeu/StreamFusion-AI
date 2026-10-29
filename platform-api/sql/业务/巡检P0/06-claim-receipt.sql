-- Retain empty claims too. Replaying a request never allocates a different job.
CREATE TABLE inspection_claim_receipt (
    node_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    boot_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    request_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    control_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    control_seq INT NOT NULL,
    run_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NULL,
    created_at DATETIME(6) NOT NULL,
    PRIMARY KEY (node_id, boot_id, request_id),
    CONSTRAINT fk_inspection_receipt_run FOREIGN KEY (run_id) REFERENCES inspection_run(run_id),
    CONSTRAINT fk_inspection_receipt_control FOREIGN KEY (control_id)
        REFERENCES inspection_control(control_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
