-- Reconciliation state is operator-visible workflow metadata. It does not add a
-- second provider location or retain any file content.
ALTER TABLE storage_objects ADD COLUMN reconciliation_state TEXT NOT NULL DEFAULT 'none'
    CHECK (reconciliation_state IN ('none','pending','running','completed','unknown','missing','failed'));
ALTER TABLE storage_objects ADD COLUMN pinned_provider TEXT
    CHECK (pinned_provider IS NULL OR pinned_provider IN ('google_drive','s3_compatible'));
ALTER TABLE storage_objects ADD COLUMN last_reconcile_at TIMESTAMPTZ;
ALTER TABLE storage_objects ADD COLUMN reconcile_attempts INTEGER NOT NULL DEFAULT 0
    CHECK (reconcile_attempts >= 0);
ALTER TABLE storage_objects ADD COLUMN last_error_class TEXT;

CREATE INDEX IF NOT EXISTS idx_storage_objects_reconciliation
    ON storage_objects (tenant_id, user_id, reconciliation_state, last_reconcile_at);
