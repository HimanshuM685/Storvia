package apperr

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestKindStatusMappingIsTotal(t *testing.T) {
	// Every declared kind must have a status. A missing entry would silently fall
	// back to 500 and hide a client mistake as a server fault.
	kinds := []Kind{
		KindValidation, KindAuthentication, KindAuthorization, KindNotFound,
		KindConflict, KindQuota, KindRateLimited, KindUnavailable, KindUncertain,
		KindProviderNotConfigured, KindConsentRequired, KindInternal,
	}
	for _, kind := range kinds {
		err := New(kind, "Title", "Detail")
		status := err.Status()
		if status == 0 {
			t.Errorf("kind %q has no HTTP status", kind)
		}
		if status == http.StatusInternalServerError && kind != KindInternal {
			t.Errorf("kind %q falls back to 500", kind)
		}
	}
}

// TestUnknownKindIsInternal checks the fail-safe for a kind added without a
// status entry.
func TestUnknownKindIsInternal(t *testing.T) {
	err := New(Kind("something_new"), "Title", "Detail")
	if err.Status() != http.StatusInternalServerError {
		t.Errorf("an unmapped kind = %d, want 500", err.Status())
	}
	if err.ProblemType() != "https://storvia.dev/problems/something_new" {
		t.Errorf("ProblemType = %q", err.ProblemType())
	}
}

func TestWrapRetainsTheCauseAndDoesNotExposeIt(t *testing.T) {
	cause := errors.New("dial tcp 10.0.0.5:5432: connection refused")
	err := Wrap(cause, KindInternal, "Internal error", "an unexpected error occurred")

	if !errors.Is(err, cause) {
		t.Error("the cause must remain reachable through errors.Is")
	}
	// The client-facing detail must be the safe one, not the cause.
	if err.Detail == cause.Error() {
		t.Error("the detail must not be the raw cause")
	}
}

func TestKindOfFollowsTheWrapChain(t *testing.T) {
	base := Quota("drive is full")
	wrapped := fmt.Errorf("while creating upload: %w", base)
	doubleWrapped := fmt.Errorf("handling request: %w", wrapped)

	if KindOf(doubleWrapped) != KindQuota {
		t.Errorf("KindOf = %q, want quota", KindOf(doubleWrapped))
	}
}

func TestKindOfCoercesUnknownErrors(t *testing.T) {
	// A bare error from a package that forgot to use apperr must not be treated as
	// anything client-safe.
	cases := []error{
		nil,
		errors.New("plain"),
		fmt.Errorf("wrapped: %w", errors.New("plain")),
	}
	for _, err := range cases {
		if KindOf(err) != KindInternal {
			t.Errorf("KindOf(%v) = %q, want internal", err, KindOf(err))
		}
	}
}

func TestWithFieldDoesNotMutateTheReceiver(t *testing.T) {
	base := Validation("bad request")
	first := base.WithField("name", "required")
	second := base.WithField("size", "too large")

	if len(base.Fields) != 0 {
		t.Errorf("the base error was mutated: %v", base.Fields)
	}
	if _, ok := first.Fields["size"]; ok {
		t.Error("the copies share a map")
	}
	if first.Fields["name"] != "required" || second.Fields["size"] != "too large" {
		t.Errorf("fields are wrong: %v / %v", first.Fields, second.Fields)
	}
}

// TestConstructorsUseTheirOwnKind guards against a copy-paste mistake in the
// constructor table, which would map the wrong problem type to the wrong status.
func TestConstructorsUseTheirOwnKind(t *testing.T) {
	tests := []struct {
		constructor func(string) *Error
		want        Kind
	}{
		{Validation, KindValidation},
		{Authentication, KindAuthentication},
		{Authorization, KindAuthorization},
		{NotFound, KindNotFound},
		{Conflict, KindConflict},
		{Quota, KindQuota},
		{RateLimited, KindRateLimited},
		{Unavailable, KindUnavailable},
		{Uncertain, KindUncertain},
		{ConsentRequired, KindConsentRequired},
		{ProviderNotConfigured, KindProviderNotConfigured},
		{Internal, KindInternal},
	}
	for _, tc := range tests {
		t.Run(string(tc.want), func(t *testing.T) {
			err := tc.constructor("detail")
			if err.Kind != tc.want {
				t.Errorf("kind = %q, want %q", err.Kind, tc.want)
			}
			if err.Title == "" {
				t.Error("every constructor must set a safe title")
			}
		})
	}
}

// TestInternalDetailIsGeneric keeps the internal message free of specifics.
func TestInternalDetailIsGeneric(t *testing.T) {
	if Internal("an unexpected error occurred").Detail != "an unexpected error occurred" {
		t.Error("the internal detail must be generic")
	}
}

// TestUncertainIsAnErrorStatus documents the choice: an unknown outcome is
// reported with an error status so a client cannot mistake it for a completed
// upload. It is retryable, but it is still a failure to report.
func TestUncertainIsAnErrorStatus(t *testing.T) {
	err := Uncertain("the provider result is not yet known")
	if err.Status() != http.StatusConflict {
		t.Errorf("status = %d, want 409 Conflict", err.Status())
	}
}
