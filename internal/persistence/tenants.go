package persistence

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// TenantRepository manages Storvia's own identity boundary.
//
// A tenant is created by the application, never by a provider callback.
type TenantRepository struct {
	store *Store
}

// NewTenantRepository builds the repository.
func NewTenantRepository(store *Store) *TenantRepository {
	return &TenantRepository{store: store}
}

// EnsureTenant creates a tenant row if it does not already exist.
//
// Every scoped row hangs off the tenant: consent, Drive connections, objects, and
// uploads all reference it. Without the tenant every one of those writes fails on a
// foreign key that nothing else would have satisfied.
//
// The development admin panel is the case that actually bites, because it names a
// fixed tenant ID in configuration and nothing else ever inserts that row — so the
// panel appears to work right up until the first thing that writes.
func (r *TenantRepository) EnsureTenant(ctx context.Context, tenantID, name string) error {
	if strings.TrimSpace(tenantID) == "" {
		return errors.New("persistence: a tenant id is required")
	}
	if strings.TrimSpace(name) == "" {
		name = tenantID
	}
	now := time.Now().UTC()

	_, err := r.store.db.ExecContext(ctx, r.store.Rebind(`
		INSERT INTO tenants (id, name, created_at, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT (id) DO UPDATE SET name = excluded.name, updated_at = excluded.updated_at`),
		tenantID, name, now, now)
	if err != nil {
		return fmt.Errorf("persistence: ensure tenant: %w", err)
	}
	return nil
}
