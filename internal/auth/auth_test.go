package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const testSigningKey = "test-signing-key-that-is-definitely-long-enough-32"

// newTestIssuer builds an issuer with a long enough key.
func newTestIssuer(t *testing.T) *Issuer {
	t.Helper()
	issuer, err := NewIssuer(IssuerOptions{
		SigningKey: testSigningKey,
		CookieName: "storvia_session",
		TTL:        time.Hour,
	})
	if err != nil {
		t.Fatalf("NewIssuer: %v", err)
	}
	return issuer
}

// testIdentity is a valid tenant/user pair.
func testIdentity() Identity {
	return Identity{TenantID: "tenant-a", UserID: "user-1", Email: "user@example.com"}
}

func TestNewIssuerRejectsShortKeys(t *testing.T) {
	// A short HMAC key is brute-forceable, so this is a hard stop.
	for _, key := range []string{"", "short", strings.Repeat("a", 31)} {
		if _, err := NewIssuer(IssuerOptions{SigningKey: key}); err == nil {
			t.Errorf("key of length %d must be rejected", len(key))
		}
	}
	if _, err := NewIssuer(IssuerOptions{SigningKey: strings.Repeat("a", 32)}); err != nil {
		t.Errorf("a 32-character key must be accepted: %v", err)
	}
}

func TestIssueAndVerifyRoundTrip(t *testing.T) {
	issuer := newTestIssuer(t)
	identity := testIdentity()

	token, session, err := issuer.Issue(identity, "sess-1", time.Hour)
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	if session.Identity.TenantID != identity.TenantID {
		t.Errorf("session tenant = %q, want %q", session.Identity.TenantID, identity.TenantID)
	}

	verified, err := issuer.Verify(token, time.Now())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if verified.ID != "sess-1" {
		t.Errorf("session id = %q, want sess-1", verified.ID)
	}
}

func TestIssueRequiresAValidIdentity(t *testing.T) {
	issuer := newTestIssuer(t)
	for _, identity := range []Identity{{}, {TenantID: "t"}, {UserID: "u"}} {
		if _, _, err := issuer.Issue(identity, "s", time.Hour); err == nil {
			t.Errorf("issuing a session for %+v must fail", identity)
		}
	}
}

func TestVerifyRejectsTamperedTokens(t *testing.T) {
	issuer := newTestIssuer(t)
	token, _, err := issuer.Issue(testIdentity(), "sess-1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name  string
		token string
	}{
		{"empty", ""},
		{"no separators", "abcdef"},
		{"truncated", token[:len(token)-5]},
		{"payload swapped", "sess-2." + strings.SplitN(token, ".", 3)[1]},
		{"signature removed", strings.Join(strings.Split(token, ".")[:2], ".")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := issuer.Verify(tc.token, time.Now()); err == nil {
				t.Error("a tampered token must not verify")
			}
		})
	}
}

func TestVerifyRejectsExpiredTokens(t *testing.T) {
	issuer := newTestIssuer(t)
	token, _, err := issuer.Issue(testIdentity(), "sess-1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	// The verifier takes its clock as a parameter, so expiry is exercised by
	// advancing time rather than by sleeping.
	if _, err := issuer.Verify(token, time.Now().Add(2*time.Hour)); err == nil {
		t.Error("an expired token must not verify")
	}
}

// TestIssueFallsBackToTheIssuerTTLForNonPositiveTTL documents that a
// non-positive lifetime uses the issuer default rather than minting an
// already-expired token.
func TestIssueFallsBackToTheIssuerTTLForNonPositiveTTL(t *testing.T) {
	issuer := newTestIssuer(t)
	token, session, err := issuer.Issue(testIdentity(), "sess-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if !session.ExpiresAt.After(time.Now()) {
		t.Error("a non-positive TTL must fall back to the issuer TTL, not mint an expired session")
	}
	if _, err := issuer.Verify(token, time.Now()); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

// TestVerifyDoesNotDistinguishFailureReasons keeps the endpoint from becoming an
// oracle for probing tokens.
func TestVerifyDoesNotDistinguishFailureReasons(t *testing.T) {
	issuer := newTestIssuer(t)
	valid, _, err := issuer.Issue(testIdentity(), "sess-1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	_, validErr := issuer.Verify(valid+"tampered", time.Now())
	_, expiredErr := issuer.Verify(valid, time.Now().Add(2*time.Hour))

	if validErr == nil || expiredErr == nil {
		t.Fatalf("both must fail: %v / %v", validErr, expiredErr)
	}
	if validErr.Error() != expiredErr.Error() {
		t.Errorf("error messages differ, which leaks which check failed: %q vs %q",
			validErr.Error(), expiredErr.Error())
	}
}

func TestTokenFromAnotherIssuerIsRejected(t *testing.T) {
	first := newTestIssuer(t)
	second, err := NewIssuer(IssuerOptions{SigningKey: "a-completely-different-signing-key-of-32chars"})
	if err != nil {
		t.Fatal(err)
	}

	token, _, err := first.Issue(testIdentity(), "sess-1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := second.Verify(token, time.Now()); err == nil {
		t.Error("a token signed by a different key must not verify")
	}
}

func TestCookieHelpers(t *testing.T) {
	issuer := newTestIssuer(t)
	token, session, err := issuer.Issue(testIdentity(), "sess-1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}

	recorder := httptest.NewRecorder()
	issuer.SetSessionCookie(recorder, token, session.ExpiresAt)

	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	cookie := cookies[0]
	if !cookie.HttpOnly {
		t.Error("the session cookie must be HttpOnly so script cannot read it")
	}
	if cookie.SameSite != http.SameSiteLaxMode {
		t.Errorf("SameSite = %v, want Lax", cookie.SameSite)
	}

	// The value must round-trip into a request.
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(cookie)
	if got := issuer.SessionCookieValue(request); got != token {
		t.Error("the session cookie did not round-trip")
	}
}

func TestClearSessionCookieExpiresIt(t *testing.T) {
	issuer := newTestIssuer(t)
	recorder := httptest.NewRecorder()
	issuer.ClearSessionCookie(recorder)

	cookies := recorder.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	if cookies[0].MaxAge >= 0 {
		t.Errorf("MaxAge = %d, want a negative value so the browser drops it", cookies[0].MaxAge)
	}
}

func TestSessionCookieMissingReturnsEmpty(t *testing.T) {
	issuer := newTestIssuer(t)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	if got := issuer.SessionCookieValue(request); got != "" {
		t.Errorf("SessionCookieValue with no cookie = %q, want empty", got)
	}
}

func TestBearerTokenParsing(t *testing.T) {
	tests := []struct {
		header string
		want   string
	}{
		{"Bearer abc123", "abc123"},
		{"bearer abc123", "abc123"},
		{"BEARER abc123", "abc123"},
		{"Bearer  abc123 ", "abc123"},
		{"Bearer", ""},
		{"Basic abc123", ""},
		{"", ""},
	}
	for _, tc := range tests {
		t.Run(tc.header, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			if tc.header != "" {
				request.Header.Set("Authorization", tc.header)
			}
			if got := BearerToken(request); got != tc.want {
				t.Errorf("BearerToken(%q) = %q, want %q", tc.header, got, tc.want)
			}
		})
	}
}

func TestIdentityContextRoundTrip(t *testing.T) {
	identity := testIdentity()
	ctx := WithIdentity(context.Background(), identity)

	got, ok := IdentityFrom(ctx)
	if !ok {
		t.Fatal("IdentityFrom reported the identity missing")
	}
	if got.TenantID != identity.TenantID || got.UserID != identity.UserID {
		t.Errorf("got %+v, want %+v", got, identity)
	}

	if _, ok := IdentityFrom(context.Background()); ok {
		t.Error("an empty context must report no identity")
	}
}

func TestIdentityValid(t *testing.T) {
	valid := []Identity{
		{TenantID: "t", UserID: "u"},
		{TenantID: "t", UserID: "u", Email: "x"},
	}
	for _, identity := range valid {
		if !identity.Valid() {
			t.Errorf("%+v should be valid", identity)
		}
	}
	invalid := []Identity{{}, {TenantID: "t"}, {UserID: "u"}}
	for _, identity := range invalid {
		if identity.Valid() {
			t.Errorf("%+v should be invalid", identity)
		}
	}
}

// TestMemorySessions covers the development session store.
func TestMemorySessions(t *testing.T) {
	ctx := context.Background()
	sessions := NewMemorySessions(10)

	identity := testIdentity()
	session := Session{
		ID:        "sess-1",
		Identity:  identity,
		ExpiresAt: time.Now().Add(time.Hour),
	}
	if err := sessions.StoreSession(ctx, session); err != nil {
		t.Fatalf("StoreSession: %v", err)
	}

	got, ok := sessions.LookupSession(ctx, "sess-1")
	if !ok {
		t.Fatal("LookupSession reported the session missing")
	}
	if got.TenantID != identity.TenantID {
		t.Errorf("tenant = %q, want %q", got.TenantID, identity.TenantID)
	}

	if _, ok := sessions.LookupSession(ctx, "absent"); ok {
		t.Error("an unknown session must not resolve")
	}

	sessions.DeleteSession(ctx, "sess-1")
	if _, ok := sessions.LookupSession(ctx, "sess-1"); ok {
		t.Error("a deleted session must not resolve; this is what sign-out relies on")
	}
}

// TestMemorySessionsStayBounded keeps a burst of sign-ins from growing the map
// without limit.
func TestMemorySessionsStayBounded(t *testing.T) {
	ctx := context.Background()
	sessions := NewMemorySessions(10)

	for i := 0; i < 100; i++ {
		session := Session{
			ID:        string(rune('a'+i%26)) + string(rune('a'+i/26)),
			Identity:  testIdentity(),
			ExpiresAt: time.Now().Add(time.Duration(i) * time.Minute),
		}
		if err := sessions.StoreSession(ctx, session); err != nil {
			t.Fatal(err)
		}
	}

	sessions.mu.RLock()
	count := len(sessions.entries)
	sessions.mu.RUnlock()
	if count > 10 {
		t.Errorf("store holds %d sessions, want at most 10", count)
	}
}

// interface conformance.
var _ Lookup = (*MemorySessions)(nil)
