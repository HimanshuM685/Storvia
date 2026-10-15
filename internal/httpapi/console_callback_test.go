package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

// consoleRouter builds the production route table around a console.
//
// It deliberately does not hand-build the routes it tests. A test that re-declared the
// paths would pass even after the real router had lost them, which is exactly the
// failure these tests exist to catch.
func consoleRouter(console *Console) http.Handler {
	router := Router(Config{Console: console, RateLimit: 1000, RateInterval: time.Minute})
	return router
}

// testConsoleAssets is a stand-in for the built Next.js export.
//
// The Go tests must not require Node to have been run: a test suite that only passes
// after a frontend build is a test suite that gets skipped.
func testConsoleAssets() fstest.MapFS {
	return fstest.MapFS{
		"index.html":          {Data: []byte("<!doctype html><title>Storvia console</title><div id=\"root\"></div>")},
		"_next/static/app.js": {Data: []byte("console.log(1)")},
	}
}

// TestTheRegisteredCallbackPathStillWorks is the test that matters most here, and it
// exists because I got this wrong once.
//
// I removed the /admin routes and repointed drive.admin_redirect_uri at
// /console/oauth/callback, on the reasoning that the old panel was gone and the
// configuration should say so. That broke sign-in completely: Google rejects an
// unregistered redirect_uri with `redirect_uri_mismatch` before the user is ever shown a
// consent screen, and the URI registered in Google Cloud could not be changed from here.
//
// The callback therefore has to stay where it is registered. This asserts the handler
// still answers there, against the production route table.
func TestTheRegisteredCallbackPathStillWorks(t *testing.T) {
	router := consoleRouter(newTestConsole(t, nil, &stubRouting{}))

	// The registered path first, then the console's own. Both must reach the handler.
	for _, path := range []string{
		"/admin/oauth/callback?code=x&state=y",
		"/console/oauth/callback?code=x&state=y",
	} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code == http.StatusNotFound {
			t.Errorf("%s = 404; google redirects here after consent, so a 404 means "+
				"nobody can sign in", path)
		}
		if rec.Code != http.StatusFound {
			t.Errorf("%s = %d, want a redirect to the console", path, rec.Code)
		}
		if got := rec.Header().Get("Location"); !strings.HasPrefix(got, "/console") {
			t.Errorf("%s redirected to %q, want the console: the handler verifies, issues "+
				"the session, and hands the browser back", path, got)
		}
	}
}

// TestTheOldPanelServesNoInterface is the half that actually removes the old panel.
//
// Keeping the callback is not the same as keeping the panel. Nothing renders at /admin:
// it redirects, and there is no document, no form, and no server-rendered HTML anywhere
// under it. Only the OAuth return trip is still answered there.
func TestTheOldPanelServesNoInterface(t *testing.T) {
	router := consoleRouter(newTestConsole(t, nil, &stubRouting{}))

	for _, path := range []string{"/admin", "/admin/"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

		if rec.Code != http.StatusMovedPermanently {
			t.Errorf("%s = %d, want 301", path, rec.Code)
		}
		if got := rec.Header().Get("Location"); got != "/console/" {
			t.Errorf("%s redirected to %q, want /console/", path, got)
		}
		if body := rec.Body.String(); strings.Contains(body, "<html") ||
			strings.Contains(body, "Sign in with Google") {
			t.Errorf("%s served interface markup; the old panel must be gone", path)
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
