package drive

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/shovan04/storvia/internal/apperr"
	"github.com/shovan04/storvia/internal/persistence"
)

// AuthorizeURL builds the Google consent URL for a tenant.
//
// The state parameter is mandatory and bound to the browser session. Without it a
// stolen authorization code could be redeemed by another session, which is the
// classic OAuth CSRF/account-mix-up attack.
func (c *Client) AuthorizeURL(state, browserBinding string) (string, error) {
	if !c.Configured() {
		return "", ErrNotConfigured
	}
	if state == "" {
		return "", errors.New("drive: oauth state is required")
	}

	query := url.Values{}
	query.Set("client_id", c.cfg.Credentials.ClientID)
	// offline is required: the backend acts while the user is absent.
	query.Set("access_type", "offline")
	// prompt=consent guarantees a refresh token even for a previously authorized
	// account, which otherwise silently omits it.
	query.Set("prompt", "consent")
	query.Set("include_granted_scopes", "true")
	query.Set("response_type", "code")
	query.Set("scope", scopesParam(c.cfg.Scopes))
	query.Set("redirect_uri", c.cfg.Credentials.RedirectURI)
	// The state is a random value whose digest is bound to the browser binding.
	query.Set("state", state+":"+browserBinding)

	return authorizeURL + "?" + query.Encode(), nil
}

// ExchangeResult is what a successful callback yields.
type ExchangeResult struct {
	Connection Connection
	// RefreshTokenReturned is false when Google omitted a refresh token on a
	// re-consent, in which case the existing stored token must be preserved.
	RefreshTokenReturned bool
}

// Exchange completes the code-for-token exchange and stores the connection.
//
// A returned error is safe to show a user: it names the problem (declined,
// invalid_grant, network) without including the code, the token, or the raw
// provider body.
func (c *Client) Exchange(ctx context.Context, scope Scope, code, state, browserBinding string) (ExchangeResult, error) {
	if !c.Configured() {
		return ExchangeResult{}, ErrNotConfigured
	}
	if !c.states.Consume(state, browserBinding) {
		// A wrong or replayed state is rejected outright. Guessing is not
		// feasible because the value is 256 bits of CSPRNG output.
		return ExchangeResult{}, apperrAuthentication("the authorization request could not be verified; start the connection again")
	}

	form := url.Values{}
	form.Set("code", code)
	form.Set("client_id", c.cfg.Credentials.ClientID)
	form.Set("client_secret", c.cfg.Credentials.ClientSecret)
	// The redirect URI must match the authorization request byte for byte,
	// including a trailing slash.
	form.Set("redirect_uri", c.cfg.Credentials.RedirectURI)
	form.Set("grant_type", "authorization_code")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenEndpoint,
		strings.NewReader(form.Encode()))
	if err != nil {
		return ExchangeResult{}, fmt.Errorf("drive: build token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return ExchangeResult{}, fmt.Errorf("drive: token endpoint unreachable: %w", err)
	}
	defer drainAndClose(resp)

	body, _ := readBounded(resp.Body)
	if resp.StatusCode >= 400 {
		return ExchangeResult{}, classifyTokenError(resp.StatusCode, body)
	}

	values, err := parseFormURL(body)
	if err != nil {
		return ExchangeResult{}, err
	}

	accessToken := values.Get("access_token")
	if accessToken == "" {
		return ExchangeResult{}, errors.New("drive: the token response contained no access token")
	}
	// The refresh token is optional in the response. Presenting one that was
	// omitted would silently revoke the user's grant.
	refreshToken := values.Get("refresh_token")
	if refreshToken == "" {
		if existing, err := c.conns.Connection(ctx, persistence.Scope{TenantID: scope.TenantID, UserID: scope.UserID}); err == nil && existing.RefreshToken != "" {
			refreshToken = existing.RefreshToken
		} else {
			return ExchangeResult{}, errors.New("drive: google returned no refresh token and none is stored")
		}
	}

	subject, err := c.fetchSubject(ctx, accessToken)
	if err != nil {
		return ExchangeResult{}, err
	}

	connection := Connection{
		TenantID:     scope.TenantID,
		UserID:       scope.UserID,
		Subject:      subject,
		RefreshToken: refreshToken,
		Status:       StatusConnected,
	}
	if err := c.conns.SaveConnection(ctx, connection); err != nil {
		return ExchangeResult{}, fmt.Errorf("drive: save connection: %w", err)
	}

	ttl, err := time.ParseDuration(values.Get("expires_in") + "s")
	if err != nil || ttl <= 0 {
		ttl = time.Hour
	}
	// Cache for slightly less than the real lifetime so a token is never handed
	// out after it expires.
	if err := c.tokens.Set(ctx, connection.ID, accessToken, ttl-c.cfg.RefreshSkew); err != nil {
		c.log.WarnContext(ctx, "could not cache the initial access token", "error", err.Error())
	}

	return ExchangeResult{Connection: connection, RefreshTokenReturned: values.Get("refresh_token") != ""}, nil
}

// Scope carries the authenticated tenant into the OAuth exchange.
type Scope struct {
	TenantID string
	UserID   string
}

// classifyTokenError maps a token endpoint failure onto a user-safe error.
func classifyTokenError(status int, body []byte) error {
	var payload struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(body, &payload)

	switch payload.Error {
	case "invalid_grant":
		// The grant is gone. Reconnect, never fall back to S3.
		return apperrAuthentication("the google authorization expired or was revoked; reconnect your drive")
	case "access_denied":
		return apperrAuthentication("authorization was declined")
	case "invalid_client":
		return errors.New("drive: the configured google client credentials were rejected")
	case "unauthorized_client":
		return errors.New("drive: this google client is not authorized for the requested grant type")
	}
	if status >= 500 {
		return fmt.Errorf("drive: the google token service is unavailable (status %d)", status)
	}
	return fmt.Errorf("drive: the token exchange was rejected (status %d)", status)
}

// fetchSubject reads the authenticated user's subject identifier.
func (c *Client) fetchSubject(ctx context.Context, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://openidconnect.googleapis.com/v1/userinfo", nil)
	if err != nil {
		return "", fmt.Errorf("drive: build userinfo request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)

	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("drive: userinfo unreachable: %w", err)
	}
	defer drainAndClose(resp)

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("drive: could not identify the google account (status %d)", resp.StatusCode)
	}
	body, _ := readBounded(resp.Body)
	var payload struct {
		Sub string `json:"sub"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Sub == "" {
		return "", errors.New("drive: the userinfo response did not contain a subject")
	}
	return payload.Sub, nil
}

// revoke invalidates a refresh token at Google.
func (c *Client) revoke(ctx context.Context, refreshToken string) error {
	form := url.Values{}
	form.Set("token", refreshToken)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, revokeEndpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("drive: build revoke request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.cfg.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("drive: revoke request failed: %w", err)
	}
	defer drainAndClose(resp)

	if resp.StatusCode >= 400 && resp.StatusCode != http.StatusBadRequest {
		return fmt.Errorf("drive: revoke returned status %d", resp.StatusCode)
	}
	return nil
}

// readBounded reads at most 64 KiB from r.
//
// The cap matters on error paths: an unbounded read of a hostile or broken
// response body is a memory exhaustion vector.
func readBounded(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	buf := make([]byte, 0, 4096)
	chunk := make([]byte, 4096)
	for len(buf) < 64<<10 {
		n, err := r.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
		}
		if err != nil {
			if errors.Is(err, context.Canceled) {
				return buf, nil
			}
			break
		}
	}
	return buf, nil
}

// apperrAuthentication maps a consent failure onto the shared error vocabulary so
// the HTTP layer renders it with the right status and problem type.
func apperrAuthentication(detail string) error {
	return apperr.ConsentRequired(detail)
}

// StateStore holds pending OAuth state values.
//
// A state value is single use. Consuming it removes it, which is what makes a
// replayed callback fail even if it arrives before the legitimate one.
type StateStore struct {
	mu      sync.Mutex
	entries map[string]stateEntry
	ttl     time.Duration
	now     func() time.Time
}

type stateEntry struct {
	// binding is the digest of the browser session that started the flow.
	binding []byte
	expires time.Time
}

// NewStateStore builds a state store with the given lifetime.
func NewStateStore(ttl time.Duration) *StateStore {
	if ttl <= 0 {
		// The user has to click through Google's consent screen in this time.
		ttl = 10 * time.Minute
	}
	return &StateStore{entries: make(map[string]stateEntry), ttl: ttl, now: time.Now}
}

// Put records a pending authorization for a browser binding.
func (s *StateStore) Put(state, browserBinding string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	s.entries[state] = stateEntry{binding: hashSecret(browserBinding), expires: s.now().Add(s.ttl)}
}

// Consume validates and removes a state value.
//
// It returns false for an unknown value, an expired value, or a value whose
// browser binding does not match. All three failures are indistinguishable to the
// caller, which prevents probing for valid states.
func (s *StateStore) Consume(state, browserBinding string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.entries[state]
	if !ok {
		return false
	}
	// Delete before validating the binding so a wrong-binding attempt still burns
	// the state rather than leaving it available for a retry.
	delete(s.entries, state)

	if s.now().After(entry.expires) {
		return false
	}
	return constantTimeEqual(string(entry.binding), string(hashSecret(browserBinding)))
}

// Len reports the number of pending states, for diagnostics.
func (s *StateStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweepLocked()
	return len(s.entries)
}

func (s *StateStore) sweepLocked() {
	now := s.now()
	for state, entry := range s.entries {
		if now.After(entry.expires) {
			delete(s.entries, state)
		}
	}
}
