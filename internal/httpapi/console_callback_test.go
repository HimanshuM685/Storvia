package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// consoleRouter builds the production route table around a console.
//
// It deliberately does not hand-build the routes it tests. A test that re-declared the
// paths would pass even after the real router had lost them, which is exactly the
// failure these tests exist to catch.
func consoleRouter(console *Console) http.Handler {
	return Router(Config{Console: console, RateLimit: 1000, RateInterval: time.Minute})
}

// TestOAuthCallbackSurvivesThePanelMove is the regression test for a real outage.
//
// The console replaced a panel served at /admin, and the OAuth callback path moved to
// /console/oauth/callback. That broke sign-in for anyone whose Google Cloud project
// still had the old URI registered: Google redirected the browser to a path the server
// no longer served, and the person saw a 404 instead of a session.
//
// It is an easy mistake to repeat because the failure is invisible until someone tries
// to log in, and the callback URI lives in Google Cloud rather than in this repository.
func TestOAuthCallbackSurvivesThePanelMove(t *testing.T) {
	console := newTestConsole(t, nil, &stubRouting{})

	router := consoleRouter(console)

	for _, path := range []string{
		"/console/oauth/callback?code=x&state=y",
		// The old, registered path. This one must keep working.
		"/admin/oauth/callback?code=x&state=y",
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code == http.StatusNotFound {
			t.Errorf("%s = 404; google redirects here after consent, so a 404 means "+
				"nobody can sign in", path)
		}
		// A fake code is rejected, which is correct. What matters is that the request
		// reached the callback handler and was handled rather than 404'd.
		if rec.Code != http.StatusFound {
			t.Errorf("%s = %d, want a redirect to the console", path, rec.Code)
		}
		if got := rec.Header().Get("Location"); !strings.HasPrefix(got, "/console") {
			t.Errorf("%s redirected to %q, want the console", path, got)
		}
	}
}

// TestOldPanelRootRedirectsToTheConsole. A stale bookmark or an old link should land
// on the console rather than a 404. It is a pointer, not a second panel.
func TestOldPanelRootRedirectsToTheConsole(t *testing.T) {
	router := consoleRouter(newTestConsole(t, nil, &stubRouting{}))

	for _, path := range []string{"/admin", "/admin/"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusMovedPermanently {
			t.Errorf("%s = %d, want 301", path, rec.Code)
		}
		if got := rec.Header().Get("Location"); got != "/console" {
			t.Errorf("%s redirected to %q, want /console", path, got)
		}
	}
}

// TestDeclinedConsentIsNotAnError. Pressing Back on Google's consent screen is a normal
// outcome, and it should say so rather than reporting a failed sign-in.
func TestDeclinedConsentIsNotAnError(t *testing.T) {
	console := newTestConsole(t, nil, &stubRouting{})
	rec := httptest.NewRecorder()

	console.Callback(rec, httptest.NewRequest(http.MethodGet,
		"/console/oauth/callback?error=access_denied", nil))

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want a redirect", rec.Code)
	}
	location := rec.Header().Get("Location")
	if !strings.Contains(location, "access_denied") {
		t.Errorf("Location = %q, want the google reason surfaced to the operator", location)
	}
}

// TestCallbackWithNoCodeDoesNot404.
func TestCallbackWithNoCodeDoesNot404(t *testing.T) {
	console := newTestConsole(t, nil, &stubRouting{})
	rec := httptest.NewRecorder()

	console.Callback(rec, httptest.NewRequest(http.MethodGet, "/console/oauth/callback", nil))

	if rec.Code != http.StatusFound {
		t.Errorf("a callback with no code = %d, want a redirect with an explanation", rec.Code)
	}
}
