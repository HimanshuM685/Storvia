-- Tenant acceptance of the outage-fallback disclosure.
--
-- A file may only be routed to the fallback provider when the tenant has accepted
-- that its contents may be stored outside the user's own Drive. This table is the
-- record of that acceptance, and it is the only thing that opens the gate.
--
-- It is deliberately per tenant rather than a deployment-wide switch: consent to
-- have company-managed storage hold your files is not transferable between tenants,
-- and a deployment must not be able to imply consent on someone's behalf.

CREATE TABLE IF NOT EXISTS tenant_fallback_consent (
    id           TEXT PRIMARY KEY,
    tenant_id    TEXT NOT NULL REFERENCES tenants (id) ON DELETE CASCADE,
    accepted     BOOLEAN NOT NULL,
    -- The disclosure text version the tenant agreed to. If the wording changes,
    -- acceptance stops matching and the tenant is asked again, rather than a
    -- changed disclosure being accepted under an old agreement.
    disclosure_version TEXT NOT NULL,
    accepted_at  TIMESTAMPTZ,
    withdrawn_at TIMESTAMPTZ,
    created_at   TIMESTAMPTZ NOT NULL,
    updated_at   TIMESTAMPTZ NOT NULL,
    UNIQUE (tenant_id)
);

-- A tenant that has withdrawn must not read as accepted, so the two timestamps
-- cannot both be set. An acceptance is either present or explicitly withdrawn.
CREATE INDEX IF NOT EXISTS idx_tenant_fallback_consent_accepted
    ON tenant_fallback_consent (tenant_id, accepted);