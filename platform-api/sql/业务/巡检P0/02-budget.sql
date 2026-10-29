-- Fixed scope keys: global, node:<nodeId>, plus the immutable job budget keys.
CREATE TABLE inspection_budget (
    budget_key VARCHAR(128) CHARACTER SET ascii COLLATE ascii_bin NOT NULL,
    capacity INT NOT NULL,
    used_slots INT NOT NULL DEFAULT 0,
    PRIMARY KEY (budget_key),
    CONSTRAINT ck_inspection_budget_bounds CHECK (
        capacity BETWEEN 0 AND 4096 AND used_slots BETWEEN 0 AND capacity)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
