package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/shovan04/storvia/internal/apperr"
	"github.com/shovan04/storvia/internal/persistence"
	"github.com/shovan04/storvia/internal/storage/provider/drive"
)

// OAuthStateTTL is how long a pending authorization stays valid.
const OAuthStateTTL = 10 * time.Minute

// Connector is the Drive consent surface the connection service drives.
//
// Declaring it here, in the consumer, keeps the service free of OAuth types and
// makes it testable without a Google endpoint.
type Connector interface {
	// BeginConnect records a pending authorization bound to browserBinding and
	// returns the consent URL.
	BeginConnect(ctx context.Context, scope persistence.Scope, browserBinding string) (authorizeURL string, err error)
	// CompleteConnect exchanges the code for tokens and stores the connection.
	CompleteConnect(ctx context.Context, scope persistence.Scope, code, state, browserBinding string) error
	// Status reports the connection state without secrets.
	Status(ctx context.Context, scope persistence.Scope) (ConnectionStatus, error)
	// Disconnect stops new Drive requests and drops the stored grant. It leaves
	// the user's Drive files untouched.
	Disconnect(ctx context.Context, scope persistence.Scope) error
}

// ConnectionStatus is the client-visible connection state.
type ConnectionStatus struct {
	Connected     bool
	Status        string
	Subject       string
	GrantedScopes []string
	FolderPresent bool
	LastSuccessAt *time.Time
	Stale         bool
}

// ConnectionService handles the Drive connection lifecycle.
type ConnectionService struct {
	connector Connector
}

// NewConnectionService builds the service.
func NewConnectionService(connector Connector) *ConnectionService {
	return &ConnectionService{connector: connector}
}

// Status reports the Drive connection state.
func (s *ConnectionService) Status(ctx context.Context, scope persistence.Scope) (ConnectionStatus, error) {
	return s.connector.Status(ctx, scope)
}

// Disconnect removes the connection.
//
// It stops new Drive requests and new uploads, and leaves the user's own Drive
// files in place. Storvia never deletes user data as a side effect of a
// connection change.
func (s *ConnectionService) Disconnect(ctx context.Context, scope persistence.Scope) error {
	return s.connector.Disconnect(ctx, scope)
}

// BeginConnect starts the consent flow and returns the URL the browser must open.
func (s *ConnectionService) BeginConnect(ctx context.Context, scope persistence.Scope, browserBinding string) (string, error) {
	if browserBinding == "" {
		// An unbound flow would accept a callback from any browser, which is the
		// OAuth account-mix-up attack.
		return "", apperr.Authentication("a signed-in session is required to connect google drive")
	}
	url, err := s.connector.BeginConnect(ctx, scope, browserBinding)
	if err != nil {
		return "", apperr.Wrap(err, apperr.KindInternal, "Internal error",
			"the google authorization could not be started")
	}
	return url, nil
}

// CompleteConnect finishes the consent flow.
func (s *ConnectionService) CompleteConnect(ctx context.Context, scope persistence.Scope, code, state, browserBinding string) error {
	return s.connector.CompleteConnect(ctx, scope, code, state, browserBinding)
}

// DriveConnector implements Connector over the Drive client.
type DriveConnector struct {
	client *drive.Client
}

// NewDriveConnector builds a connector.
func NewDriveConnector(client *drive.Client) *DriveConnector {
	return &DriveConnector{client: client}
}

// BeginConnect starts the consent flow.
func (d *DriveConnector) BeginConnect(ctx context.Context, scope persistence.Scope, browserBinding string) (string, error) {
	state, err := d.client.NewState()
	if err != nil {
		return "", apperr.Wrap(err, apperr.KindInternal, "Internal error", "could not start the authorization")
	}
	d.client.RememberState(state, browserBinding)
	return d.client.AuthorizeURL(state, browserBinding)
}

// CompleteConnect finishes the consent flow.
func (d *DriveConnector) CompleteConnect(ctx context.Context, scope persistence.Scope, code, state, browserBinding string) error {
	_, err := d.client.Exchange(ctx, drive.Scope{TenantID: scope.TenantID, UserID: scope.UserID}, code, state, browserBinding)
	if err == nil {
		return nil
	}
	if errors.Is(err, drive.ErrNotConfigured) {
		return apperr.ProviderNotConfigured("google drive is not configured on this deployment")
	}
	if apperr.Is(err, apperr.KindConsentRequired) {
		return err
	}
	return apperr.Wrap(err, apperr.KindInternal, "Internal error", "the drive connection could not be completed")
}

// Status reports the connection state.
func (d *DriveConnector) Status(ctx context.Context, scope persistence.Scope) (ConnectionStatus, error) {
	status := ConnectionStatus{Status: "disconnected", GrantedScopes: []string{drive.ScopeDriveFile}}

	raw, err := d.client.ConnectionStatus(ctx, scope)
	if err != nil {
		if errors.Is(err, persistence.ErrNoConnection) {
			return status, nil
		}
		return ConnectionStatus{}, apperr.Wrap(err, apperr.KindInternal, "Internal error",
			"the drive connection state could not be read")
	}

	status.Status = raw.Status
	status.Subject = raw.Subject
	status.FolderPresent = raw.RootFolderID != ""
	status.LastSuccessAt = raw.LastSuccessAt
	status.Stale = drive.Stale(raw.LastSuccessAt, time.Now())
	status.Connected = raw.Status == drive.StatusConnected
	if !d.client.Configured() {
		// Report the deployment gap rather than implying the user's account is at
		// fault.
		status.Status = "provider_not_configured"
	}
	return status, nil
}

// Disconnect removes the connection.
func (d *DriveConnector) Disconnect(ctx context.Context, scope persistence.Scope) error {
	if err := d.client.Disconnect(ctx, scope); err != nil {
		if errors.Is(err, persistence.ErrNoConnection) {
			return apperr.NotFound("no google drive connection exists for this account")
		}
		return apperr.Wrap(err, apperr.KindInternal, "Internal error", "the drive connection could not be removed")
	}
	return nil
}

// ensure the fmt import stays used if the file evolves; the reference below keeps
// the import honest today.
var _ = fmt.Sprintf
