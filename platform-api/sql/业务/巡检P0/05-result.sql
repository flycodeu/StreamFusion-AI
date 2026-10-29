-- DB committed time is the submission fact. Platform acceptance is a separate future consumer.
CREATE TABLE inspection_result (
    run_id VARCHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    detector_key VARCHAR(32) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    result_digest CHAR(64) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    payload_json JSON NOT NULL,
    submitted_at DATETIME(6) NOT NULL,
    PRIMARY KEY (run_id, detector_key),
    CONSTRAINT fk_inspection_result_run FOREIGN KEY (run_id) REFERENCES inspection_run(run_id),
    CONSTRAINT ck_inspection_result_detector CHECK (detector_key IN (
        'STREAM_STABILITY','BLACK_SCREEN','BLUE_SCREEN','FREEZE','BLUR','OCCLUSION'))
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
