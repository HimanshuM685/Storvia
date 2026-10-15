package drive

import (
	"net/url"
	"testing"
)

// TestAuthorizeEndpointIsNotUnderTheOAuthAPIBase is the regression guard for a bug
// this repository shipped with: the authorization URL was derived from the same base
// as the token endpoint, producing oauth2.googleapis.com/auth.
//
// That path does not exist. Google answers it with a 404 error page, so connecting an
// account failed with what looked like a misconfiguration and no log line pointing at
// the real cause. Nothing caught it because every OAuth test asserted against a stub
// that accepted the URL.
//
// The guard is about the *shape* of the endpoint, so it fails loudly if anyone
// re-derives these three URLs from a single base again.
func TestAuthorizeEndpointIsNotUnderTheOAuthAPIBase(t *testing.T) {
	parsed, err := url.Parse(authorizeURL)
	if err != nil {
		t.Fatalf("authorizeURL does not parse: %v", err)
	}

	if parsed.Host != "accounts.google.com" {
		t.Errorf("authorization host = %q, want accounts.google.com. Google serves the "+
			"consent page there, not under oauth2.googleapis.com, which 404s.", parsed.Host)
	}
	if parsed.Path != "/o/oauth2/v2/auth" {
		t.Errorf("authorization path = %q, want /o/oauth2/v2/auth", parsed.Path)
	}
	if parsed.Scheme != "https" {
		t.Errorf("authorization scheme = %q, want https", parsed.Scheme)
	}

	// The two hosts are the whole point of the split. Assert the token and revoke
	// endpoints are still on the API host, so they cannot be dragged along with it.
	for _, tc := range []struct{ name, endpoint, wantHost string }{
		{"token", tokenEndpoint, "oauth2.googleapis.com"},
		{"revoke", revokeEndpoint, "oauth2.googleapis.com"},
	} {
		parsed, err := url.Parse(tc.endpoint)
		if err != nil {
			t.Fatalf("%s endpoint does not parse: %v", tc.name, err)
		}
		if parsed.Host != tc.wantHost {
			t.Errorf("%s endpoint host = %q, want %q", tc.name, parsed.Host, tc.wantHost)
		}
	}
}

// TestAuthorizeURLGoesToGoogle is the end-to-end version of the same guard, through
// the real builder rather than the constant. It is the assertion that would have
// caught the original bug.
func TestAuthorizeURLGoesToGoogle(t *testing.T) {
	client := newTestClient(t, true)

	raw, err := client.AuthorizeURLWithRedirect("state-token", "binding", "http://127.0.0.1:8080/admin/oauth/callback")
	if err != nil {
		t.Fatalf("AuthorizeURLWithRedirect: %v", err)
	}

	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("built authorization URL does not parse: %v", err)
	}
	if parsed.Scheme != "https" || parsed.Host != "accounts.google.com" {
		t.Fatalf("authorization goes to %s://%s, want https://accounts.google.com", parsed.Scheme, parsed.Host)
	}
	if parsed.Path != "/o/oauth2/v2/auth" {
		t.Errorf("authorization path = %q, want /o/oauth2/v2/auth", parsed.Path)
	}
}
