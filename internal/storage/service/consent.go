package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/shovan04/storvia/internal/apperr"
	"github.com/shovan04/storvia/internal/config"
	"github.com/shovan04/storvia/internal/persistence"
)

// ConsentStore records and reads a tenant's acceptance of the outage-fallback
// disclosure.
//
// It is a port rather than the concrete repository so the service can be tested
// without a database, and so this rule cannot be bypassed by wiring the repository
// in directly somewhere else.
type ConsentStore interface {
	// Granted reports whether this tenant may currently have files routed to the
	// fallback provider.
	Granted(ctx context.Context, tenantID string) (bool, error)
	// Accept records acceptance of the current disclosure.
	Accept(ctx context.Context, tenantID string) (persistence.FallbackConsent, error)
	// Withdraw records a refusal.
	Withdraw(ctx context.Context, tenantID string) error
	// Resolve returns the full record, or persistence.ErrNoConsent.
	Resolve(ctx context.Context, tenantID string) (persistence.FallbackConsent, error)
}

// ConsentService exposes the tenant's fallback-consent state and lets them change
// it.
//
// Consent is a decision a tenant makes, so the only thing this service does is make
// that decision readable and writable. It deliberately does not decide anything on
// the tenant's behalf: there is no "accept all tenants" path here, because the
// fallback gate is precisely a promise that Storvia will not move someone's file
// into storage they did not agree to.
type ConsentService struct {
	store      ConsentStore
	disclosure FallbackDisclosure
}

// FallbackDisclosure is the user-facing explanation of what accepting outage
// fallback means. It is returned with the consent state so a client cannot
// accidentally ask a tenant to accept a version without also showing the copy
// for that version.
type FallbackDisclosure struct {
	Version        string `json:"version"`
	Destination    string `json:"destination"`
	Region         string `json:"region"`
	OperatorAccess string `json:"operator_access"`
	Encryption     string `json:"encryption"`
	Retention      string `json:"retention"`
	Deletion       string `json:"deletion"`
	Disconnect     string `json:"disconnect"`
}

// NewConsentService builds the service. The optional disclosure keeps existing
// embedders source-compatible while allowing the application wiring to include
// the actual configured fallback destination and region.
func NewConsentService(store ConsentStore, disclosures ...FallbackDisclosure) *ConsentService {
	disclosure := DefaultFallbackDisclosure()
	if len(disclosures) > 0 {
		disclosure = disclosures[0]
	}
	return &ConsentService{store: store, disclosure: disclosure}
}

// DefaultFallbackDisclosure is used by isolated service tests and embedders that
// do not have deployment configuration available. The application should pass
// DisclosureForFallback so the destination and region are concrete.
func DefaultFallbackDisclosure() FallbackDisclosure {
	return FallbackDisclosure{
		Version:        persistence.CurrentDisclosureVersion,
		Destination:    "The deployment's configured S3-compatible fallback provider",
		Region:         "Configured by the deployment",
		OperatorAccess: "The Storvia deployment operator controls access to the fallback provider and may be able to access stored objects according to its permissions.",
		Encryption:     "Transport to the fallback provider uses HTTPS. Provider-side encryption and key management are controlled by the deployment and provider configuration.",
		Retention:      "Storvia does not keep a second copy or promise independent backup retention. Retention follows the deployment's provider and lifecycle policy.",
		Deletion:       "Storvia deletes an object from its recorded provider when the provider confirms the deletion. Provider versioning or retention settings may affect final erasure.",
		Disconnect:     "Disconnecting Google Drive stops new Drive operations. Existing fallback objects remain on the fallback provider until deleted under the published policy.",
	}
}

// DisclosureForFallback creates the disclosure shown for the configured fallback
// destination. The bucket name is ordinary deployment configuration, not a secret,
// and naming it prevents a tenant from accepting an unexplained destination.
func DisclosureForFallback(fallback config.FallbackConfig) FallbackDisclosure {
	disclosure := DefaultFallbackDisclosure()
	active := fallback.ActiveBackends()
	if len(active) == 0 {
		return disclosure
	}
	backend := active[0]
	vendor := backend.Vendor
	switch vendor {
	case config.VendorCloudflareR2:
		vendor = "Cloudflare R2"
	case config.VendorAWSS3:
		vendor = "Amazon S3"
	default:
		vendor = fmt.Sprintf("%s S3-compatible storage", vendor)
	}
	disclosure.Destination = fmt.Sprintf("%s bucket %q", vendor, backend.Bucket)
	if backend.Region != "" {
		disclosure.Region = backend.Region
	}
	return disclosure
}

// DisclosureVersion is the version of the disclosure a tenant accepts.
func (s *ConsentService) DisclosureVersion() string {
	return persistence.CurrentDisclosureVersion
}

// Disclosure returns the copy clients must show before accepting fallback.
func (s *ConsentService) Disclosure() FallbackDisclosure {
	return s.disclosure
}

// ConsentView is what a client is told.
//
// It reports "never asked" separately from "declined" so the client can show the
// right thing: an invitation in the first case, and a confirmation in the second.
type ConsentView struct {
	Accepted          bool               `json:"accepted"`
	EverAsked         bool               `json:"ever_asked"`
	Granted           bool               `json:"granted"`
	DisclosureVersion string             `json:"disclosure_version"`
	Disclosure        FallbackDisclosure `json:"disclosure"`
}

// Show returns the tenant's current consent state.
func (s *ConsentService) Show(ctx context.Context, scope persistence.Scope) (ConsentView, error) {
	consent, err := s.store.Resolve(ctx, scope.TenantID)
	if errors.Is(err, persistence.ErrNoConsent) {
		return s.view(false, false, false, s.DisclosureVersion()), nil
	}
	if err != nil {
		return ConsentView{}, apperr.Wrap(err, apperr.KindInternal, "Internal error",
			"could not read the fallback consent record")
	}
	return s.view(consent.Accepted, true, consent.Granted(), consent.DisclosureVersion), nil
}

// Accept records the tenant's acceptance.
//
// A tenant that has never been asked is not asked here: accepting creates the record,
// which is the whole point of the call.
func (s *ConsentService) Accept(ctx context.Context, scope persistence.Scope) (ConsentView, error) {
	consent, err := s.store.Accept(ctx, scope.TenantID)
	if err != nil {
		return ConsentView{}, apperr.Wrap(err, apperr.KindInternal, "Internal error",
			"could not record the fallback consent")
	}
	return s.view(consent.Accepted, true, consent.Granted(), consent.DisclosureVersion), nil
}

// Withdraw records the tenant's refusal and closes the gate immediately.
//
// Withdrawal is not reversible by this call alone: re-accepting requires an explicit
// Accept, so a tenant cannot lose the fact that they once declined.
func (s *ConsentService) Withdraw(ctx context.Context, scope persistence.Scope) (ConsentView, error) {
	if err := s.store.Withdraw(ctx, scope.TenantID); err != nil {
		return ConsentView{}, apperr.Wrap(err, apperr.KindInternal, "Internal error",
			"could not withdraw the fallback consent")
	}
	consent, err := s.store.Resolve(ctx, scope.TenantID)
	if errors.Is(err, persistence.ErrNoConsent) {
		return s.view(false, false, false, s.DisclosureVersion()), nil
	}
	if err != nil {
		return ConsentView{}, apperr.Wrap(err, apperr.KindInternal, "Internal error",
			"could not read the fallback consent record")
	}
	return s.view(consent.Accepted, true, consent.Granted(), consent.DisclosureVersion), nil
}

func (s *ConsentService) view(accepted, everAsked, granted bool, version string) ConsentView {
	return ConsentView{
		Accepted:          accepted,
		EverAsked:         everAsked,
		Granted:           granted,
		DisclosureVersion: version,
		Disclosure:        s.disclosure,
	}
}

// ConsentGate adapts the consent store to the coordinator's routing dependency.
//
// It lives here rather than in the coordinator so the rule that a lookup failure
// must never open the gate is stated once. A database problem closing the fallback
// shut is the safe direction: uploads keep going to Drive or fail, and no file is
// moved somewhere the tenant did not agree to.
func ConsentGate(store ConsentStore, required bool) func(context.Context, persistence.Scope) (bool, error) {
	return func(ctx context.Context, scope persistence.Scope) (bool, error) {
		// A deployment that does not require consent gets it by configuration. Reading
		// the store anyway and reporting "not granted" would refuse the upload for a
		// disclosure nobody asked for -- which is what happened: the flag existed,
		// validated fine, and was ignored at runtime.
		if !required {
			return true, nil
		}
		granted, err := store.Granted(ctx, scope.TenantID)
		if err != nil {
			return false, err
		}
		return granted, nil
	}
}
