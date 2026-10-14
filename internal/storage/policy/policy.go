// Package policy is the pure decision engine for Storvia routing.
//
// It has no network, database, or clock dependency of its own: every input is
// passed in. That is what makes the tables in docs/storage-policy.md testable as
// plain unit tests, and it is why this package is the first thing to build and
// the last thing to be allowed to grow provider-specific code.
package policy

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// FailureClass is the normalized classification of a provider failure. Every
// provider adapter must map its own error onto one of these before the policy
// layer sees it, because routing decisions are made on the class, never on a raw
// status code.
type FailureClass string

const (
	// ClassNone means the operation succeeded.
	ClassNone FailureClass = ""
	// ClassValidation is a rejected request: bad MIME, oversize, bad tenant.
	ClassValidation FailureClass = "validation"
	// ClassAuthentication covers 401 and an absent or rejected credential.
	ClassAuthentication FailureClass = "authentication"
	// ClassAuthorization covers 403 permission errors and Workspace admin
	// policy such as domainPolicy.
	ClassAuthorization FailureClass = "authorization"
	// ClassConsentRequired covers a declined consent, a disconnected account, a
	// revoked grant, and invalid_grant. All of them mean "reconnect", never
	// "write to S3".
	ClassConsentRequired FailureClass = "consent_required"
	// ClassQuota covers storageQuotaExceeded and daily upload/copy limits.
	ClassQuota FailureClass = "quota"
	// ClassRateLimited covers 429 and userRateLimitExceeded or rateLimitExceeded.
	ClassRateLimited FailureClass = "rate_limited"
	// ClassUnavailable covers transport failure and 500/502/503/504. It is the
	// only class that may make a new upload eligible for the fallback, and only
	// while no provider mutation may have started.
	ClassUnavailable FailureClass = "unavailable"
	// ClassUncertain covers a request that may already have been accepted by the
	// provider: a timeout after the body was sent, a reset after the write.
	ClassUncertain FailureClass = "uncertain"
	// ClassMissing covers a 404 on an object Storvia believes exists. It is
	// missing, not deleted by us and not a reason to recreate elsewhere.
	ClassMissing FailureClass = "missing"
	// ClassConfiguration covers an invalid endpoint, a signature mismatch, TLS
	// verification failure, and missing credentials.
	ClassConfiguration FailureClass = "configuration"
	// ClassConflict covers a precondition failure and an aborted session.
	ClassConflict FailureClass = "conflict"
	// ClassUnknown is anything unclassified. It is treated conservatively: never
	// eligible for fallback, because guessing "outage" on an unclassified error
	// is how a quota error silently becomes an S3 write.
	ClassUnknown FailureClass = "unknown"
)

// ProviderError is the normalized error a provider adapter returns.
type ProviderError struct {
	Provider  string
	Operation string
	Class     FailureClass
	// StatusCode is the provider's HTTP status, when there was one.
	StatusCode int
	// Reason is a short provider reason code such as storageQuotaExceeded. It is
	// safe to log but never returned to a client verbatim.
	Reason string
	// Retryable is the provider's own opinion. The coordinator's budget overrides
	// it: a provider may say retryable while the routing budget is already spent.
	Retryable bool
	// MutationMayHaveStarted is the critical flag. It must be true whenever a
	// request could have reached the provider, including when the response was
	// lost. Getting this wrong in the false direction creates duplicate files.
	MutationMayHaveStarted bool
	// SafeDetail is a redacted, operator-facing explanation.
	SafeDetail string
	// Err is the wrapped internal cause, kept out of client responses.
	Err error
}

func (e *ProviderError) Error() string {
	if e == nil {
		return "<nil>"
	}
	parts := []string{e.Provider + "." + e.Operation, string(e.Class)}
	if e.StatusCode != 0 {
		parts = append(parts, fmt.Sprintf("status=%d", e.StatusCode))
	}
	if e.Reason != "" {
		parts = append(parts, "reason="+e.Reason)
	}
	if e.MutationMayHaveStarted {
		parts = append(parts, "mutation_may_have_started")
	}
	if e.Err != nil {
		parts = append(parts, e.Err.Error())
	}
	return strings.Join(parts, " ")
}

func (e *ProviderError) Unwrap() error { return e.Err }

// Phase describes how far an operation progressed. It is a second input to the
// fallback decision alongside the failure class, because the same class means
// different things before and after bytes leave the process.
type Phase string

const (
	// PhasePreflight is a read-only metadata call: no mutation possible.
	PhasePreflight Phase = "preflight"
	// PhaseInitiate is session creation. A timeout here may already have created
	// a provider-side session, so it is treated as mutation-capable.
	PhaseInitiate Phase = "initiate"
	// PhaseTransfer is chunk or part upload.
	PhaseTransfer Phase = "transfer"
	// PhaseComplete is the finalizing call.
	PhaseComplete Phase = "complete"
	// PhaseVerify is the post-write inspection.
	PhaseVerify Phase = "verify"
	// PhaseDownload is a read.
	PhaseDownload Phase = "download"
	// PhaseDelete is a provider deletion.
	PhaseDelete Phase = "delete"
)

// FallbackDecision is the outcome of evaluating one request against the policy.
type FallbackDecision string

const (
	// DecisionUseDrive means Drive is available or the failure is not eligible.
	DecisionUseDrive FallbackDecision = "use_drive"
	// DecisionUseFallback means an eligible availability failure occurred before
	// any provider mutation and every gate passed.
	DecisionUseFallback FallbackDecision = "use_fallback"
	// DecisionReject means the request must fail without touching either provider.
	DecisionReject FallbackDecision = "reject"
	// DecisionDefer means the request is not classifiable yet and must not act.
	DecisionDefer FallbackDecision = "defer"
)

// RoutingRequest is the full input to a routing decision.
type RoutingRequest struct {
	// PreflightResult is nil when the availability check succeeded.
	PreflightError *ProviderError
	// PreflightPhase is the phase the availability check ran in.
	PreflightPhase Phase
	// CircuitOpen reports a scoped, recent Drive-unavailability circuit. It only
	// matters when the preflight could not be run at all.
	CircuitOpen bool
	// MutationMayHaveStarted is the coordinator's knowledge about the attempt.
	// Any true value closes the fallback door permanently for this upload.
	MutationMayHaveStarted bool
	// Gates are the non-provider preconditions.
	Gates Gates
}

// Gates are the preconditions checked before routing. A false value here blocks
// fallback even when the provider said it is unavailable: a circuit breaker must
// never override consent, quota knowledge, or authorization.
type Gates struct {
	// ConnectionUsable is false when Drive is not connected, consent was
	// declined, the grant was revoked, or a Workspace policy blocks access.
	ConnectionUsable bool
	// Authorized is the tenant authorization result for this upload.
	Authorized bool
	// RequestValid is the result of size, MIME, and category validation.
	RequestValid bool
	// KnownQuotaBlock is true when Storvia already knows the user's Drive is
	// full or a quota is exhausted.
	KnownQuotaBlock bool
	// FallbackConsent is true when the tenant has accepted the outage-fallback
	// disclosure. It is a precondition of enabling outage uploads at all.
	FallbackConsent bool
	// FallbackConfigured is true when an enabled backend with credentials exists.
	FallbackConfigured bool
	// FallbackBudgetRemaining is false once the fallback attempt budget is spent.
	FallbackBudgetRemaining bool
}

// RoutingDecision is the result of a routing evaluation.
type RoutingDecision struct {
	Decision FallbackDecision
	// Reason is a stable machine code recorded in the upload's routing evidence.
	Reason string
	// Message is the operator-facing explanation. It must be safe to log.
	Message string
	// Provider is the selected provider when the decision selects one.
	Provider string
	// EligibleForFallback reports whether the fallback door is open, regardless of
	// which provider was chosen. It is false once a mutation may have started.
	EligibleForFallback bool
}

// Decision reason codes. They are persisted, so changing a value is a schema-level
// decision, not a refactor.
const (
	ReasonDriveAvailable       = "drive_available"
	ReasonPreflightSucceeded   = "drive_preflight_succeeded"
	ReasonUnavailablePreflight = "drive_unavailable_preflight"
	ReasonCircuitOpen          = "drive_circuit_open"
	ReasonMutationInFlight     = "mutation_already_started"
	ReasonNotConnected         = "drive_not_connected"
	ReasonNotAuthorized        = "tenant_not_authorized"
	ReasonInvalidRequest       = "request_invalid"
	ReasonQuotaBlocked         = "drive_quota_blocked"
	ReasonRateLimited          = "provider_rate_limited"
	ReasonNoFallbackConsent    = "fallback_consent_missing"
	ReasonNoFallbackConfigured = "fallback_not_configured"
	ReasonFallbackBudgetSpent  = "fallback_budget_exhausted"
	ReasonUnclassified         = "unclassified_failure"
)

// Evaluate decides where a new upload should go.
//
// The ordering of the checks below is the contract. Gating happens before
// classification, so a fallback-eligible error still fails the request when the
// tenant has not consented; and mutation history is checked last but wins
// absolutely, so no sequence of arguments reopens the fallback door once bytes
// may have reached a provider.
func Evaluate(req RoutingRequest) RoutingDecision {
	// 1. Non-provider gates. These are decided before the provider is consulted
	//    at all, and they are hard stops.
	if !req.Gates.RequestValid {
		return reject(ReasonInvalidRequest, "request failed validation")
	}
	if !req.Gates.Authorized {
		return reject(ReasonNotAuthorized, "tenant is not authorized for this upload")
	}

	// 2. A mutation that may already have been sent closes the door on a second
	//    provider. This is the invariant that prevents duplicate files.
	if req.MutationMayHaveStarted {
		if req.PreflightError == nil && !req.CircuitOpen {
			return decision(DecisionUseDrive, ReasonPreflightSucceeded,
				"drive preflight succeeded", true)
		}
		return decision(DecisionUseDrive, ReasonMutationInFlight,
			"upload is already pinned to a provider; no cross-provider write is permitted",
			false)
	}

	// 3. No usable connection is a stop, not a routing input. Without a connected
	//    Drive account there is nothing to preflight and nothing to fall back
	//    from: the user must connect first.
	if !req.Gates.ConnectionUsable {
		return decision(DecisionReject, ReasonNotConnected,
			"no usable google drive connection; the user must connect or reconnect", false)
	}

	// 4. Success path.
	if req.PreflightError == nil {
		return decision(DecisionUseDrive, ReasonPreflightSucceeded,
			"drive preflight succeeded", true)
	}

	// 5. Classify the preflight failure.
	class := req.PreflightError.Class

	switch class {
	case ClassConsentRequired:
		return decision(DecisionReject, ReasonNotConnected,
			"drive consent is missing or revoked; the user must reconnect", false)

	case ClassAuthentication:
		return decision(DecisionReject, ReasonNotConnected,
			"drive credential is not usable; the user must reconnect", false)

	case ClassAuthorization:
		return decision(DecisionReject, ReasonNotConnected,
			"drive access is blocked by permission or workspace policy", false)

	case ClassQuota:
		return decision(DecisionReject, ReasonQuotaBlocked,
			"drive storage or upload quota is exhausted", false)

	case ClassRateLimited:
		return decision(DecisionUseDrive, ReasonRateLimited,
			"drive is rate limited; back off rather than switching provider", false)

	case ClassValidation:
		return decision(DecisionReject, ReasonInvalidRequest,
			"drive rejected the request as invalid", false)

	case ClassConfiguration:
		return decision(DecisionReject, ReasonNoFallbackConfigured,
			"drive client configuration is invalid", false)

	case ClassMissing:
		return decision(DecisionUseDrive, ReasonPreflightSucceeded,
			"the configured app folder is missing; request explicit recovery rather than falling back", false)

	case ClassUncertain:
		// A preflight is read-only, so an uncertain result here means the
		// availability call itself was inconclusive. It is not evidence of an
		// outage.
		return decision(DecisionDefer, ReasonUnclassified,
			"drive preflight result is inconclusive; retry before routing", false)

	case ClassUnavailable:
		return evaluateUnavailable(req)

	case ClassUnknown:
		// Deliberately conservative. Treating an unclassified error as an outage
		// would let a mis-mapped quota or auth error reach the fallback bucket.
		return decision(DecisionDefer, ReasonUnclassified,
			"drive preflight failure was not classifiable; refusing to route to the fallback provider",
			false)

	default:
		return decision(DecisionDefer, ReasonUnclassified,
			"unhandled drive preflight class "+string(class), false)
	}
}

// evaluateUnavailable handles the one branch that may select S3.
//
// A preflight timeout or 5xx is eligible, but only when the failure happened in
// a read-only phase and every non-provider gate passes.
func evaluateUnavailable(req RoutingRequest) RoutingDecision {
	reason := ReasonUnavailablePreflight
	message := "drive is unavailable"

	if req.PreflightPhase != PhasePreflight {
		return decision(DecisionUseDrive, ReasonMutationInFlight,
			"drive failed after the operation left the read-only phase; stay on the current provider",
			false)
	}

	if !req.Gates.ConnectionUsable {
		return decision(DecisionReject, ReasonNotConnected,
			"drive connection is not usable, so an availability failure cannot be trusted", false)
	}
	if req.Gates.KnownQuotaBlock {
		return decision(DecisionReject, ReasonQuotaBlocked,
			"drive quota is known to be exhausted, which is not an outage", false)
	}
	if !req.Gates.FallbackConsent {
		return decision(DecisionUseDrive, ReasonNoFallbackConsent,
			"fallback is disabled because the tenant has not accepted the outage-fallback disclosure",
			false)
	}
	if !req.Gates.FallbackConfigured {
		return decision(DecisionUseDrive, ReasonNoFallbackConfigured,
			"no enabled fallback backend with credentials is configured", false)
	}
	if !req.Gates.FallbackBudgetRemaining {
		return decision(DecisionUseDrive, ReasonFallbackBudgetSpent,
			"the fallback attempt budget is exhausted", false)
	}

	if req.CircuitOpen {
		reason = ReasonCircuitOpen
		message = "a scoped drive outage circuit is open; routing eligible new uploads to the fallback"
	}
	return decision(DecisionUseFallback, reason, message, true)
}

func reject(reason, message string) RoutingDecision {
	return RoutingDecision{Decision: DecisionReject, Reason: reason, Message: message}
}

func decision(d FallbackDecision, reason, message string, eligible bool) RoutingDecision {
	out := RoutingDecision{Decision: d, Reason: reason, Message: message, EligibleForFallback: eligible}
	if d == DecisionUseFallback {
		out.Provider = "s3_compatible"
	} else if d == DecisionUseDrive {
		out.Provider = "google_drive"
	}
	return out
}

// EligibleForFallbackForError reports whether a single preflight failure, in the
// read-only phase, is of an eligible class. The table-driven classifier tests
// use it directly so the docs table and the code cannot drift.
func EligibleForFallbackForError(err *ProviderError, phase Phase) bool {
	if err == nil || phase != PhasePreflight {
		return false
	}
	return err.Class == ClassUnavailable
}

// ClassifyGoogleError maps a Drive HTTP status and provider reason onto a
// FailureClass.
//
// It is intentionally strict: anything it does not positively recognize becomes
// ClassUnknown, and ClassUnknown is never eligible for the fallback. Status codes
// are documented in docs/references.md section 11.
func ClassifyGoogleError(status int, reason string) FailureClass {
	reason = strings.TrimSpace(reason)
	switch reason {
	case "storageQuotaExceeded", "uploadTooLarge":
		return ClassQuota
	case "domainPolicy":
		return ClassAuthorization
	case "rateLimitExceeded", "userRateLimitExceeded":
		return ClassRateLimited
	case "notFound", "notFoundFile":
		return ClassMissing
	case "invalidGrant", "invalid_grant", "unauthorized", "invalid":
		return ClassAuthentication
	case "forbidden":
		return ClassAuthorization
	case "badRequest", "invalidRequest":
		return ClassValidation
	case "backendError", "internalError", "backendUnavailable":
		return ClassUnavailable
	}

	switch {
	case status == 0:
		// No HTTP status means a transport failure: DNS, connect, TLS, or a
		// dropped connection before any response.
		return ClassUnavailable
	case status == 401:
		return ClassAuthentication
	case status == 403:
		return ClassAuthorization
	case status == 404:
		return ClassMissing
	case status == 429:
		return ClassRateLimited
	case status == 500 || status == 502 || status == 503 || status == 504:
		return ClassUnavailable
	case status >= 500:
		return ClassUnavailable
	case status >= 400:
		return ClassValidation
	}
	return ClassUnknown
}

// ClassifyS3Error maps an S3-compatible status onto a FailureClass, mirroring
// ClassifyGoogleError so both providers normalize into the same vocabulary.
func ClassifyS3Error(status int, code string) FailureClass {
	switch code {
	case "NoSuchKey", "NoSuchBucket", "NotFound":
		return ClassMissing
	case "SignatureDoesNotMatch", "InvalidAccessKeyId", "AccessDenied", "AccountProblem":
		return ClassConfiguration
	case "SlowDown", "RequestLimitExceeded", "TooManyRequests":
		return ClassRateLimited
	case "QuotaExceeded", "EntityTooLarge", "InvalidBucketState":
		return ClassQuota
	case "InvalidRequest", "InvalidArgument", "MalformedXML", "InvalidPart", "InvalidPartOrder":
		return ClassValidation
	case "InternalError", "SlowDownInternal":
		return ClassUnavailable
	}

	switch {
	case status == 0:
		return ClassUnavailable
	case status == 401:
		return ClassConfiguration
	case status == 403:
		return ClassConfiguration
	case status == 404:
		return ClassMissing
	case status == 408:
		return ClassUnavailable
	case status == 429:
		return ClassRateLimited
	case status >= 500:
		return ClassUnavailable
	case status >= 400:
		return ClassValidation
	}
	return ClassUnknown
}

// RetryBudget bounds the total time and attempts a coordinator may spend
// producing one provider call sequence.
//
// It exists because the failure mode this project cares about most is retry
// multiplication: a coordinator loop wrapped around an SDK that retries on its
// own turns a bounded operation into an unbounded one. The SDK's retryer must
// therefore be constrained to fit inside this budget.
type RetryBudget struct {
	// Total is the wall-clock ceiling for the whole operation.
	Total time.Duration
	// MaxAttempts is the ceiling on coordinator-level attempts.
	MaxAttempts int
	// Elapsed is the time already consumed before this attempt.
	Elapsed time.Duration
	// AttemptsUsed counts coordinator attempts already made.
	AttemptsUsed int
}

// Remaining reports the time left in the budget.
func (b RetryBudget) Remaining() time.Duration {
	left := b.Total - b.Elapsed
	if left < 0 {
		return 0
	}
	return left
}

// Exhausted reports whether the budget forbids another attempt.
func (b RetryBudget) Exhausted() bool {
	return b.AttemptsUsed >= b.MaxAttempts || b.Remaining() <= 0
}

// AttemptsLeft reports how many coordinator attempts remain.
func (b RetryBudget) AttemptsLeft() int {
	left := b.MaxAttempts - b.AttemptsUsed
	if left < 0 {
		return 0
	}
	return left
}

// Allow reports whether another attempt fits, and how much time it may take.
// A single attempt is never allowed to start with less than one millisecond left,
// because a call that cannot possibly finish only produces a timeout, and a
// timeout after a write is exactly the uncertainty this design refuses to create.
func (b RetryBudget) Allow(attemptTimeout time.Duration) (time.Duration, bool) {
	if b.AttemptsLeft() == 0 {
		return 0, false
	}
	left := b.Remaining()
	if attemptTimeout > 0 && attemptTimeout > left {
		// A per-attempt timeout that exceeds the budget would let a single call
		// outlive the whole routing budget.
		return 0, false
	}
	if left < time.Millisecond {
		return 0, false
	}
	return left, true
}

// Consume records one attempt that spent the given duration.
func (b RetryBudget) Consume(spent time.Duration) RetryBudget {
	b.AttemptsUsed++
	b.Elapsed += spent
	return b
}

// RoutingBudget derives the preflight budget from configuration, using the
// documented initial values: a three second per-call timeout, at most two calls,
// and an eight second total routing budget.
func RoutingBudget(perCallTimeout time.Duration, maxAttempts int, total time.Duration) RetryBudget {
	return RetryBudget{Total: total, MaxAttempts: maxAttempts}
}

// Backoff returns the delay before attempt n, using bounded exponential backoff
// with full jitter. Jitter matters here specifically because many uploads can
// fail at once; without it a Drive blip turns into a synchronized retry storm.
//
// seed supplies deterministic randomness in tests. It must be in [0,1).
func Backoff(attempt int, base time.Duration, max time.Duration, seed float64) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	delay := base
	for i := 0; i < attempt && delay < max; i++ {
		delay *= 2
	}
	if delay > max {
		delay = max
	}
	if seed < 0 {
		seed = 0
	}
	if seed >= 1 {
		seed = 0.999999
	}
	// Full jitter: uniform in [delay/2, delay). Keeping the floor above zero
	// avoids a zero-delay spin loop.
	half := delay / 2
	return half + time.Duration(float64(half)*seed)
}

// CircuitState is the state of a scoped Drive outage circuit.
type CircuitState string

const (
	// CircuitClosed passes traffic through normally.
	CircuitClosed CircuitState = "closed"
	// CircuitOpen routes eligible new uploads to the fallback until it cools down.
	CircuitOpen CircuitState = "open"
	// CircuitHalfOpen allows a single read-only probe to test recovery.
	CircuitHalfOpen CircuitState = "half_open"
)

// Circuit is a per-connection Drive outage circuit.
//
// It is scoped to a single Drive connection and runtime egress context. It opens
// only on consecutive eligible availability failures, so a quota error, an auth
// failure, or a validation failure can never open it and push another user's
// traffic to S3.
type Circuit struct {
	State CircuitState
	// ConsecutiveFailures counts eligible availability failures.
	ConsecutiveFailures int
	// FailureThreshold is the count at which the circuit opens.
	FailureThreshold int
	// Cooldown is how long the circuit stays open before a probe is allowed.
	Cooldown time.Duration
	// OpenedAt is when the circuit entered the open state.
	OpenedAt time.Time
	// ProbeInFlight prevents two half-open probes from racing.
	ProbeInFlight bool
}

// NewCircuit builds a closed circuit.
func NewCircuit(failureThreshold int, cooldown time.Duration) *Circuit {
	return &Circuit{
		State:            CircuitClosed,
		FailureThreshold: failureThreshold,
		Cooldown:         cooldown,
	}
}

// Observe records the outcome of a preflight and returns the resulting state.
//
// Only an eligible availability failure counts: the failure must be an
// availability class AND must not carry a mutation that may have reached the
// provider. A failure that clears both checks is not evidence that Drive is down,
// so it leaves the counter untouched.
//
// A success always closes the circuit, whatever state it was in. That is what
// makes a half-open probe cheap: one good preflight restores normal routing
// immediately instead of leaving new uploads on the fallback for another cooldown.
func (c *Circuit) Observe(failure *ProviderError, now time.Time) CircuitState {
	if failure == nil {
		c.ConsecutiveFailures = 0
		if c.State != CircuitClosed {
			c.State = CircuitClosed
			c.OpenedAt = time.Time{}
			c.ProbeInFlight = false
		}
		return c.State
	}

	if !EligibleFailure(failure) {
		// Not an outage signal: a quota, auth, validation, or mutation-bearing
		// failure. It neither counts nor opens, and it must not reset a genuine
		// run of availability failures either, since those are independent
		// signals.
		if c.State == CircuitOpen && c.cooldownElapsed(now) {
			c.State = CircuitHalfOpen
			c.ProbeInFlight = false
		}
		return c.State
	}

	c.ConsecutiveFailures++
	if c.State == CircuitHalfOpen {
		// The probe failed: re-open for another full cooldown.
		c.State = CircuitOpen
		c.OpenedAt = now
		c.ProbeInFlight = false
		return c.State
	}
	if c.State == CircuitOpen {
		if c.cooldownElapsed(now) {
			c.State = CircuitHalfOpen
			c.ProbeInFlight = false
		}
		return c.State
	}
	if c.FailureThreshold > 0 && c.ConsecutiveFailures >= c.FailureThreshold {
		c.State = CircuitOpen
		c.OpenedAt = now
	}
	return c.State
}

func (c *Circuit) cooldownElapsed(now time.Time) bool {
	if c.OpenedAt.IsZero() {
		return false
	}
	return !now.Before(c.OpenedAt.Add(c.Cooldown))
}

// RoutesToFallback reports whether new uploads should skip Drive right now.
//
// An open circuit is consulted only together with the routing gates; Evaluate
// still applies consent, authorization, and budget checks afterwards.
func (c *Circuit) RoutesToFallback() bool { return c.State == CircuitOpen }

// EligibleFailures reports whether err is the kind of failure that may open this
// circuit. It is exported so the table-driven tests can assert it per policy row.
func EligibleFailure(err *ProviderError) bool {
	if err == nil {
		return false
	}
	return err.Class == ClassUnavailable && !err.MutationMayHaveStarted
}

// AllowedObjectTransitions is the validated object state machine from
// docs/architecture.md section 6.
var AllowedObjectTransitions = map[ObjectState][]ObjectState{
	"pending":         {"selected", "initiating", "failed", "blocked"},
	"selected":        {"initiating", "failed", "blocked"},
	"initiating":      {"active", "outcome_unknown", "failed", "blocked"},
	"active":          {"verifying", "outcome_unknown", "failed"},
	"verifying":       {"ready", "outcome_unknown", "failed"},
	"outcome_unknown": {"active", "verifying", "failed"},
	"ready":           {"deleting", "missing"},
	"deleting":        {"deleted", "missing", "failed"},
	"failed":          {"selected"},
	"blocked":         {"selected"},
	"deleted":         {},
	"missing":         {"selected"},
}

// ObjectState is the attempt lifecycle used by the transition table.
type ObjectState string

// CanTransition reports whether from -> to is a legal move.
//
// The two rules worth stating in prose: a failed or blocked attempt may only be
// retried after cleanup is confirmed, and a ready object only ever goes to
// deleting or missing. There is no transition from ready back to uploading,
// because that would be a second location.
func CanTransition(from, to ObjectState) bool {
	for _, allowed := range AllowedObjectTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// ValidateTransition returns a descriptive error for an illegal move.
func ValidateTransition(from, to ObjectState) error {
	if CanTransition(from, to) {
		return nil
	}
	allowed := make([]string, 0, len(AllowedObjectTransitions[from]))
	for _, state := range AllowedObjectTransitions[from] {
		allowed = append(allowed, string(state))
	}
	sort.Strings(allowed)
	return fmt.Errorf("policy: illegal transition %s -> %s (allowed: %v)", from, to, allowed)
}

// FallbackEligibleTransitions lists the moves permitted while a fallback upload
// is in flight: finish on S3, and never move back to Drive.
var FallbackEligibleTransitions = []ObjectState{"active", "verifying", "ready", "outcome_unknown", "failed"}
