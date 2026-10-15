package persistence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/shovan04/storvia/internal/idid"
	"github.com/shovan04/storvia/internal/secrets"
)

// DriveConnection is the stored Drive connection row.
type DriveConnection struct {
	ID       string
	TenantID string
	UserID   string
	Subject  string
	// EncryptedRefreshToken is ciphertext. It is never decrypted outside the
	// Drive adapter.
	EncryptedRefreshToken []byte
	// PlaintextRefreshToken is set only by ConnectionByID, which reads a connection
	// for the Drive adapter. Nothing else should populate or read it.
	PlaintextRefreshToken string
	KeyReference          string
	GrantedScopes         string
	RootFolderID          *string
	ChangesCursor         *string
	Status                string
	LastSuccessAt         *time.Time
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// DriveConnectionRepository stores Drive connections.
//
// It implements the Drive adapter's ConnectionResolver, which is what keeps the
// provider free of any repository dependency.
type DriveConnectionRepository struct {
	store   *Store
	keyring secrets.Keyring
	ids     *idid.Generator
}

// NewDriveConnectionRepository builds a repository. The keyring is required
// because a refresh token may never be stored in plaintext.
func NewDriveConnectionRepository(store *Store, keyring secrets.Keyring) *DriveConnectionRepository {
	return &DriveConnectionRepository{
		store:   store,
		keyring: keyring,
		ids:     idid.NewGenerator("dc"),
	}
}

// ErrNoConnection reports that a tenant has no Drive connection.
var ErrNoConnection = errors.New("persistence: no drive connection")

// Resolve returns a connection with its refresh token decrypted.
//
// Decryption happens here, at the edge of the provider boundary, so no other
// layer ever holds a usable refresh token.
func (r *DriveConnectionRepository) Resolve(ctx context.Context, scope Scope) (id string, subject string, refreshToken string, status string, rootFolderID string, err error) {
	row := r.store.db.QueryRowContext(ctx, r.store.Rebind(`SELECT
		id, google_subject, encrypted_refresh_token, key_reference, status, root_folder_id
		FROM drive_connections
		WHERE tenant_id = ? AND user_id = ?
		ORDER BY updated_at DESC`), scope.TenantID, scope.UserID)

	var (
		ciphertext []byte
		keyRef     sql.NullString
		folderID   sql.NullString
	)
	err = row.Scan(&id, &subject, &ciphertext, &keyRef, &status, &folderID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", "", "", "", ErrNoConnection
	}
	if err != nil {
		return "", "", "", "", "", fmt.Errorf("persistence: scan drive connection: %w", err)
	}

	plaintext, err := r.keyring.Decrypt(keyRef.String, ciphertext)
	if err != nil {
		// A connection whose token cannot be decrypted is not usable. Returning
		// an error rather than an empty token keeps a decryption failure from
		// looking like a revoked grant.
		return "", "", "", "", "", fmt.Errorf("persistence: decrypt refresh token: %w", err)
	}
	return id, subject, string(plaintext), status, folderID.String, nil
}

// Upsert stores or refreshes a connection.
//
// The refresh token is encrypted here, on the way in. Nothing upstream has to
// remember to do it, and nothing downstream can read it in the clear.
func (r *DriveConnectionRepository) Upsert(ctx context.Context, scope Scope, subject, refreshToken string) (string, error) {
	if r.keyring == nil {
		return "", errors.New("persistence: a keyring is required to store a refresh token")
	}
	if subject == "" {
		return "", errors.New("persistence: subject is required")
	}

	encrypted, err := r.keyring.Encrypt(r.keyring.ActiveReference(), []byte(refreshToken))
	if err != nil {
		return "", fmt.Errorf("persistence: encrypt refresh token: %w", err)
	}

	existingID, _, _, _, _, err := r.Resolve(ctx, scope)
	if err == nil && existingID != "" {
		query := r.store.Rebind(`UPDATE drive_connections SET
			encrypted_refresh_token = ?, key_reference = ?, google_subject = ?,
			status = 'connected', updated_at = ?
			WHERE id = ? AND tenant_id = ? AND user_id = ?`)
		_, err := r.store.db.ExecContext(ctx, query, encrypted,
			r.keyring.ActiveReference(), subject, time.Now().UTC(),
			existingID, scope.TenantID, scope.UserID)
		if err != nil {
			return "", fmt.Errorf("persistence: update drive connection: %w", err)
		}
		return existingID, nil
	}
	if err != nil && !errors.Is(err, ErrNoConnection) {
		return "", err
	}

	id, err := r.ids.New()
	if err != nil {
		return "", fmt.Errorf("persistence: allocate connection id: %w", err)
	}

	now := time.Now().UTC()
	query := r.store.Rebind(`INSERT INTO drive_connections (
		id, tenant_id, user_id, google_subject, encrypted_refresh_token, key_reference,
		granted_scopes, status, created_at, updated_at
	) VALUES (?, ?, ?, ?, ?, ?, ?, 'connected', ?, ?)`)
	_, err = r.store.db.ExecContext(ctx, query, id, scope.TenantID, scope.UserID, subject,
		encrypted, r.keyring.ActiveReference(), "https://www.googleapis.com/auth/drive.file", now, now)
	if err != nil {
		if isUniqueViolation(err) {
			return "", ErrConflict
		}
		return "", fmt.Errorf("persistence: insert drive connection: %w", err)
	}
	return id, nil
}

// UpdateRefreshToken replaces an encrypted refresh token.
//
// It exists for the case where Google rotates a refresh token mid-session: the
// new value is the only one that will ever work, so losing it signs the user out.
func (r *DriveConnectionRepository) UpdateRefreshToken(ctx context.Context, connectionID, refreshToken string) error {
	encrypted, err := r.keyring.Encrypt(r.keyring.ActiveReference(), []byte(refreshToken))
	if err != nil {
		return fmt.Errorf("persistence: encrypt refresh token: %w", err)
	}
	query := r.store.Rebind(`UPDATE drive_connections SET
		encrypted_refresh_token = ?, key_reference = ?, updated_at = ?
	WHERE id = ?`)
	_, err = r.store.db.ExecContext(ctx, query, encrypted, r.keyring.ActiveReference(),
		time.Now().UTC(), connectionID)
	if err != nil {
		return fmt.Errorf("persistence: update refresh token: %w", err)
	}
	return nil
}

// SetStatus changes a connection's status.
func (r *DriveConnectionRepository) SetStatus(ctx context.Context, connectionID, status string) error {
	query := r.store.Rebind(`UPDATE drive_connections SET status = ?, updated_at = ? WHERE id = ?`)
	_, err := r.store.db.ExecContext(ctx, query, status, time.Now().UTC(), connectionID)
	if err != nil {
		return fmt.Errorf("persistence: set connection status: %w", err)
	}
	return nil
}

// SetRootFolderID records the app-managed Drive folder.
func (r *DriveConnectionRepository) SetRootFolderID(ctx context.Context, connectionID, folderID string) error {
	query := r.store.Rebind(`UPDATE drive_connections SET root_folder_id = ?, updated_at = ? WHERE id = ?`)
	_, err := r.store.db.ExecContext(ctx, query, folderID, time.Now().UTC(), connectionID)
	if err != nil {
		return fmt.Errorf("persistence: set root folder: %w", err)
	}
	return nil
}

// SetChangesCursor records how far the Drive changes feed has been processed.
//
// The cursor is only advanced after a page is fully processed. Advancing it
// earlier would silently skip changes and leave the catalog permanently stale,
// which is worse than replaying a page.
func (r *DriveConnectionRepository) SetChangesCursor(ctx context.Context, connectionID, cursor string) error {
	query := r.store.Rebind(`UPDATE drive_connections SET changes_cursor = ?, updated_at = ? WHERE id = ?`)
	_, err := r.store.db.ExecContext(ctx, query, cursor, time.Now().UTC(), connectionID)
	if err != nil {
		return fmt.Errorf("persistence: set changes cursor: %w", err)
	}
	return nil
}

// RecordSuccess stamps the last successful Drive call.
//
// It powers a staleness signal in the status endpoint: a connection that has not
// succeeded in hours may be silently broken even though its status still says
// connected.
func (r *DriveConnectionRepository) RecordSuccess(ctx context.Context, connectionID string) error {
	query := r.store.Rebind(`UPDATE drive_connections SET last_success_at = ?, updated_at = ? WHERE id = ?`)
	_, err := r.store.db.ExecContext(ctx, query, time.Now().UTC(), time.Now().UTC(), connectionID)
	if err != nil {
		return fmt.Errorf("persistence: record success: %w", err)
	}
	return nil
}

// Delete removes a connection row.
//
// It is used by an account deletion request, not by disconnect. Disconnect keeps
// the row so the audit trail of what was connected survives.
func (r *DriveConnectionRepository) Delete(ctx context.Context, connectionID string) error {
	query := r.store.Rebind(`DELETE FROM drive_connections WHERE id = ?`)
	_, err := r.store.db.ExecContext(ctx, query, connectionID)
	if err != nil {
		return fmt.Errorf("persistence: delete drive connection: %w", err)
	}
	return nil
}

// ConnectionByID reads a connection without a tenant scope.
//
// It is for background work, which already knows which connection it is acting on.
// It is deliberately not a way to bypass tenant scoping: a caller still needs the
// connection id, and that id is never accepted from a client.
func (r *DriveConnectionRepository) ConnectionByID(ctx context.Context, connectionID string) (DriveConnection, error) {
	row := r.store.db.QueryRowContext(ctx, r.store.Rebind(`SELECT
		tenant_id, user_id, google_subject, encrypted_refresh_token, status,
		root_folder_id, changes_cursor
		FROM drive_connections WHERE id = ?`), connectionID)

	var (
		out        DriveConnection
		ciphertext []byte
		keyRef     sql.NullString
		folder     sql.NullString
		cursor     sql.NullString
	)
	err := row.Scan(&out.TenantID, &out.UserID, &out.Subject, &ciphertext, &out.Status,
		&folder, &cursor)
	if errors.Is(err, sql.ErrNoRows) {
		return DriveConnection{}, ErrNoConnection
	}
	if err != nil {
		return DriveConnection{}, fmt.Errorf("persistence: get drive connection by id: %w", err)
	}
	out.ID = connectionID
	out.KeyReference = keyRef.String
	out.RootFolderID = nullableString(folder)
	out.ChangesCursor = nullableString(cursor)

	plaintext, err := r.keyring.Decrypt(keyRef.String, ciphertext)
	if err != nil {
		return DriveConnection{}, fmt.Errorf("persistence: decrypt refresh token: %w", err)
	}
	// The record type carries ciphertext and the adapter needs the plaintext, so the
	// decrypted value rides along here rather than being returned separately. It is
	// only ever read by the Drive adapter.
	out.PlaintextRefreshToken = string(plaintext)
	return out, nil
}

// ConnectedConnectionIDs lists the connections that are worth polling.
//
// Only `connected` ones: a revoked or disconnected grant has nothing to poll, and
// calling Drive with a revoked token just produces errors that look like an outage.
func (r *DriveConnectionRepository) ConnectedConnectionIDs(ctx context.Context) ([]string, error) {
	rows, err := r.store.db.QueryContext(ctx,
		`SELECT id FROM drive_connections WHERE status = 'connected' ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("persistence: list connected drives: %w", err)
	}
	defer rows.Close()

	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("persistence: scan connected drive: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("persistence: list connected drives: %w", err)
	}
	return ids, nil
}
