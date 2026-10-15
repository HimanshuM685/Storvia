package httpapi

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shovan04/storvia/internal/auth"
	"github.com/shovan04/storvia/internal/persistence"
	"github.com/shovan04/storvia/internal/storage/service"
)

type adminConnectionsStub struct {
	startedBinding string
	completed      bool
	disconnected   bool
	status         service.ConnectionStatus
	beginErr       error
	completeErr    error
}

func (s *adminConnectionsStub) BeginConnectAt(_ context.Context, _ persistence.Scope, binding, _ string) (string, error) {
	s.startedBinding = binding
	if s.beginErr != nil {
		return "", s.beginErr
	}
	return "https://accounts.google.com/auth?state=state-test", nil
}

func (s *adminConnectionsStub) CompleteConnectAt(_ context.Context, _ persistence.Scope, code, state, binding, _ string) error {
	if code != "code-test" || state != "state-test" || binding == "" {
		return errors.New("unexpected oauth callback")
	}
	if s.completeErr != nil {
		return s.completeErr
	}
	s.completed = true
	return nil
}

func (s *adminConnectionsStub) Status(context.Context, persistence.Scope) (service.ConnectionStatus, error) {
	return s.status, nil
}

func (s *adminConnectionsStub) Disconnect(context.Context, persistence.Scope) error {
	s.disconnected = true
	return nil
}

func newAdminTestHandler(t *testing.T, connections AdminConnections) (*AdminHandler, *auth.Issuer, *auth.MemorySessions) {
	t.Helper()
	issuer, err := auth.NewIssuer(auth.IssuerOptions{
		SigningKey: "01234567890123456789012345678901",
		CookieName: "storvia_session",
		TTL:        time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	sessions := auth.NewMemorySessions(10)
	handler := NewAdminHandler(AdminPanelConfig{
		Enabled:    true,
		TenantID:   "tenant-dev",
		UserID:     "user-dev",
		FlowCookie: "storvia_admin_flow",
	}, issuer, sessions, connections)
	return handler, issuer, sessions
}

func TestAdminPanelIsDisabledByDefault(t *testing.T) {
	handler := NewAdminHandler(AdminPanelConfig{}, nil, nil, nil)
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/admin", nil)

	handler.Show(recorder, request)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", recorder.Code)
	}
}

func TestAdminOAuthCallbackCreatesScopedSession(t *testing.T) {
	connections := &adminConnectionsStub{status: service.ConnectionStatus{
		Connected:     true,
		Status:        "connected",
		Subject:       "google-subject",
		GrantedScopes: []string{"https://www.googleapis.com/auth/drive.file"},
	}}
	handler, issuer, sessions := newAdminTestHandler(t, connections)

	start := httptest.NewRecorder()
	handler.Connect(start, httptest.NewRequest(http.MethodGet, "/admin/connect", nil))
	if start.Code != http.StatusFound {
		t.Fatalf("connect status = %d, want 302", start.Code)
	}
	if got := start.Header().Get("Location"); got != "https://accounts.google.com/auth?state=state-test" {
		t.Fatalf("location = %q", got)
	}
	flowCookies := start.Result().Cookies()
	if len(flowCookies) != 1 || flowCookies[0].Name != "storvia_admin_flow" || flowCookies[0].Value == "" {
		t.Fatalf("connect did not set a flow cookie: %#v", flowCookies)
	}

	callback := httptest.NewRequest(http.MethodGet, "/admin/oauth/callback?code=code-test&state=state-test", nil)
	callback.AddCookie(flowCookies[0])
	callbackResponse := httptest.NewRecorder()
	handler.Callback(callbackResponse, callback)
	if callbackResponse.Code != http.StatusSeeOther {
		t.Fatalf("callback status = %d, want 303", callbackResponse.Code)
	}
	if !connections.completed {
		t.Fatal("callback did not complete the Drive connection")
	}

	var sessionCookie *http.Cookie
	for _, cookie := range callbackResponse.Result().Cookies() {
		if cookie.Name == issuer.CookieName() && cookie.Value != "" {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil {
		t.Fatal("callback did not issue an application session cookie")
	}
	if _, ok := sessions.LookupSession(context.Background(), ""); ok {
		t.Fatal("empty session ID must not be stored")
	}

	pageRequest := httptest.NewRequest(http.MethodGet, "/admin", nil)
	pageRequest.AddCookie(sessionCookie)
	pageResponse := httptest.NewRecorder()
	handler.Show(pageResponse, pageRequest)
	if pageResponse.Code != http.StatusOK {
		t.Fatalf("admin page status = %d, want 200", pageResponse.Code)
	}
	body := pageResponse.Body.String()
	for _, want := range []string{"google-subject", "connected", "drive.file"} {
		if !strings.Contains(body, want) {
			t.Errorf("admin page does not contain %q: %s", want, body)
		}
	}
	if strings.Contains(body, "code-test") || strings.Contains(body, "storvia_admin_flow") {
		t.Errorf("admin page leaked OAuth flow material: %s", body)
	}
}

func TestAdminDisconnectRequiresSessionAndUsesConfiguredScope(t *testing.T) {
	connections := &adminConnectionsStub{}
	handler, issuer, sessions := newAdminTestHandler(t, connections)

	unauthorized := httptest.NewRecorder()
	handler.Disconnect(unauthorized, httptest.NewRequest(http.MethodPost, "/admin/disconnect", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want 401", unauthorized.Code)
	}

	token, session, err := issuer.Issue(auth.Identity{TenantID: "tenant-dev", UserID: "user-dev"}, "session-test", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := sessions.StoreSession(context.Background(), session); err != nil {
		t.Fatal(err)
	}
	authorized := httptest.NewRequest(http.MethodPost, "/admin/disconnect", nil)
	authorized.AddCookie(&http.Cookie{Name: issuer.CookieName(), Value: token})
	response := httptest.NewRecorder()
	handler.Disconnect(response, authorized)
	if response.Code != http.StatusSeeOther || !connections.disconnected {
		t.Fatalf("disconnect status = %d, disconnected = %v", response.Code, connections.disconnected)
	}
}

// TestAdminCallbackLogsTheRealFailureReason is the guard for a debugging dead end.
//
// The callback showed the user "Google Drive authorization could not be completed" and
// recorded nothing anywhere. A scope mismatch in the OAuth exchange therefore looked
// identical to a bad code, a replayed state, and a provider outage — and went
// uninvestigated for a whole session.
//
// The user-facing message stays generic on purpose; the point is that the cause must
// reach the log.
func TestAdminCallbackLogsTheRealFailureReason(t *testing.T) {
	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler, _, _ := newAdminTestHandler(t, &adminConnectionsStub{
		completeErr: errors.New("drive: about returned 401 UNAUTHENTICATED"),
	})
	handler.log = logger

	req := httptest.NewRequest(http.MethodGet, "/admin/oauth/callback?code=code-test&state=state-test", nil)
	req.AddCookie(&http.Cookie{Name: "storvia_admin_flow", Value: "flow-value"})
	rec := httptest.NewRecorder()

	handler.Callback(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	if logged.Len() == 0 {
		t.Fatal("the failure reason was discarded; nothing was logged")
	}
	for _, want := range []string{"complete_connect", "UNAUTHENTICATED"} {
		if !strings.Contains(logged.String(), want) {
			t.Errorf("log does not mention %q:\n%s", want, logged.String())
		}
	}
	// The page itself must not carry the provider detail.
	if strings.Contains(rec.Body.String(), "UNAUTHENTICATED") {
		t.Error("the error page must not leak the provider's reason")
	}
}

// TestAdminCallbackLogsAMissingFlowCookie covers the other silent branch.
func TestAdminCallbackLogsAMissingFlowCookie(t *testing.T) {
	var logged bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))

	handler, _, _ := newAdminTestHandler(t, &adminConnectionsStub{})
	handler.log = logger

	req := httptest.NewRequest(http.MethodGet, "/admin/oauth/callback?code=code-test&state=state-test", nil)
	rec := httptest.NewRecorder()
	handler.Callback(rec, req)

	if !strings.Contains(logged.String(), "flow_cookie") {
		t.Errorf("a missing flow cookie must be logged:\n%s", logged.String())
	}
}
