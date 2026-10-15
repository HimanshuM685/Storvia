package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/shovan04/storvia/internal/auth"
	"github.com/shovan04/storvia/internal/persistence"
	"github.com/shovan04/storvia/internal/storage/service"
)

// AdminPanelConfig is the intentionally small configuration surface for the
// development-only Drive consent panel. The configured tenant/user are test
// identities, not values supplied by the browser.
type AdminPanelConfig struct {
	Enabled     bool
	TenantID    string
	UserID      string
	FlowCookie  string
	RedirectURI string
	Secure      bool
}

// AdminConnections is the subset of the connection service used by the panel.
// Keeping this as an interface makes the page and its session boundary easy to
// test without calling Google.
type AdminConnections interface {
	BeginConnectAt(ctx context.Context, scope persistence.Scope, browserBinding, redirectURI string) (string, error)
	CompleteConnectAt(ctx context.Context, scope persistence.Scope, code, state, browserBinding, redirectURI string) error
	Status(ctx context.Context, scope persistence.Scope) (service.ConnectionStatus, error)
	Disconnect(ctx context.Context, scope persistence.Scope) error
}

// AdminHandler serves a local development panel for testing Google Drive OAuth.
// It is not a replacement for application authentication and is never mounted
// when the configuration environment is not development.
type AdminHandler struct {
	cfg         AdminPanelConfig
	issuer      *auth.Issuer
	sessions    auth.Lookup
	connections AdminConnections
	diagnostics DiagnosticsProvider
	consent     FallbackConsenter
	folders     FolderEnsurer
	// log records why a connect attempt failed.
	//
	// The panel deliberately shows the user a generic message, because the cause can
	// contain provider detail. That is only safe if the real reason is recorded
	// somewhere: without this, a failed connection produced a 502 and no explanation
	// anywhere, which is how a scope mismatch went uninvestigated for a whole session.
	log *slog.Logger
}

// FallbackConsenter lets the development panel record the disclosure acceptance a
// real tenant would give through the API.
//
// It is here so a developer can unblock themselves without curling an endpoint by
// hand, and it is deliberately the same service the API uses rather than a shortcut
// that writes the record directly.
type FallbackConsenter interface {
	Accept(ctx context.Context, scope persistence.Scope) (bool, error)
}

// NewAdminHandler builds the development-only panel handler.
func NewAdminHandler(cfg AdminPanelConfig, issuer *auth.Issuer, sessions auth.Lookup, connections AdminConnections) *AdminHandler {
	if cfg.FlowCookie == "" {
		cfg.FlowCookie = "storvia_admin_flow"
	}
	return &AdminHandler{cfg: cfg, issuer: issuer, sessions: sessions, connections: connections, log: slog.Default()}
}

// FolderEnsurer prepares the app-managed Drive folder on request.
type FolderEnsurer interface {
	EnsureRootFolder(ctx context.Context, scope persistence.Scope) error
}

// WithLogger attaches the logger used to record connect failures.
func (h *AdminHandler) WithLogger(log *slog.Logger) *AdminHandler {
	if log != nil {
		h.log = log
	}
	return h
}

// logConnectFailure records the real reason behind a user-facing message.
func (h *AdminHandler) logConnectFailure(r *http.Request, stage string, err error) {
	if h.log == nil {
		return
	}
	h.log.ErrorContext(r.Context(), "admin drive connect failed",
		"stage", stage,
		"reason", err.Error(),
	)
}

// WithDiagnostics attaches the live-status source and the consent action.
//
// Both are setters after construction because the handler is built before the
// application wiring that produces them, and a development-only panel should not force
// the production wiring to carry dependencies it never uses.
func (h *AdminHandler) WithDiagnostics(d DiagnosticsProvider, c FallbackConsenter) *AdminHandler {
	h.diagnostics = d
	h.consent = c
	return h
}

// WithFolderEnsurer attaches the folder-management action.
func (h *AdminHandler) WithFolderEnsurer(f FolderEnsurer) *AdminHandler {
	h.folders = f
	return h
}

// Show handles GET /admin.
func (h *AdminHandler) Show(w http.ResponseWriter, r *http.Request) {
	if !h.enabled(w) {
		return
	}
	identity, signedIn := h.adminIdentity(r)
	scope := persistence.Scope{TenantID: h.cfg.TenantID, UserID: h.cfg.UserID}
	if signedIn {
		scope = persistence.Scope{TenantID: identity.TenantID, UserID: identity.UserID}
	}

	data := adminDashboardData{
		SignedIn: signedIn,
		TenantID: h.cfg.TenantID,
		UserID:   h.cfg.UserID,
		Now:      time.Now().UTC(),
	}
	if signedIn {
		status, err := h.connections.Status(r.Context(), scope)
		if err != nil {
			renderAdminError(w, "The Drive connection status could not be loaded.")
			return
		}
		data.Status = &adminStatusView{
			Connected:     status.Connected,
			Status:        status.Status,
			Subject:       status.Subject,
			GrantedScopes: status.GrantedScopes,
			FolderPresent: status.FolderPresent,
			LastSuccessAt: status.LastSuccessAt,
		}
	}
	if h.diagnostics != nil {
		// A diagnostics failure must not blank the page: the rest of the panel is
		// still the most useful thing to show.
		diag := h.diagnostics.Diagnostics(r.Context())
		if diag.ConsentVersion == "" {
			diag.ConsentVersion = "unavailable"
		}
		data.Diag = diag
	}
	renderAdminDashboard(w, data)
}

// AcceptFallbackConsent handles POST /admin/fallback-consent.
//
// Without this, exercising the outage path requires discovering an API endpoint and
// curling it first, which is the single easiest step to miss.
func (h *AdminHandler) AcceptFallbackConsent(w http.ResponseWriter, r *http.Request) {
	if !h.enabled(w) {
		return
	}
	if h.consent == nil {
		renderAdminError(w, "Fallback consent is not available in this build.")
		return
	}
	identity, signedIn := h.adminIdentity(r)
	scope := persistence.Scope{TenantID: h.cfg.TenantID, UserID: h.cfg.UserID}
	if signedIn {
		scope = persistence.Scope{TenantID: identity.TenantID, UserID: identity.UserID}
	}
	if _, err := h.consent.Accept(r.Context(), scope); err != nil {
		renderAdminError(w, "The fallback consent could not be recorded.")
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// Connect handles GET /admin/connect. It creates a temporary browser-bound
// OAuth flow and redirects to Google's consent screen.
func (h *AdminHandler) Connect(w http.ResponseWriter, r *http.Request) {
	if !h.enabled(w) {
		return
	}
	flow, err := randomAdminToken()
	if err != nil {
		renderAdminError(w, "The local OAuth flow could not be started.")
		return
	}
	h.setFlowCookie(w, flow)
	authorizeURL, err := h.connections.BeginConnectAt(r.Context(), h.scope(), flow, h.cfg.RedirectURI)
	if err != nil {
		h.clearFlowCookie(w)
		renderAdminError(w, "Google Drive OAuth is not configured or could not be started.")
		return
	}
	http.Redirect(w, r, authorizeURL, http.StatusFound)
}

// Callback handles GET /admin/oauth/callback. It exchanges the Google code,
// then issues an ordinary Storvia session for the configured development user.
func (h *AdminHandler) Callback(w http.ResponseWriter, r *http.Request) {
	if !h.enabled(w) {
		return
	}
	if oauthErr := r.URL.Query().Get("error"); oauthErr != "" {
		h.clearFlowCookie(w)
		h.logConnectFailure(r, "google_declined", fmt.Errorf("google returned %s", oauthErr))
		renderAdminError(w, "Google Drive authorization was not completed.")
		return
	}
	flow, err := r.Cookie(h.cfg.FlowCookie)
	if err != nil || strings.TrimSpace(flow.Value) == "" {
		h.logConnectFailure(r, "flow_cookie", errors.New("the browser flow cookie was missing"))
		renderAdminError(w, "The local OAuth browser flow is missing or expired. Start again.")
		return
	}
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		h.logConnectFailure(r, "incomplete_response", fmt.Errorf("code or state missing"))
		renderAdminError(w, "Google returned an incomplete authorization response.")
		return
	}
	if err := h.connections.CompleteConnectAt(r.Context(), h.scope(), code, state, flow.Value, h.cfg.RedirectURI); err != nil {
		h.clearFlowCookie(w)
		h.logConnectFailure(r, "complete_connect", err)
		renderAdminError(w, "Google Drive authorization could not be completed. See the server log for the reason.")
		return
	}
	if err := h.issueSession(w, r.Context()); err != nil {
		h.clearFlowCookie(w)
		h.logConnectFailure(r, "issue_session", err)
		renderAdminError(w, "The Drive connection succeeded, but the local admin session could not be created.")
		return
	}
	h.clearFlowCookie(w)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// EnsureFolder handles POST /admin/ensure-folder.
//
// It exists because the app-managed folder is created at connection time, and a
// connection made before that was implemented has no folder. Re-running it here is an
// explicit user action rather than something that happens silently on the next upload,
// so "create a folder and start writing there" is always a decision someone made.
func (h *AdminHandler) EnsureFolder(w http.ResponseWriter, r *http.Request) {
	if !h.enabled(w) {
		return
	}
	if h.folders == nil {
		renderAdminError(w, "Drive folder management is not available in this build.")
		return
	}
	identity, _ := h.adminIdentity(r)
	scope := persistence.Scope{TenantID: h.cfg.TenantID, UserID: h.cfg.UserID}
	if identity.Valid() {
		scope = persistence.Scope{TenantID: identity.TenantID, UserID: identity.UserID}
	}
	if err := h.folders.EnsureRootFolder(r.Context(), scope); err != nil {
		h.logConnectFailure(r, "ensure_folder", err)
		renderAdminError(w, "The Storvia Drive folder could not be prepared. See the server log.")
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// Disconnect handles POST /admin/disconnect.
func (h *AdminHandler) Disconnect(w http.ResponseWriter, r *http.Request) {
	if !h.enabled(w) {
		return
	}
	if _, ok := h.adminIdentity(r); !ok {
		h.renderUnauthorized(w)
		return
	}
	if err := h.connections.Disconnect(r.Context(), h.scope()); err != nil {
		renderAdminError(w, "The Drive connection could not be disconnected.")
		return
	}
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

// Logout handles POST /admin/logout. Clearing the cookie is sufficient for the
// local panel; the server-side session store may retain an expired entry until
// its normal bounded eviction.
func (h *AdminHandler) Logout(w http.ResponseWriter, r *http.Request) {
	if !h.enabled(w) {
		return
	}
	h.issuer.ClearSessionCookie(w)
	http.Redirect(w, r, "/admin", http.StatusSeeOther)
}

func (h *AdminHandler) enabled(w http.ResponseWriter) bool {
	if h.cfg.Enabled && h.issuer != nil && h.sessions != nil && h.connections != nil {
		return true
	}
	w.WriteHeader(http.StatusNotFound)
	return false
}

func (h *AdminHandler) scope() persistence.Scope {
	return persistence.Scope{TenantID: h.cfg.TenantID, UserID: h.cfg.UserID}
}

func (h *AdminHandler) adminIdentity(r *http.Request) (auth.Identity, bool) {
	token := h.issuer.SessionCookieValue(r)
	if token == "" {
		return auth.Identity{}, false
	}
	verified, err := h.issuer.Verify(token, time.Now())
	if err != nil {
		return auth.Identity{}, false
	}
	identity, ok := h.sessions.LookupSession(r.Context(), verified.ID)
	if !ok || identity.TenantID != h.cfg.TenantID || identity.UserID != h.cfg.UserID {
		return auth.Identity{}, false
	}
	return identity, true
}

func (h *AdminHandler) issueSession(w http.ResponseWriter, ctx context.Context) error {
	identity := auth.Identity{TenantID: h.cfg.TenantID, UserID: h.cfg.UserID, Email: "development-admin"}
	token, session, err := h.issuer.Issue(identity, "", 0)
	if err != nil {
		return err
	}
	if err := h.sessions.StoreSession(ctx, session); err != nil {
		return err
	}
	h.issuer.SetSessionCookie(w, token, session.ExpiresAt)
	return nil
}

func (h *AdminHandler) setFlowCookie(w http.ResponseWriter, value string) {
	http.SetCookie(w, &http.Cookie{
		Name:     h.cfg.FlowCookie,
		Value:    value,
		Path:     "/admin",
		MaxAge:   600,
		HttpOnly: true,
		Secure:   h.cfg.Secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *AdminHandler) clearFlowCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     h.cfg.FlowCookie,
		Value:    "",
		Path:     "/admin",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   h.cfg.Secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func (h *AdminHandler) renderUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte("<!doctype html><title>Storvia admin</title><p>Sign in with Google Drive first.</p><p><a href=\"/admin\">Back to admin</a></p>"))
}

func renderAdminError(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusBadGateway)
	_, _ = w.Write([]byte("<!doctype html><title>Storvia admin</title><p>" + template.HTMLEscapeString(message) + "</p><p><a href=\"/admin\">Back to admin</a></p>"))
}

func randomAdminToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
