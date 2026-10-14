package drive

import (
	"context"
	"errors"
	"time"

	"github.com/shovan04/storvia/internal/persistence"
)

// Store is the persistence surface the Drive provider needs.
//
// It is declared here, in the consumer, rather than importing a repository
// package directly. That inversion is what keeps the provider testable with a
// fake and stops a storage change from rippling into OAuth code.
type Store interface {
	// Resolve returns the connection for a scope with its token decrypted. It
	// returns ErrNoConnection when the tenant has not connected Drive.
	Resolve(ctx context.Context, scope persistence.Scope) (id, subject, refreshToken, status, rootFolderID string, err error)
	// Upsert stores or refreshes a connection and returns its ID.
	Upsert(ctx context.Context, scope persistence.Scope, subject, refreshToken string) (string, error)
	// UpdateRefreshToken replaces a rotated refresh token.
	UpdateRefreshToken(ctx context.Context, connectionID, refreshToken string) error
	// SetStatus changes a connection's status.
	SetStatus(ctx context.Context, connectionID, status string) error
	// SetRootFolderID records the app-managed folder.
	SetRootFolderID(ctx context.Context, connectionID, folderID string) error
}

// storeResolver adapts a Store to the ConnectionResolver interface the client
// consumes. The indirection exists so the client keeps working against the small
// Connection struct it needs, not the storage record shape.
type storeResolver struct{ store Store }

// NewResolver builds a ConnectionResolver backed by a Store.
func NewResolver(store Store) ConnectionResolver { return &storeResolver{store: store} }

// Connection returns the connection for a scope.
//
// A tenant with no connection returns ErrNoConnection rather than a zero
// Connection, so the caller can distinguish "not connected" from "connected but
// broken".
func (r *storeResolver) Connection(ctx context.Context, scope persistence.Scope) (Connection, error) {
	id, subject, token, status, folderID, err := r.store.Resolve(ctx, scope)
	if err != nil {
		if errors.Is(err, persistence.ErrNoConnection) {
			return Connection{}, persistence.ErrNoConnection
		}
		return Connection{}, err
	}
	return Connection{
		ID:           id,
		TenantID:     scope.TenantID,
		UserID:       scope.UserID,
		Subject:      subject,
		RefreshToken: token,
		RootFolderID: folderID,
		Status:       status,
	}, nil
}

// SaveConnection stores or refreshes a connection.
func (r *storeResolver) SaveConnection(ctx context.Context, connection Connection) error {
	id, err := r.store.Upsert(ctx, persistence.Scope{
		TenantID: connection.TenantID,
		UserID:   connection.UserID,
	}, connection.Subject, connection.RefreshToken)
	if err != nil {
		return err
	}
	connection.ID = id
	if connection.RootFolderID != "" {
		if err := r.store.SetRootFolderID(ctx, id, connection.RootFolderID); err != nil {
			return err
		}
	}
	return nil
}

// MarkStatus changes a connection's status.
//
// A status change whose connection has vanished is treated as success: the
// intended end state, disconnected or needing reauthorization, already holds.
func (r *storeResolver) MarkStatus(ctx context.Context, connectionID, status string) error {
	return r.store.SetStatus(ctx, connectionID, status)
}

// SetRefreshToken persists a rotated refresh token.
func (c *Client) SetRefreshToken(ctx context.Context, connectionID, refreshToken string) error {
	store, ok := c.conns.(*storeResolver)
	if !ok {
		return errors.New("drive: this connection store cannot persist rotated tokens")
	}
	return store.store.UpdateRefreshToken(ctx, connectionID, refreshToken)
}

// StalenessThreshold is how long without a successful call before a connection is
// reported as possibly stale in the status endpoint.
const StalenessThreshold = 24 * time.Hour

// Stale reports whether lastSuccess is old enough to be worth surfacing.
func Stale(lastSuccess *time.Time, now time.Time) bool {
	if lastSuccess == nil {
		// Never used is different from used long ago: it points at a
		// configuration or connection problem rather than a stale session.
		return true
	}
	return now.Sub(*lastSuccess) > StalenessThreshold
}
