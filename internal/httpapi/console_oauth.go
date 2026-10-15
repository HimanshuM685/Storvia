package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"time"

	"github.com/shovan04/storvia/internal/persistence"
)

var (
	errMissingFlowCookie = errors.New("console: the sign-in flow cookie is missing")
	errMissingCode       = errors.New("console: google returned no authorization code")
)

// consoleRedirect is where the browser lands after a console OAuth round trip.
const consoleRedirect = "/console"

// SignIn starts the Google OAuth flow for the console.
//
// The browser binding is stored in a cookie so the callback can prove the flow that
// started in this browser is the one returning. Without it, a second person could
// complete someone else's consent by feeding a state value into the callback.
func (c *Console) SignIn(w http.ResponseWriter, r *http.Request) {
	if !c.enabled(w) {
		return
	}

	authorizeURL, err := c.beginConnect(w, r)
	if err != nil {
		c.log.WarnContext(r.Context(), "console: could not start sign-in", "error", err.Error())
		http.Redirect(w, r, "/console?error="+url.QueryEscape(
			"Could not start the Google sign-in."), http.StatusFound)
		return
	}
	http.Redirect(w, r, authorizeURL, http.StatusFound)
}

// Callback completes the Google OAuth flow and issues the console session.
func (c *Console) Callback(w http.ResponseWriter, r *http.Request) {
	if !c.enabled(w) {
		return
	}

	if oauthErr := r.URL.Query().Get("error"); oauthErr != "" {
		// A declined consent screen is not a failure of this service, and it is
		// reported as a normal outcome rather than an error page.
		http.Redirect(w, r, "/console?error="+url.QueryEscape(
			"Google did not grant access: "+oauthErr), http.StatusFound)
		return
	}

	if err := c.completeConnect(w, r); err != nil {
		c.log.WarnContext(r.Context(), "console: sign-in callback failed", "error", err.Error())
		http.Redirect(w, r, "/console?error="+url.QueryEscape(
			"The Google sign-in could not be completed."), http.StatusFound)
		return
	}
	http.Redirect(w, r, consoleRedirect, http.StatusFound)
}

// SignOut ends the console session.
//
// It clears the cookie and revokes the server-side session. Revoking matters: a
// cleared cookie alone leaves a still-valid token in the store, which is harmless
// until the cookie is somehow recovered.
func (c *Console) SignOut(w http.ResponseWriter, r *http.Request) {
	token := c.issuer.SessionCookieValue(r)
	if token != "" {
		if verified, err := c.issuer.Verify(token, time.Now()); err == nil {
			if revoker, ok := c.sessions.(interface {
				RevokeSession(ctx context.Context, sessionID string) error
			}); ok {
				if err := revoker.RevokeSession(r.Context(), verified.ID); err != nil {
					c.log.WarnContext(r.Context(), "console: could not revoke the session",
						"error", err.Error())
				}
			}
		}
	}
	c.issuer.ClearSessionCookie(w)
	http.Redirect(w, r, consoleRedirect, http.StatusFound)
}

// EnsureFolder creates the app-managed Storvia/ folder if it is absent.
func (c *Console) EnsureFolder(w http.ResponseWriter, r *http.Request) {
	scope, ok := c.requireSession(w, r)
	if !ok {
		return
	}
	if c.folders == nil {
		c.writeJSON(w, http.StatusNotImplemented, map[string]string{
			"error": "This deployment cannot create the Drive folder.",
		})
		return
	}
	if err := c.folders.EnsureRootFolder(r.Context(), scope); err != nil {
		c.fail(w, err)
		return
	}
	c.writeJSON(w, http.StatusOK, map[string]bool{"created": true})
}

// Disconnect ends the Drive connection without ending the console session.
//
// Signing out of Google and disconnecting Drive are different things: an operator may
// want to test a fresh connection while staying signed in to the console.
func (c *Console) Disconnect(w http.ResponseWriter, r *http.Request) {
	scope, ok := c.requireSession(w, r)
	if !ok {
		return
	}
	if err := c.connects.Disconnect(r.Context(), scope); err != nil {
		c.fail(w, err)
		return
	}
	c.writeJSON(w, http.StatusOK, map[string]bool{"disconnected": true})
}

// scopeFor is the tenant the console acts on.
func (c *Console) scopeFor() persistence.Scope {
	return persistence.Scope{TenantID: c.cfg.TenantID, UserID: c.cfg.UserID}
}
