package persistence

import (
	"context"
	"errors"
	"testing"
	"time"
)

// seedConsentTenant inserts the tenant row the consent table references.
func seedConsentTenant(t *testing.T, store *Store, tenantID string) {
	t.Helper()
	now := time.Now().UTC()
	if _, err := store.DB().Exec(
		`INSERT INTO tenants (id, name, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		tenantID, tenantID, now, now); err != nil {
		t.Fatalf("seed tenant: %v", err)
	}
}

// TestFallbackConsentIsNotGrantedByDefault is the safety property: a tenant that has
// never been asked must not have the outage fallback open.
//
// This is the default every deployment inherits, so it is the one that matters most.
// A tenant whose consent record is missing must read as not granted, not as an error
// and certainly not as consent by omission.
func TestFallbackConsentIsNotGrantedByDefault(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seedConsentTenant(t, store, "tenant_a")

	repo := NewFallbackConsentRepository(store)

	granted, err := repo.Granted(ctx, "tenant_a")
	if err != nil {
		t.Fatalf("Granted on a tenant with no record returned an error: %v", err)
	}
	if granted {
		t.Fatal("a tenant that was never asked is treated as having consented")
	}

	// The distinction between "never asked" and "said no" must survive, because the
	// caller has to be able to tell a new tenant from a refusing one.
	_, err = repo.Resolve(ctx, "tenant_a")
	if !errors.Is(err, ErrNoConsent) {
		t.Errorf("Resolve error = %v, want ErrNoConsent", err)
	}
}

// TestFallbackConsentLifecycle walks the full accept/withdraw/re-accept cycle.
func TestFallbackConsentLifecycle(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seedConsentTenant(t, store, "tenant_a")
	repo := NewFallbackConsentRepository(store)

	if _, err := repo.Accept(ctx, "tenant_a"); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	assertGranted(t, repo, ctx, "tenant_a", true)

	// Accepting twice must not fail; a client retrying a request should not be told
	// it did something wrong.
	if _, err := repo.Accept(ctx, "tenant_a"); err != nil {
		t.Fatalf("re-accepting must be idempotent: %v", err)
	}
	assertGranted(t, repo, ctx, "tenant_a", true)

	if err := repo.Withdraw(ctx, "tenant_a"); err != nil {
		t.Fatalf("Withdraw: %v", err)
	}
	assertGranted(t, repo, ctx, "tenant_a", false)

	// Changing your mind and then changing it back must not be blocked by history.
	if _, err := repo.Accept(ctx, "tenant_a"); err != nil {
		t.Fatalf("re-accept after withdrawal: %v", err)
	}
	assertGranted(t, repo, ctx, "tenant_a", true)

	consent, err := repo.Resolve(ctx, "tenant_a")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !consent.Accepted {
		t.Error("the record should read as accepted after re-accepting")
	}
	if !consent.WithdrawnAt.IsZero() {
		t.Error("re-accepting must clear the withdrawal timestamp, or the gate stays shut")
	}
}

// TestFallbackConsentRequiresTheCurrentDisclosure guards the version check. A tenant
// must not be held to terms it never saw, so an acceptance of an older disclosure
// stops counting the moment the wording changes.
func TestFallbackConsentRequiresTheCurrentDisclosure(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seedConsentTenant(t, store, "tenant_a")
	repo := NewFallbackConsentRepository(store)

	if _, err := repo.Accept(ctx, "tenant_a"); err != nil {
		t.Fatalf("Accept: %v", err)
	}
	assertGranted(t, repo, ctx, "tenant_a", true)

	if _, err := store.DB().Exec(
		`UPDATE tenant_fallback_consent SET disclosure_version = 'an-older-version'`); err != nil {
		t.Fatalf("age the disclosure: %v", err)
	}

	assertGranted(t, repo, ctx, "tenant_a", false)

	consent, err := repo.Resolve(ctx, "tenant_a")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !consent.Accepted {
		t.Error("the stored acceptance flag should still be true; only the gate is shut")
	}
}

// TestFallbackConsentIsPerTenant checks that one tenant's acceptance cannot open
// another tenant's gate. Consent to have files held in company storage is not
// transferable.
func TestFallbackConsentIsPerTenant(t *testing.T) {
	store := newTestStore(t)
	ctx := context.Background()
	seedConsentTenant(t, store, "tenant_a")
	seedConsentTenant(t, store, "tenant_b")
	repo := NewFallbackConsentRepository(store)

	if _, err := repo.Accept(ctx, "tenant_a"); err != nil {
		t.Fatalf("Accept: %v", err)
	}

	assertGranted(t, repo, ctx, "tenant_a", true)
	assertGranted(t, repo, ctx, "tenant_b", false)
}

func assertGranted(t *testing.T, repo *FallbackConsentRepository, ctx context.Context, tenantID string, want bool) {
	t.Helper()
	got, err := repo.Granted(ctx, tenantID)
	if err != nil {
		t.Fatalf("Granted(%s): %v", tenantID, err)
	}
	if got != want {
		t.Errorf("Granted(%s) = %v, want %v", tenantID, got, want)
	}
}
