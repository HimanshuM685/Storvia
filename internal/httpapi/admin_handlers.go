package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"html/template"
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
}

// NewAdminHandler builds the development-only panel handler.
func NewAdminHandler(cfg AdminPanelConfig, issuer *auth.Issuer, sessions auth.Lookup, connections AdminConnections) *AdminHandler {
	if cfg.FlowCookie == "" {
		cfg.FlowCookie = "storvia_admin_flow"
	}
	return &AdminHandler{cfg: cfg, issuer: issuer, sessions: sessions, connections: connections}
}

// Show handles GET /admin.
func (h *AdminHandler) Show(w http.ResponseWriter, r *http.Request) {
	if !h.enabled(w) {
		return
	}
	identity, signedIn := h.adminIdentity(r)
	data := adminPageData{
		SignedIn: signedIn,
		TenantID: h.cfg.TenantID,
		UserID:   h.cfg.UserID,
	}
	if signedIn {
		status, err := h.connections.Status(r.Context(), persistence.Scope{TenantID: identity.TenantID, UserID: identity.UserID})
		if err != nil {
			renderAdminError(w, "The Drive connection status could not be loaded.")
			return
		}
		data.Status = &status
	}
	renderAdminPage(w, data)
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
		renderAdminError(w, "Google Drive authorization was not completed.")
		return
	}
	flow, err := r.Cookie(h.cfg.FlowCookie)
	if err != nil || strings.TrimSpace(flow.Value) == "" {
		renderAdminError(w, "The local OAuth browser flow is missing or expired. Start again.")
		return
	}
	code := r.URL.Query().Get("code")
	state := r.URL.Query().Get("state")
	if code == "" || state == "" {
		renderAdminError(w, "Google returned an incomplete authorization response.")
		return
	}
	if err := h.connections.CompleteConnectAt(r.Context(), h.scope(), code, state, flow.Value, h.cfg.RedirectURI); err != nil {
		h.clearFlowCookie(w)
		renderAdminError(w, "Google Drive authorization could not be completed.")
		return
	}
	if err := h.issueSession(w, r.Context()); err != nil {
		h.clearFlowCookie(w)
		renderAdminError(w, "The Drive connection succeeded, but the local admin session could not be created.")
		return
	}
	h.clearFlowCookie(w)
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

type adminPageData struct {
	SignedIn bool
	TenantID string
	UserID   string
	Status   *service.ConnectionStatus
}

var adminTemplate = template.Must(template.New("admin").Funcs(template.FuncMap{"join": strings.Join}).Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'">
<title>Storvia admin</title><style>body{font:16px system-ui,sans-serif;max-width:720px;margin:3rem auto;padding:0 1rem;color:#17202a}main{border:1px solid #d5dbe0;border-radius:12px;padding:2rem}code{background:#eef1f4;padding:.15rem .3rem;border-radius:4px}.ok{color:#147d42}.warn{color:#9a5b00}button,a.button{border:0;border-radius:6px;padding:.65rem 1rem;background:#1769aa;color:white;text-decoration:none;font:inherit;cursor:pointer}form{display:inline;margin-right:.5rem}.muted{color:#59636e}</style></head>
<body><main><h1>Storvia development admin</h1>
<p class="muted">This panel is enabled only in the development configuration. It tests Google Drive OAuth and connection status; file transfer is not implemented yet.</p>
{{if not .SignedIn}}<h2>Google Drive login</h2><p>Connect a test Google account using the narrow <code>drive.file</code> scope.</p><p><a class="button" href="/admin/connect">Sign in with Google Drive</a></p>
{{else}}<h2>Drive connection</h2><dl><dt>Test tenant</dt><dd><code>{{.TenantID}}</code></dd><dt>Test user</dt><dd><code>{{.UserID}}</code></dd>{{with .Status}}<dt>Status</dt><dd class="{{if .Connected}}ok{{else}}warn{{end}}">{{.Status}}</dd><dt>Google subject</dt><dd><code>{{if .Subject}}{{.Subject}}{{else}}not connected{{end}}</code></dd><dt>Granted scope</dt><dd><code>{{join .GrantedScopes ", "}}</code></dd><dt>Storvia folder</dt><dd>{{if .FolderPresent}}present{{else}}not created yet{{end}}</dd>{{if .LastSuccessAt}}<dt>Last successful Drive call</dt><dd>{{.LastSuccessAt}}</dd>{{end}}{{end}}</dl><p><a class="button" href="/admin">Refresh status</a></p><form method="post" action="/admin/disconnect"><button type="submit">Disconnect Drive</button></form><form method="post" action="/admin/logout"><button type="submit">Log out</button></form>{{end}}
</main></body></html>`))

func renderAdminPage(w http.ResponseWriter, data adminPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if err := adminTemplate.Execute(w, data); err != nil {
		// The template is static and parsed at init; this is only a defensive
		// response path if future edits introduce an execution error.
		http.Error(w, "admin page unavailable", http.StatusInternalServerError)
	}
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
