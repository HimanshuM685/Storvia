package policy

import (
	"testing"
	"time"
)

// allGatesOpen returns a Gates value with every precondition satisfied.
func allGatesOpen() Gates {
	return Gates{
		ConnectionUsable:        true,
		Authorized:              true,
		RequestValid:            true,
		KnownQuotaBlock:         false,
		FallbackConsent:         true,
		FallbackConfigured:      true,
		FallbackBudgetRemaining: true,
	}
}

// TestEvaluatePolicyTable mirrors the table in docs/storage-policy.md section 2.
//
// Each row is a documented condition. The test exists so the code and the
// document cannot drift: if a rule changes in one place, this fails.
func TestEvaluatePolicyTable(t *testing.T) {
	tests := []struct {
		name     string
		request  RoutingRequest
		want     FallbackDecision
		wantProv string
		// wantFallbackEligible asserts whether the fallback door is open. It is
		// separate from the decision because a decision to use Drive can still
		// leave the door open for a future attempt.
		wantFallbackEligible bool
	}{
		{
			name:     "drive responds successfully",
			request:  RoutingRequest{Gates: allGatesOpen()},
			want:     DecisionUseDrive,
			wantProv: "google_drive",
			// The door is open but unused: a later ambiguity may not use it.
			wantFallbackEligible: true,
		},
		{
			name: "preflight timeout after bounded retry",
			request: RoutingRequest{
				PreflightError: &ProviderError{Class: ClassUnavailable, Operation: "Availability"},
				PreflightPhase: PhasePreflight,
				Gates:          allGatesOpen(),
			},
			want:                 DecisionUseFallback,
			wantProv:             "s3_compatible",
			wantFallbackEligible: true,
		},
		{
			name: "preflight repeated 5xx is service unavailability",
			request: RoutingRequest{
				PreflightError: &ProviderError{Class: ClassUnavailable, StatusCode: 503},
				PreflightPhase: PhasePreflight,
				Gates:          allGatesOpen(),
			},
			want:                 DecisionUseFallback,
			wantProv:             "s3_compatible",
			wantFallbackEligible: true,
		},
		{
			name: "open circuit routes eligible new uploads to fallback",
			request: RoutingRequest{
				PreflightError: &ProviderError{Class: ClassUnavailable},
				PreflightPhase: PhasePreflight,
				CircuitOpen:    true,
				Gates:          allGatesOpen(),
			},
			want:     DecisionUseFallback,
			wantProv: "s3_compatible",
			// Once a provider was selected, no further switching is allowed.
			wantFallbackEligible: true,
		},
		{
			name: "drive storage full does not fall back",
			request: RoutingRequest{
				PreflightError: &ProviderError{Class: ClassQuota, Reason: "storageQuotaExceeded"},
				PreflightPhase: PhasePreflight,
				Gates:          allGatesOpen(),
			},
			want:                 DecisionReject,
			wantProv:             "",
			wantFallbackEligible: false,
		},
		{
			name: "drive rate limit does not fall back",
			request: RoutingRequest{
				PreflightError: &ProviderError{Class: ClassRateLimited, StatusCode: 429},
				PreflightPhase: PhasePreflight,
				Gates:          allGatesOpen(),
			},
			want:                 DecisionUseDrive,
			wantProv:             "google_drive",
			wantFallbackEligible: false,
		},
		{
			name: "revoked grant does not fall back",
			request: RoutingRequest{
				PreflightError: &ProviderError{Class: ClassConsentRequired, Reason: "invalid_grant"},
				PreflightPhase: PhasePreflight,
				Gates:          allGatesOpen(),
			},
			want:     DecisionReject,
			wantProv: "",
			// A revoked grant must never reach the fallback bucket.
			wantFallbackEligible: false,
		},
		{
			name: "workspace admin policy does not fall back",
			request: RoutingRequest{
				PreflightError: &ProviderError{Class: ClassAuthorization, Reason: "domainPolicy"},
				PreflightPhase: PhasePreflight,
				Gates:          allGatesOpen(),
			},
			want:                 DecisionReject,
			wantProv:             "",
			wantFallbackEligible: false,
		},
		{
			name: "missing folder does not fall back",
			request: RoutingRequest{
				PreflightError: &ProviderError{Class: ClassMissing, StatusCode: 404},
				PreflightPhase: PhasePreflight,
				Gates:          allGatesOpen(),
			},
			want:                 DecisionUseDrive,
			wantProv:             "google_drive",
			wantFallbackEligible: false,
		},
		{
			name: "invalid request is rejected",
			request: RoutingRequest{
				PreflightError: &ProviderError{Class: ClassValidation},
				PreflightPhase: PhasePreflight,
				Gates:          allGatesOpen(),
			},
			want:     DecisionReject,
			wantProv: "",
			// A client-side mistake is not an outage.
			wantFallbackEligible: false,
		},
		{
			name: "unclassified error is refused rather than guessed",
			request: RoutingRequest{
				PreflightError: &ProviderError{Class: ClassUnknown},
				PreflightPhase: PhasePreflight,
				Gates:          allGatesOpen(),
			},
			want:                 DecisionDefer,
			wantProv:             "",
			wantFallbackEligible: false,
		},
		{
			name: "uncertain preflight result is not evidence of an outage",
			request: RoutingRequest{
				PreflightError: &ProviderError{Class: ClassUncertain},
				PreflightPhase: PhasePreflight,
				Gates:          allGatesOpen(),
			},
			want:                 DecisionDefer,
			wantProv:             "",
			wantFallbackEligible: false,
		},
		{
			name: "no fallback consent blocks the fallback",
			request: RoutingRequest{
				PreflightError: &ProviderError{Class: ClassUnavailable},
				PreflightPhase: PhasePreflight,
				Gates:          gatesWith(func(g *Gates) { g.FallbackConsent = false }),
			},
			want:                 DecisionUseDrive,
			wantProv:             "google_drive",
			wantFallbackEligible: false,
		},
		{
			name: "no configured backend blocks the fallback",
			request: RoutingRequest{
				PreflightError: &ProviderError{Class: ClassUnavailable},
				PreflightPhase: PhasePreflight,
				Gates:          gatesWith(func(g *Gates) { g.FallbackConfigured = false }),
			},
			want:                 DecisionUseDrive,
			wantProv:             "google_drive",
			wantFallbackEligible: false,
		},
		{
			name: "exhausted fallback budget blocks the fallback",
			request: RoutingRequest{
				PreflightError: &ProviderError{Class: ClassUnavailable},
				PreflightPhase: PhasePreflight,
				Gates:          gatesWith(func(g *Gates) { g.FallbackBudgetRemaining = false }),
			},
			want:                 DecisionUseDrive,
			wantProv:             "google_drive",
			wantFallbackEligible: false,
		},
		{
			name: "unauthorized tenant is rejected before the provider is consulted",
			request: RoutingRequest{
				Gates: gatesWith(func(g *Gates) { g.Authorized = false }),
			},
			want:     DecisionReject,
			wantProv: "",
			// An unauthorized request never reaches a provider at all.
			wantFallbackEligible: false,
		},
		{
			name: "invalid request is rejected before the provider is consulted",
			request: RoutingRequest{
				Gates: gatesWith(func(g *Gates) { g.RequestValid = false }),
			},
			want:                 DecisionReject,
			wantProv:             "",
			wantFallbackEligible: false,
		},
		{
			name: "missing drive connection is rejected",
			request: RoutingRequest{
				Gates: gatesWith(func(g *Gates) { g.ConnectionUsable = false }),
			},
			want:                 DecisionReject,
			wantProv:             "",
			wantFallbackEligible: false,
		},
		{
			name: "availability failure outside the read-only phase is not eligible",
			request: RoutingRequest{
				PreflightError: &ProviderError{Class: ClassUnavailable},
				PreflightPhase: PhaseTransfer,
				Gates:          allGatesOpen(),
			},
			want:     DecisionUseDrive,
			wantProv: "google_drive",
			// Bytes have left the process, so a second write could duplicate them.
			wantFallbackEligible: false,
		},
		{
			name: "availability failure while a mutation may have started is pinned",
			request: RoutingRequest{
				PreflightError:         &ProviderError{Class: ClassUnavailable},
				PreflightPhase:         PhasePreflight,
				MutationMayHaveStarted: true,
				Gates:                  allGatesOpen(),
			},
			want:     DecisionUseDrive,
			wantProv: "google_drive",
			// The single most important invariant in the project.
			wantFallbackEligible: false,
		},
		{
			name: "configuration error never becomes an outage",
			request: RoutingRequest{
				PreflightError: &ProviderError{Class: ClassConfiguration},
				PreflightPhase: PhasePreflight,
				Gates:          allGatesOpen(),
			},
			want:                 DecisionReject,
			wantProv:             "",
			wantFallbackEligible: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(tc.request)
			if got.Decision != tc.want {
				t.Errorf("Decision = %q, want %q (reason %q, message %q)",
					got.Decision, tc.want, got.Reason, got.Message)
			}
			if got.Provider != tc.wantProv {
				t.Errorf("Provider = %q, want %q", got.Provider, tc.wantProv)
			}
			if got.EligibleForFallback != tc.wantFallbackEligible {
				t.Errorf("EligibleForFallback = %v, want %v (reason %q)",
					got.EligibleForFallback, tc.wantFallbackEligible, got.Reason)
			}
			if got.Reason == "" {
				t.Error("Reason must always be set: it is persisted as routing evidence")
			}
			if got.Message == "" {
				t.Error("Message must always be set for operator diagnostics")
			}
		})
	}
}

// gatesWith builds gates with one field overridden.
func gatesWith(mutate func(*Gates)) Gates {
	gates := allGatesOpen()
	mutate(&gates)
	return gates
}

// TestEvaluateNeverSelectsFallbackForIneligibleClasses is a blunt invariant
// check: for every class other than ClassUnavailable, the fallback must never be
// selected. It guards against a future branch that widens eligibility by accident.
func TestEvaluateNeverSelectsFallbackForIneligibleClasses(t *testing.T) {
	classes := []FailureClass{
		ClassValidation, ClassAuthentication, ClassAuthorization, ClassConsentRequired,
		ClassQuota, ClassRateLimited, ClassUncertain, ClassMissing, ClassConfiguration,
		ClassConflict, ClassUnknown, ClassNone,
	}
	for _, class := range classes {
		t.Run(string(class), func(t *testing.T) {
			got := Evaluate(RoutingRequest{
				PreflightError: &ProviderError{Class: class},
				PreflightPhase: PhasePreflight,
				Gates:          allGatesOpen(),
			})
			if got.Decision == DecisionUseFallback {
				t.Fatalf("class %q selected the fallback provider; only ClassUnavailable may", class)
			}
		})
	}
}

// TestMutationStartedNeverFallsBack checks the invariant across every class.
func TestMutationStartedNeverFallsBack(t *testing.T) {
	classes := []FailureClass{
		ClassUnavailable, ClassQuota, ClassRateLimited, ClassUncertain,
		ClassUnknown, ClassConsentRequired,
	}
	for _, class := range classes {
		t.Run(string(class), func(t *testing.T) {
			got := Evaluate(RoutingRequest{
				PreflightError:         &ProviderError{Class: class},
				PreflightPhase:         PhasePreflight,
				MutationMayHaveStarted: true,
				Gates:                  allGatesOpen(),
			})
			if got.Decision == DecisionUseFallback {
				t.Fatalf("class %q selected the fallback after a mutation may have started", class)
			}
			if got.EligibleForFallback {
				t.Fatalf("class %q left the fallback door open after a mutation may have started", class)
			}
			if got.Reason != ReasonMutationInFlight {
				t.Errorf("Reason = %q, want %q", got.Reason, ReasonMutationInFlight)
			}
		})
	}
}

// TestCircuitNeverOpensForIneligibleClasses checks the scoped circuit rule: only a
// genuine availability failure may open it.
func TestCircuitNeverOpensForIneligibleClasses(t *testing.T) {
	circuit := NewCircuit(2, 30*time.Second)
	now := time.Now()

	classes := []FailureClass{
		ClassQuota, ClassRateLimited, ClassConsentRequired, ClassAuthentication,
		ClassAuthorization, ClassValidation, ClassMissing, ClassConfiguration, ClassUnknown,
	}
	for _, class := range classes {
		circuit.Observe(&ProviderError{Class: class}, now)
		if circuit.State != CircuitClosed {
			t.Fatalf("class %q opened the circuit; only availability failures may", class)
		}
		if circuit.RoutesToFallback() {
			t.Fatalf("class %q routed to fallback via the circuit", class)
		}
	}
	if circuit.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures = %d, want 0", circuit.ConsecutiveFailures)
	}
}

// TestCircuitNeverOpensForMutationFailures checks that an ambiguous write cannot
// open the outage circuit.
func TestCircuitNeverOpensForMutationFailures(t *testing.T) {
	circuit := NewCircuit(2, 30*time.Second)
	now := time.Now()

	for i := 0; i < 5; i++ {
		circuit.Observe(&ProviderError{
			Class:                  ClassUnavailable,
			MutationMayHaveStarted: true,
		}, now)
	}
	if circuit.State != CircuitClosed {
		t.Fatalf("a mutation-bearing failure opened the circuit; the outage circuit must only count preflight availability failures")
	}
}

// TestCircuitOpensAfterConsecutiveAvailabilityFailures walks the documented
// sequence: two failures open it, cooldown allows one probe, success closes it.
func TestCircuitOpensAfterConsecutiveAvailabilityFailures(t *testing.T) {
	circuit := NewCircuit(2, 30*time.Second)
	start := time.Now()
	timeout := &ProviderError{Class: ClassUnavailable}

	if got := circuit.Observe(timeout, start); got != CircuitClosed {
		t.Fatalf("after 1 failure state = %q, want closed", got)
	}
	if got := circuit.Observe(timeout, start); got != CircuitOpen {
		t.Fatalf("after 2 failures state = %q, want open", got)
	}
	if !circuit.RoutesToFallback() {
		t.Fatal("an open circuit must report that it routes to the fallback")
	}

	// Before the cooldown elapses the circuit stays open.
	if got := circuit.Observe(timeout, start.Add(29*time.Second)); got != CircuitOpen {
		t.Errorf("state before cooldown = %q, want open", got)
	}

	// After the cooldown, one probe is allowed in the half-open state.
	afterCooldown := start.Add(31 * time.Second)
	if got := circuit.Observe(timeout, afterCooldown); got != CircuitHalfOpen {
		t.Errorf("probe failure state = %q, want half_open", got)
	}

	// A successful probe closes it immediately.
	if got := circuit.Observe(nil, afterCooldown.Add(time.Second)); got != CircuitClosed {
		t.Errorf("state after successful probe = %q, want closed", got)
	}
	if circuit.ConsecutiveFailures != 0 {
		t.Errorf("ConsecutiveFailures = %d after recovery, want 0", circuit.ConsecutiveFailures)
	}
}

// TestCircuitProbeClosesOnSuccess checks the documented "a successful probe closes
// it immediately" behaviour.
func TestCircuitProbeClosesOnSuccess(t *testing.T) {
	circuit := NewCircuit(1, 10*time.Second)
	start := time.Now()

	circuit.Observe(&ProviderError{Class: ClassUnavailable}, start)
	if circuit.State != CircuitOpen {
		t.Fatalf("state = %q, want open", circuit.State)
	}
	if got := circuit.Observe(nil, start.Add(11*time.Second)); got != CircuitClosed {
		t.Errorf("state = %q, want closed", got)
	}
}

// TestEligibleFailureForError ties the table to a single predicate.
func TestEligibleFailureForError(t *testing.T) {
	if EligibleForFallbackForError(nil, PhasePreflight) {
		t.Error("a nil error must not be eligible")
	}
	if EligibleForFallbackForError(&ProviderError{Class: ClassUnavailable}, PhaseTransfer) {
		t.Error("a non-preflight phase must not be eligible")
	}
	if !EligibleForFallbackForError(&ProviderError{Class: ClassUnavailable}, PhasePreflight) {
		t.Error("a preflight availability failure must be eligible")
	}
	if EligibleFailure(&ProviderError{Class: ClassQuota}) {
		t.Error("a quota failure must not be eligible")
	}
}

// TestClassifyGoogleError covers the documented status and reason mapping.
func TestClassifyGoogleError(t *testing.T) {
	tests := []struct {
		name   string
		status int
		reason string
		want   FailureClass
	}{
		{"no status is a transport failure", 0, "", ClassUnavailable},
		{"storage quota exceeded", 403, "storageQuotaExceeded", ClassQuota},
		{"domain policy is an authorization block", 403, "domainPolicy", ClassAuthorization},
		{"user rate limit", 403, "userRateLimitExceeded", ClassRateLimited},
		{"rate limit exceeded", 429, "rateLimitExceeded", ClassRateLimited},
		{"not found", 404, "notFound", ClassMissing},
		{"invalid grant", 401, "invalidGrant", ClassAuthentication},
		{"backend error", 500, "backendError", ClassUnavailable},
		{"internal error", 500, "internalError", ClassUnavailable},
		{"bad request", 400, "badRequest", ClassValidation},
		{"bare 401", 401, "", ClassAuthentication},
		{"bare 403", 403, "", ClassAuthorization},
		{"bare 404", 404, "", ClassMissing},
		{"bare 429", 429, "", ClassRateLimited},
		{"bare 500", 500, "", ClassUnavailable},
		{"bare 502", 502, "", ClassUnavailable},
		{"bare 503", 503, "", ClassUnavailable},
		{"bare 504", 504, "", ClassUnavailable},
		{"unexpected 418", 418, "", ClassValidation},
		{"unmapped 2xx", 204, "", ClassUnknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyGoogleError(tc.status, tc.reason); got != tc.want {
				t.Errorf("ClassifyGoogleError(%d, %q) = %q, want %q", tc.status, tc.reason, got, tc.want)
			}
		})
	}
}

// TestClassifyS3Error covers the S3-compatible mapping.
func TestClassifyS3Error(t *testing.T) {
	tests := []struct {
		name   string
		status int
		code   string
		want   FailureClass
	}{
		{"missing key", 404, "NoSuchKey", ClassMissing},
		{"missing bucket", 404, "NoSuchBucket", ClassMissing},
		{"signature mismatch is configuration", 403, "SignatureDoesNotMatch", ClassConfiguration},
		{"invalid access key is configuration", 403, "InvalidAccessKeyId", ClassConfiguration},
		{"slow down is rate limited", 503, "SlowDown", ClassRateLimited},
		{"request limit is rate limited", 429, "RequestLimitExceeded", ClassRateLimited},
		{"quota exceeded", 400, "QuotaExceeded", ClassQuota},
		{"invalid request is validation", 400, "InvalidRequest", ClassValidation},
		{"internal error", 500, "InternalError", ClassUnavailable},
		{"bare 403 is configuration", 403, "", ClassConfiguration},
		{"bare 500 is unavailable", 500, "", ClassUnavailable},
		{"no status is transport", 0, "", ClassUnavailable},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyS3Error(tc.status, tc.code); got != tc.want {
				t.Errorf("ClassifyS3Error(%d, %q) = %q, want %q", tc.status, tc.code, got, tc.want)
			}
		})
	}
}

// TestClassifyOnlyUnavailableMapsToFallbackAcrossBothProviders is the cross-check
// that the two provider classifiers agree on the routing consequence.
func TestClassifyOnlyUnavailableMapsToFallbackAcrossBothProviders(t *testing.T) {
	googleCases := []struct {
		status int
		reason string
	}{
		{0, ""}, {403, "storageQuotaExceeded"}, {403, "domainPolicy"},
		{403, "userRateLimitExceeded"}, {404, "notFound"}, {401, "invalidGrant"},
		{400, "badRequest"}, {500, "backendError"}, {503, ""},
	}
	for _, tc := range googleCases {
		class := ClassifyGoogleError(tc.status, tc.reason)
		eligible := EligibleForFallbackForError(&ProviderError{Class: class}, PhasePreflight)
		want := class == ClassUnavailable
		if eligible != want {
			t.Errorf("google(%d,%q) class=%q eligible=%v, want %v", tc.status, tc.reason, class, eligible, want)
		}
	}

	s3Cases := []struct {
		status int
		code   string
	}{
		{404, "NoSuchKey"}, {403, "SignatureDoesNotMatch"}, {503, "SlowDown"},
		{400, "QuotaExceeded"}, {400, "InvalidRequest"}, {500, "InternalError"}, {0, ""},
	}
	for _, tc := range s3Cases {
		class := ClassifyS3Error(tc.status, tc.code)
		eligible := EligibleForFallbackForError(&ProviderError{Class: class}, PhasePreflight)
		want := class == ClassUnavailable
		if eligible != want {
			t.Errorf("s3(%d,%q) class=%q eligible=%v, want %v", tc.status, tc.code, class, eligible, want)
		}
	}
}

// TestRetryBudgetStopsAttempts checks that a bounded budget is actually bounded.
func TestRetryBudgetStopsAttempts(t *testing.T) {
	budget := RetryBudget{Total: 8 * time.Second, MaxAttempts: 2}

	if _, allowed := budget.Allow(3 * time.Second); !allowed {
		t.Fatal("the first attempt should be allowed")
	}

	budget = budget.Consume(3 * time.Second)
	if _, allowed := budget.Allow(3 * time.Second); !allowed {
		t.Fatal("the second attempt should be allowed")
	}

	budget = budget.Consume(3 * time.Second)
	if _, allowed := budget.Allow(3 * time.Second); allowed {
		t.Fatal("the third attempt must be refused: MaxAttempts is 2")
	}
	if !budget.Exhausted() {
		t.Error("budget should report exhausted")
	}
	if got := budget.AttemptsLeft(); got != 0 {
		t.Errorf("AttemptsLeft = %d, want 0", got)
	}
}

// TestRetryBudgetRefusesAttemptLargerThanRemaining is the guard against a single
// call outliving the whole routing budget.
func TestRetryBudgetRefusesAttemptLargerThanRemaining(t *testing.T) {
	budget := RetryBudget{Total: 8 * time.Second, MaxAttempts: 2}
	budget = budget.Consume(6 * time.Second)

	if _, allowed := budget.Allow(3 * time.Second); allowed {
		t.Fatal("a 3s attempt must be refused when only 2s remain")
	}
	if _, allowed := budget.Allow(2 * time.Second); !allowed {
		t.Fatal("a 2s attempt that exactly fits the remaining budget should be allowed")
	}
}

// TestRetryBudgetStopsOnElapsedTime checks the time-based stop independent of
// the attempt count.
func TestRetryBudgetStopsOnElapsedTime(t *testing.T) {
	budget := RetryBudget{Total: 5 * time.Second, MaxAttempts: 100}
	budget = budget.Consume(5 * time.Second)

	if !budget.Exhausted() {
		t.Fatal("a budget with no time left must be exhausted regardless of MaxAttempts")
	}
	if _, allowed := budget.Allow(time.Second); allowed {
		t.Fatal("no attempt may start with no time left")
	}
}

// TestBackoffIsBoundedAndJittered checks the backoff shape.
//
// The jitter matters operationally: a Drive blip fails many uploads at once, and
// without jitter they would all retry in lockstep.
func TestBackoffIsBoundedAndJittered(t *testing.T) {
	base := 250 * time.Millisecond
	max := 2 * time.Second

	seen := make(map[time.Duration]struct{})
	for attempt := 0; attempt < 6; attempt++ {
		for _, seed := range []float64{0, 0.25, 0.5, 0.75, 0.999} {
			got := Backoff(attempt, base, max, seed)
			if got <= 0 {
				t.Fatalf("Backoff(%d, %.3f) = %v, must be positive", attempt, seed, got)
			}
			if got > max {
				t.Fatalf("Backoff(%d, %.3f) = %v, must not exceed %v", attempt, seed, got, max)
			}
			seen[got] = struct{}{}
		}
	}
	if len(seen) < 5 {
		t.Errorf("backoff produced only %d distinct values; jitter looks absent", len(seen))
	}

	// Saturates at the cap rather than growing without bound.
	if got := Backoff(20, base, max, 0.999); got > max {
		t.Errorf("Backoff(20) = %v, must saturate at %v", got, max)
	}

	// A negative attempt index is treated as zero rather than panicking.
	if got := Backoff(-1, base, max, 0.5); got <= 0 {
		t.Errorf("Backoff(-1) = %v, want a positive delay", got)
	}
}

// TestCanTransition walks the state machine from docs/architecture.md.
func TestCanTransition(t *testing.T) {
	legal := []struct{ from, to ObjectState }{
		{"pending", "initiating"},
		{"pending", "failed"},
		{"selected", "initiating"},
		{"initiating", "active"},
		{"initiating", "outcome_unknown"},
		{"active", "verifying"},
		{"active", "outcome_unknown"},
		{"verifying", "ready"},
		{"verifying", "outcome_unknown"},
		{"outcome_unknown", "active"},
		{"outcome_unknown", "verifying"},
		{"outcome_unknown", "failed"},
		{"failed", "selected"},
		{"blocked", "selected"},
		{"ready", "deleting"},
		{"ready", "missing"},
		{"deleting", "deleted"},
		{"missing", "selected"},
	}
	for _, tc := range legal {
		if !CanTransition(tc.from, tc.to) {
			t.Errorf("%s -> %s should be legal", tc.from, tc.to)
		}
		if err := ValidateTransition(tc.from, tc.to); err != nil {
			t.Errorf("ValidateTransition(%s, %s) = %v, want nil", tc.from, tc.to, err)
		}
	}

	illegal := []struct{ from, to ObjectState }{
		// A ready object never goes back to uploading: that would be a second
		// location for the same logical object.
		{"ready", "uploading"},
		{"ready", "active"},
		{"ready", "pending"},
		// Deletion is terminal.
		{"deleted", "selected"},
		{"deleted", "ready"},
		// A failed attempt cannot be retried without a cleanup decision first.
		{"failed", "active"},
		{"failed", "ready"},
		{"blocked", "ready"},
		// Selected cannot jump straight to ready without verification.
		{"selected", "ready"},
		// outcome_unknown is not a shortcut to ready.
		{"outcome_unknown", "ready"},
		{"pending", "ready"},
		{"pending", "outcome_unknown"},
	}
	for _, tc := range illegal {
		if CanTransition(tc.from, tc.to) {
			t.Errorf("%s -> %s must be illegal", tc.from, tc.to)
		}
		if err := ValidateTransition(tc.from, tc.to); err == nil {
			t.Errorf("ValidateTransition(%s, %s) = nil, want an error", tc.from, tc.to)
		}
	}
}

// TestReadyHasNoTransitionBackToUploading restates the single-location invariant
// at the state-machine level, where it is cheapest to enforce.
func TestReadyHasNoTransitionBackToUploading(t *testing.T) {
	for _, to := range []ObjectState{"pending", "selected", "initiating", "active", "verifying", "outcome_unknown"} {
		if CanTransition("ready", to) {
			t.Errorf("ready -> %s must be illegal; a ready object already has one location", to)
		}
	}
}

// TestOperatorForceFallbackBeatsHealthyDrive is the point of the switch: an operator
// must be able to send everything to the fallback while Drive is perfectly healthy,
// because during an incident "does the fallback work?" is unanswerable otherwise.
func TestOperatorForceFallbackBeatsHealthyDrive(t *testing.T) {
	got := Evaluate(RoutingRequest{
		Gates: gatesWith(func(g *Gates) { g.OperatorForcedFallback = true }),
		// No preflight error at all: Drive answered fine.
	})
	if got.Decision != DecisionUseFallback {
		t.Fatalf("decision = %q, want use_fallback", got.Decision)
	}
	if got.Reason != ReasonForceFallback {
		t.Errorf("reason = %q, want %q", got.Reason, ReasonForceFallback)
	}
	if got.Provider != "s3_compatible" {
		t.Errorf("provider = %q, want s3_compatible", got.Provider)
	}
}

// TestOperatorForceFallbackStillRequiresConsent is the important limit. An operator
// override changes which provider Drive's health chooses; it does not remove the
// obligation to have consent before writing a tenant's file somewhere else.
func TestOperatorForceFallbackStillRequiresConsent(t *testing.T) {
	got := Evaluate(RoutingRequest{
		Gates: gatesWith(func(g *Gates) {
			g.OperatorForcedFallback = true
			g.FallbackConsent = false
		}),
	})
	if got.Decision != DecisionReject {
		t.Fatalf("decision = %q, want reject", got.Decision)
	}
	if got.Reason != ReasonNoFallbackConsent {
		t.Errorf("reason = %q, want %q", got.Reason, ReasonNoFallbackConsent)
	}
}

// TestOperatorForceFallbackWithNoBackendFailsLoudly. Routing to a fallback that does
// not exist would otherwise surface as a confusing provider error much later.
func TestOperatorForceFallbackWithNoBackendFailsLoudly(t *testing.T) {
	got := Evaluate(RoutingRequest{
		Gates: gatesWith(func(g *Gates) {
			g.OperatorForcedFallback = true
			g.FallbackConfigured = false
		}),
	})
	if got.Decision != DecisionReject || got.Reason != ReasonNoFallbackConfigured {
		t.Errorf("got %q/%q, want reject/%q", got.Decision, got.Reason, ReasonNoFallbackConfigured)
	}
}

// TestOperatorForceFallbackDoesNotOverrideInvalidOrUnauthorized. The override is a
// routing preference, not a way to push an invalid or unauthorized request through.
func TestOperatorForceFallbackDoesNotOverrideInvalidOrUnauthorized(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*Gates)
		reason string
	}{
		{"invalid request", func(g *Gates) {
			g.OperatorForcedFallback = true
			g.RequestValid = false
		}, ReasonInvalidRequest},
		{"unauthorized", func(g *Gates) {
			g.OperatorForcedFallback = true
			g.Authorized = false
		}, ReasonNotAuthorized},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Evaluate(RoutingRequest{Gates: gatesWith(tc.mutate)})
			if got.Decision != DecisionReject || got.Reason != tc.reason {
				t.Errorf("got %q/%q, want reject/%q", got.Decision, got.Reason, tc.reason)
			}
		})
	}
}

// TestOperatorForceFallbackLeavesFallbackEligible. The override is about which provider
// to prefer for a *new* upload. If the fallback then fails mid-flight, retrying it is
// still permitted, because closing the door would strand the upload.
func TestOperatorForceFallbackLeavesFallbackEligible(t *testing.T) {
	got := Evaluate(RoutingRequest{
		Gates: gatesWith(func(g *Gates) { g.OperatorForcedFallback = true }),
	})
	if !got.EligibleForFallback {
		t.Error("forcing fallback must not close the fallback door for a later failure")
	}
}

// TestForceFallbackCannotRelocateAnInFlightMutation is the invariant that matters
// most. Even with the override on, an upload whose mutation may have started stays
// pinned to Drive: the override chooses a provider for a *new* upload and cannot
// reach back and relocate bytes that may already be sitting in Drive.
//
// Note the assertion is on the decision, not on EligibleForFallback. The policy
// reports eligibility=true there on purpose -- a preflight succeeded, so fallback
// would be permissible for some future attempt. What must not happen is that this
// upload is routed to the fallback.
func TestForceFallbackCannotRelocateAnInFlightMutation(t *testing.T) {
	got := Evaluate(RoutingRequest{
		MutationMayHaveStarted: true,
		Gates:                  gatesWith(func(g *Gates) { g.OperatorForcedFallback = true }),
	})
	if got.Decision != DecisionUseDrive {
		t.Fatalf("decision = %q (%s), want use_drive", got.Decision, got.Reason)
	}
	if got.Reason != ReasonPreflightSucceeded {
		t.Errorf("reason = %q, want %q", got.Reason, ReasonPreflightSucceeded)
	}
}

// TestForceFallbackWithFailingPreflightStaysPinned is the same invariant when the
// preflight fails, which is the case that would otherwise look fallback-eligible.
func TestForceFallbackWithFailingPreflightStaysPinned(t *testing.T) {
	got := Evaluate(RoutingRequest{
		MutationMayHaveStarted: true,
		PreflightError: &ProviderError{
			Provider: "google_drive", Operation: "Availability", Class: ClassUnavailable,
		},
		Gates: gatesWith(func(g *Gates) { g.OperatorForcedFallback = true }),
	})
	if got.Decision != DecisionUseDrive {
		t.Fatalf("decision = %q (%s), want use_drive: an in-flight mutation must not be relocated",
			got.Decision, got.Reason)
	}
	if got.Reason != ReasonMutationInFlight {
		t.Errorf("reason = %q, want %q", got.Reason, ReasonMutationInFlight)
	}
	if got.EligibleForFallback {
		t.Error("fallback must be ineligible while a mutation is unresolved")
	}
}
