-- Existing database only. Back up first; apply once after checking INFORMATION_SCHEMA.COLUMNS.
-- No object, instance, scope, credential or task record is removed.
ALTER TABLE storage_instance ADD COLUMN management_url VARCHAR(2048) NULL AFTER metrics_endpoint;
