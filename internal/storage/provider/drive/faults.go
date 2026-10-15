package drive

import (
	"context"

	"github.com/shovan04/storvia/internal/storage/model"
	"github.com/shovan04/storvia/internal/storage/policy"
)

// FaultInjection controls deliberate failure injection for testing.
//
// This exists so the outage-routing path can be exercised without waiting for
// Google to actually be down. It is the only way to prove the mechanism works:
// a real outage is rare, unreproducible, and never on demand.
//
// The controls are gated behind two independent requirements. Both must hold, so
// that turning the flag on in a production configuration file is still refused:
//
//   - AllowFaultInjection must be passed true, which app wiring does only when the
//     environment is development.
//   - the individual fault must be enabled in configuration.
//
// A provider that can be told to lie about its own health is a dangerous thing to
// ship, so every method here refuses when the gate is not open.
type FaultInjection struct {
	// DriveUnavailable makes the read-only availability preflight report a
	// transport failure, which is the one class that makes a new upload eligible
	// for the fallback.
	DriveUnavailable bool
	// gate is true only in a development wiring.
	gate bool
}

// NewFaultInjection builds an injector.
//
// gate must come from the environment, never from configuration: a config file can
// be copied from development into production by accident, and an environment
// cannot.
func NewFaultInjection(gate bool, driveUnavailable bool) FaultInjection {
	return FaultInjection{gate: gate, DriveUnavailable: driveUnavailable}
}

// Enabled reports whether fault injection is available at all.
func (f FaultInjection) Enabled() bool { return f.gate && f.DriveUnavailable }

// FaultsEnabled reports whether this client has fault injection armed. The admin
// panel surfaces it so an operator is never misled by a simulated outage.
func (c *Client) FaultsEnabled() bool { return c.faults.Enabled() }

// simulateUnavailable wraps the real availability check.
//
// When the fault is active the real check is skipped and an unavailability is
// reported instead, so the test is deterministic. When it is not, the real check
// runs untouched.
func (f FaultInjection) simulateUnavailable(ctx context.Context, scope model.AvailabilityScope, real func(context.Context, model.AvailabilityScope) error) error {
	if f.Enabled() {
		return &policy.ProviderError{
			Provider:  string(model.ProviderGoogleDrive),
			Operation: "Availability",
			Class:     policy.ClassUnavailable,
			// Stated explicitly so an operator reading a log during a test knows
			// the failure was injected rather than real.
			SafeDetail: "injected fault: drive availability preflight forced to fail",
			Retryable:  true,
		}
	}
	return real(ctx, scope)
}
