-- Storvia initial schema (SQLite dialect).
--
-- Metadata and workflow state only. There is no replica table and no file-body
-- column: one logical object has exactly one provider pointer, so a second
-- storage location is not representable in this schema.

CREATE TABLE IF NOT EXISTS drive_connections (
    id                     TEXT PRIMARY KEY,
    tenant_id              TEXT NOT NULL,
    user_id                TEXT NOT NULL,
    google_subject         TEXT NOT NULL,
    encrypted_refresh_token BLOB NOT NULL,
    key_reference          TEXT NOT NULL,
    granted_scopes         TEXT NOT NULL,
    root_folder_id         TEXT,
    changes_cursor         TEXT,
    status                 TEXT NOT NULL CHECK (status IN ('connected','reauth_required','disconnected','blocked')),
    last_success_at        TIMESTAMP,
    created_at             TIMESTAMP NOT NULL,
    updated_at             TIMESTAMP NOT NULL,
    UNIQUE (tenant_id, user_id, google_subject)
);

CREATE INDEX IF NOT EXISTS idx_drive_connections_tenant_user
    ON drive_connections (tenant_id, user_id);

-- The tenant is Storvia's own identity boundary. It is created by the
-- application, never by a provider callback, and every scoped row below hangs
-- off it so that a missing tenant cannot be papered over with a bare string.
CREATE TABLE IF NOT EXISTS tenants (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

-- The fallback destination is operator configuration, never client supplied.
CREATE TABLE IF NOT EXISTS storage_backends (
    id                  TEXT PRIMARY KEY,
    scope               TEXT NOT NULL CHECK (scope IN ('deployment','tenant')),
    tenant_id           TEXT REFERENCES tenants (id) ON DELETE CASCADE,
    provider            TEXT NOT NULL CHECK (provider = 's3_compatible'),
    vendor              TEXT NOT NULL CHECK (vendor IN ('aws_s3','cloudflare_r2','other')),
    endpoint            TEXT NOT NULL,
    region              TEXT NOT NULL,
    bucket              TEXT NOT NULL,
    prefix              TEXT NOT NULL DEFAULT '',
    credential_reference TEXT NOT NULL,
    status              TEXT NOT NULL CHECK (status IN ('active','disabled')),
    capabilities        TEXT NOT NULL DEFAULT '{}',
    created_at          TIMESTAMP NOT NULL,
    updated_at          TIMESTAMP NOT NULL,
    -- A tenant-scoped backend must name its tenant; a deployment-scoped one must not.
    CHECK ((scope = 'tenant' AND tenant_id IS NOT NULL) OR (scope = 'deployment' AND tenant_id IS NULL))
);

CREATE TABLE IF NOT EXISTS storage_objects (
    id                TEXT PRIMARY KEY,
    tenant_id         TEXT NOT NULL,
    user_id           TEXT NOT NULL,
    logical_key       TEXT NOT NULL,
    original_name     TEXT NOT NULL,
    mime_type         TEXT NOT NULL,
    category          TEXT NOT NULL DEFAULT '',
    expected_size     INTEGER NOT NULL,
    verified_size     INTEGER NOT NULL DEFAULT 0,
    checksum_algorithm TEXT,
    verified_checksum  TEXT,
    provider          TEXT CHECK (provider IS NULL OR provider IN ('google_drive','s3_compatible')),
    drive_connection_id TEXT REFERENCES drive_connections (id),
    backend_id        TEXT REFERENCES storage_backends (id),
    provider_file_id  TEXT,
    provider_object_key TEXT,
    status            TEXT NOT NULL CHECK (status IN (
                          'pending','uploading','outcome_unknown','ready','blocked',
                          'failed','deleting','deleted','missing')),
    fallback_reason   TEXT,
    selected_at       TIMESTAMP,
    verified_at       TIMESTAMP,
    row_version       INTEGER NOT NULL DEFAULT 1,
    created_at        TIMESTAMP NOT NULL,
    updated_at        TIMESTAMP NOT NULL,
    deleted_at        TIMESTAMP,

    -- The single-location invariant, enforced by the database rather than only
    -- by application code.
    -- A ready object carries exactly one complete provider location.
    CHECK (status <> 'ready' OR (
        (provider = 'google_drive' AND drive_connection_id IS NOT NULL
             AND provider_file_id IS NOT NULL AND backend_id IS NULL
             AND provider_object_key IS NULL)
        OR
        (provider = 's3_compatible' AND backend_id IS NOT NULL
             AND provider_object_key IS NOT NULL AND drive_connection_id IS NULL
             AND provider_file_id IS NULL)
    )),

    -- A verified location must have a verified size equal to what was declared.
    CHECK (status <> 'ready' OR verified_size = expected_size),

    -- A Drive location and an S3 location are mutually exclusive at all times,
    -- not only when ready.
    CHECK (NOT (provider_file_id IS NOT NULL AND provider_object_key IS NOT NULL))
);

-- One logical object per (tenant, user, logical key): a retried request reuses
-- the row instead of creating a second file.
CREATE UNIQUE INDEX IF NOT EXISTS uq_storage_objects_logical
    ON storage_objects (tenant_id, user_id, logical_key);

-- A provider identity may be claimed by only one object, so a replayed provider
-- response cannot attach two logical objects to one file.
CREATE UNIQUE INDEX IF NOT EXISTS uq_storage_objects_drive_file
    ON storage_objects (drive_connection_id, provider_file_id)
    WHERE drive_connection_id IS NOT NULL AND provider_file_id IS NOT NULL;

CREATE UNIQUE INDEX IF NOT EXISTS uq_storage_objects_backend_key
    ON storage_objects (backend_id, provider_object_key)
    WHERE backend_id IS NOT NULL AND provider_object_key IS NOT NULL;

-- Every tenant-scoped lookup in the repositories is served by this index, which
-- is what keeps cross-tenant access impossible to express by accident.
CREATE INDEX IF NOT EXISTS idx_storage_objects_tenant_user
    ON storage_objects (tenant_id, user_id, status);

CREATE TABLE IF NOT EXISTS storage_uploads (
    id                 TEXT PRIMARY KEY,
    object_id          TEXT NOT NULL REFERENCES storage_objects (id),
    tenant_id          TEXT NOT NULL,
    user_id            TEXT NOT NULL,
    idempotency_key    TEXT NOT NULL,
    request_fingerprint TEXT NOT NULL,
    active_attempt_id  TEXT,
    state              TEXT NOT NULL CHECK (state IN (
                         'pending','uploading','outcome_unknown','ready','blocked',
                         'failed','deleting','deleted','missing')),
    row_version        INTEGER NOT NULL DEFAULT 1,
    created_at         TIMESTAMP NOT NULL,
    updated_at         TIMESTAMP NOT NULL
);

-- The idempotency guarantee: the same key cannot produce two uploads.
CREATE UNIQUE INDEX IF NOT EXISTS uq_storage_uploads_idempotency
    ON storage_uploads (tenant_id, user_id, idempotency_key);

CREATE TABLE IF NOT EXISTS upload_attempts (
    id                          TEXT PRIMARY KEY,
    upload_id                   TEXT NOT NULL REFERENCES storage_uploads (id),
    provider                    TEXT NOT NULL CHECK (provider IN ('google_drive','s3_compatible')),
    drive_connection_id         TEXT REFERENCES drive_connections (id),
    backend_id                  TEXT REFERENCES storage_backends (id),
    planned_provider_identity   TEXT,
    encrypted_session_uri       BLOB,
    multipart_upload_id         TEXT,
    state                       TEXT NOT NULL CHECK (state IN (
                                  'selected','initiating','active','verifying','outcome_unknown',
                                  'completed','cleanup_pending','aborted','failed')),
    -- Never reset. Once a request may have reached the provider, this attempt is
    -- pinned and cross-provider failover is forbidden for it.
    mutation_may_have_started   INTEGER NOT NULL DEFAULT 0,
    confirmed_offset            INTEGER NOT NULL DEFAULT 0,
    expected_size               INTEGER NOT NULL DEFAULT 0,
    parts_metadata              TEXT,
    last_sanitized_error        TEXT,
    operation_generation        INTEGER NOT NULL DEFAULT 1,
    lease_owner                 TEXT,
    lease_expires_at            TIMESTAMP,
    created_at                  TIMESTAMP NOT NULL,
    updated_at                  TIMESTAMP NOT NULL,
    CHECK (NOT (drive_connection_id IS NOT NULL AND backend_id IS NOT NULL))
);

CREATE INDEX IF NOT EXISTS idx_upload_attempts_upload
    ON upload_attempts (upload_id, state);

CREATE INDEX IF NOT EXISTS idx_upload_attempts_lease
    ON upload_attempts (state, lease_expires_at);

CREATE TABLE IF NOT EXISTS storage_jobs (
    id            TEXT PRIMARY KEY,
    object_id     TEXT REFERENCES storage_objects (id),
    attempt_id    TEXT REFERENCES upload_attempts (id),
    tenant_id     TEXT NOT NULL,
    kind          TEXT NOT NULL CHECK (kind IN ('reconcile','delete','cleanup')),
    state         TEXT NOT NULL CHECK (state IN ('pending','leased','running','done','failed')),
    run_after     TIMESTAMP NOT NULL,
    lease_owner   TEXT,
    lease_expires_at TIMESTAMP,
    attempts      INTEGER NOT NULL DEFAULT 0,
    last_error    TEXT,
    created_at    TIMESTAMP NOT NULL,
    updated_at    TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_storage_jobs_claimable
    ON storage_jobs (state, run_after);

CREATE UNIQUE INDEX IF NOT EXISTS uq_storage_jobs_attempt_kind
    ON storage_jobs (attempt_id, kind)
    WHERE attempt_id IS NOT NULL;