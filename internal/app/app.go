// Package app wires the Storvia service together.
//
// Every constructor here is explicit about what it needs and what it refuses to
// do without. In particular a missing credential set degrades the relevant
// provider to a reporting-only state rather than failing the process, because a
// container that refuses to start cannot report why it is unready.
package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/shovan04/storvia/internal/auth"
	"github.com/shovan04/storvia/internal/cache"
	"github.com/shovan04/storvia/internal/config"
	"github.com/shovan04/storvia/internal/httpapi"
	"github.com/shovan04/storvia/internal/idid"
	"github.com/shovan04/storvia/internal/persistence"
	"github.com/shovan04/storvia/internal/secrets"
	"github.com/shovan04/storvia/internal/storage/provider/drive"
	"github.com/shovan04/storvia/internal/storage/provider/s3compat"
	"github.com/shovan04/storvia/internal/storage/service"
)

// App is the assembled service.
type App struct {
	Config *config.Config
	Logger *slog.Logger
	Store  *persistence.Store
	Cache  cache.Cache
	Server *http.Server

	// Backends holds the constructed S3-compatible clients, keyed by backend ID.
	// It is empty when fallback is disabled.
	Backends map[string]*s3compat.Client
	// secrets holds resolved secret values keyed by environment variable name.
	// It never leaves this struct and is never logged.
	secrets map[string]string
	// Uploads and Objects are the use cases, exposed so tests can drive them
	// without going through HTTP.
	Uploads *service.UploadService
	Objects *service.ObjectService
	// Issues lists degraded-but-started conditions an operator must resolve.
	Issues []string
}

// Options are the inputs to Build.
type Options struct {
	Config *config.Config
	Logger *slog.Logger
}

// Build assembles the service.
//
// It returns an App whose Issues describe anything degraded, plus an error only
// for conditions that make the service unsafe to run at all.
func Build(ctx context.Context, opts Options) (*App, error) {
	cfg := opts.Config
	if cfg == nil {
		return nil, errors.New("app: configuration is required")
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	app := &App{Config: cfg, Logger: logger, Backends: make(map[string]*s3compat.Client)}

	// Every secret is resolved once, here, and fails fast if a required one is
	// absent. Previously each call site read the environment independently, so a
	// missing secret surfaced much later as an authentication failure, or as a
	// fallback backend that was silently skipped while appearing configured.
	resolvedSecrets, err := cfg.ResolveSecrets(os.LookupEnv)
	if err != nil {
		return nil, err
	}
	app.secrets = resolvedSecrets

	store, err := persistence.Open(ctx, cfg.Database, resolvedSecrets[cfg.Database.PasswordRef])
	if err != nil {
		return nil, err
	}
	app.Store = store

	if cfg.Database.AutoMigrate {
		applied, err := store.Migrate(ctx)
		if err != nil {
			store.Close()
			return nil, err
		}
		if applied > 0 {
			logger.InfoContext(ctx, "applied database migrations", "count", applied)
		}
	}

	cacheBackend, err := buildCache(ctx, cfg.Cache, resolvedSecrets)
	if err != nil {
		store.Close()
		return nil, err
	}
	app.Cache = cacheBackend

	backends, issues := buildBackends(ctx, cfg, resolvedSecrets, logger)
	app.Backends = backends
	app.Issues = append(app.Issues, issues...)

	keyring, err := buildKeyring(cfg, resolvedSecrets)
	if err != nil {
		// Without a keyring no refresh token can be stored safely, so this is a
		// hard stop rather than a degraded mode.
		cacheBackend.Close()
		store.Close()
		return nil, err
	}

	driveClient, err := buildDrive(ctx, cfg, store, logger, keyring)
	if err != nil {
		cacheBackend.Close()
		store.Close()
		return nil, err
	}
	if !driveClient.Configured() {
		app.Issues = append(app.Issues,
			"google drive credentials are absent: drive uploads and reads are unavailable until they are set")
	}

	uploads, objects, connections, err := buildServices(cfg, store, keyring, driveClient, backends, logger)
	if err != nil {
		cacheBackend.Close()
		store.Close()
		return nil, err
	}
	app.Uploads = uploads
	app.Objects = objects

	issuer, sessions, err := buildAuth(cfg, resolvedSecrets)
	if err != nil {
		cacheBackend.Close()
		store.Close()
		return nil, err
	}

	admin := httpapi.NewAdminHandler(httpapi.AdminPanelConfig{
		Enabled:     cfg.AdminPanel.Enabled,
		TenantID:    cfg.AdminPanel.TenantID,
		UserID:      cfg.AdminPanel.UserID,
		FlowCookie:  cfg.AdminPanel.FlowCookie,
		RedirectURI: cfg.Drive.AdminRedirectURI,
		Secure:      cfg.Auth.SecureCookie,
	}, issuer, sessions, connections)
	app.Server = buildServer(cfg, logger, store, issuer, sessions, uploads, objects, connections, admin, driveClient, cacheBackend)

	logger.InfoContext(ctx, "storvia service configured",
		"summary", cfg.RedactedSummary(),
		"key_references", keyringReferences(keyring),
		"issues", app.Issues,
	)
	return app, nil
}

// buildCache constructs the configured cache backend.
func buildCache(ctx context.Context, cfg config.CacheConfig, resolved map[string]string) (cache.Cache, error) {
	switch cfg.Driver {
	case config.DriverMemory:
		return cache.NewMemory(cache.WithJanitorInterval(cfg.JanitorInterval.Duration())), nil
	case config.DriverRedis:
		return cache.NewRedis(ctx, cfg.RedisAddress,
			cache.WithPassword(resolved[cfg.RedisPasswordRef]),
			cache.WithDatabase(cfg.RedisDatabase),
			cache.WithClientName("storvia-api"),
		)
	default:
		return nil, fmt.Errorf("app: unsupported cache driver %q", cfg.Driver)
	}
}

// buildBackends constructs the S3-compatible clients.
//
// Credentials come from the environment via the config's env references, which
// were already resolved at load time. A backend that fails to construct is
// reported as an issue and skipped: it must not take the whole service down, and
// it must not silently become a no-op that the routing policy thinks is healthy.
func buildBackends(ctx context.Context, cfg *config.Config, resolved map[string]string, logger *slog.Logger) (map[string]*s3compat.Client, []string) {
	var issues []string
	built := make(map[string]*s3compat.Client)

	if !cfg.Fallback.Enabled {
		return built, issues
	}

	for _, backendCfg := range cfg.Fallback.ActiveBackends() {
		client, err := s3compat.New(ctx, s3compat.Config{
			ID:            backendCfg.ID,
			Vendor:        backendCfg.Vendor,
			Endpoint:      backendCfg.Endpoint,
			Region:        backendCfg.Region,
			Bucket:        backendCfg.Bucket,
			Prefix:        backendCfg.Prefix,
			UsePathStyle:  backendCfg.UsePathStyle,
			PresignTTL:    cfg.Uploads.PresignedURLTTL.Duration(),
			RetryAttempts: 2,
			Credentials: s3compat.Credentials{
				AccessKeyID:     resolved[backendCfg.AccessKeyIDRef],
				SecretAccessKey: resolved[backendCfg.SecretAccessKeyRef],
				SessionToken:    resolved[backendCfg.SessionTokenRef],
			},
		}, s3compat.Options{MultipartTTL: backendCfg.MultipartAbortTTL.Duration()})
		if err != nil {
			issues = append(issues, fmt.Sprintf("fallback backend %q could not be constructed: %v", backendCfg.ID, err))
			logger.ErrorContext(ctx, "fallback backend construction failed",
				"backend_id", backendCfg.ID, "error", err.Error())
			continue
		}
		built[backendCfg.ID] = client
	}
	return built, issues
}

// buildKeyring constructs the encryption keyring.
func buildKeyring(cfg *config.Config, resolved map[string]string) (*secrets.AESGCMKeyring, error) {
	// A missing key was already reported by ResolveSecrets; this is a backstop.
	active := resolved[cfg.Security.EncryptionKeyRef]
	if active == "" {
		return nil, fmt.Errorf("app: the encryption key %q is not set; refresh tokens cannot be stored safely",
			cfg.Security.EncryptionKeyRef)
	}
	keyring, err := secrets.NewAESGCMKeyring(secrets.KeyringOptions{
		ActiveReference:   cfg.Security.EncryptionKeyRef,
		ActiveKey:         active,
		PreviousReference: cfg.Security.EncryptionKeyPrevious,
		PreviousKey:       resolved[cfg.Security.EncryptionKeyPrevious],
	})
	if err != nil {
		return nil, fmt.Errorf("app: encryption keyring: %w", err)
	}
	return keyring, nil
}

// buildDrive constructs the Drive provider.
func buildDrive(ctx context.Context, cfg *config.Config, store *persistence.Store, logger *slog.Logger, keyring secrets.Keyring) (*drive.Client, error) {
	connections := persistence.NewDriveConnectionRepository(store, keyring)

	client, err := drive.New(ctx, drive.Config{
		Credentials: drive.Credentials{
			ClientID:     os.Getenv(cfg.Drive.ClientIDRef),
			ClientSecret: os.Getenv(cfg.Drive.ClientSecretRef),
			RedirectURI:  cfg.Drive.RedirectURI,
		},
		Scopes:           cfg.Drive.Scopes,
		RootFolderName:   cfg.Drive.RootFolderName,
		PreflightTimeout: cfg.Drive.PreflightTimeout.Duration(),
	}, drive.ClientDeps{
		Tokens:   drive.NewMemoryTokens(),
		Resolver: drive.NewResolver(connections),
		States:   drive.NewStateStore(10 * time.Minute),
		Logger:   logger,
	})
	if err != nil {
		return nil, fmt.Errorf("app: drive client: %w", err)
	}
	return client, nil
}

// buildServices constructs the use cases.
func buildServices(cfg *config.Config, store *persistence.Store, keyring secrets.Keyring, driveClient *drive.Client, backends map[string]*s3compat.Client, logger *slog.Logger) (*service.UploadService, *service.ObjectService, *service.ConnectionService, error) {
	objects := persistence.NewObjectRepository(store)
	uploadsRepo := persistence.NewUploadRepository(store)
	attempts := persistence.NewAttemptRepository(store)
	backendRepo := persistence.NewBackendRepository(store)

	// Record the enabled backends so the routing policy can confirm an eligible
	// fallback has a real destination before it selects one.
	for _, backendCfg := range cfg.Fallback.ActiveBackends() {
		if _, ok := backends[backendCfg.ID]; !ok {
			continue
		}
		scopeValue := "deployment"
		err := backendRepo.Upsert(context.Background(), persistence.Backend{
			ID:           backendCfg.ID,
			Scope:        scopeValue,
			Vendor:       backendCfg.Vendor,
			Endpoint:     backendCfg.Endpoint,
			Region:       backendCfg.Region,
			Bucket:       backendCfg.Bucket,
			Prefix:       backendCfg.Prefix,
			Status:       "active",
			Capabilities: "{}",
		})
		if err != nil {
			logger.Warn("could not record the fallback backend", "backend_id", backendCfg.ID, "error", err.Error())
		}
	}

	uploads, err := service.NewUploadService(service.Dependencies{
		Objects:      objects,
		Uploads:      uploadsRepo,
		Attempts:     attempts,
		Backends:     backendRepo,
		Drive:        driveClient,
		UploadLimits: cfg.Uploads,
		DriveCfg:     cfg.Drive,
		Fallback:     cfg.Fallback,
		IDs:          idid.NewGenerator("upl"),
		Logger:       logger,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("app: upload service: %w", err)
	}

	source := newContentSource(store, backends, driveClient, logger)
	objectService := service.NewObjectService(objects, uploadsRepo, source)
	connectionService := service.NewConnectionService(service.NewDriveConnector(driveClient))

	return uploads, objectService, connectionService, nil
}

// buildAuth constructs the session issuer and store.
func buildAuth(cfg *config.Config, resolved map[string]string) (*auth.Issuer, auth.Lookup, error) {
	issuer, err := auth.NewIssuer(auth.IssuerOptions{
		SigningKey: resolved[cfg.Auth.SessionSigningKeyRef],
		CookieName: cfg.Auth.SessionCookieName,
		TTL:        cfg.Auth.SessionTTL.Duration(),
		Secure:     cfg.Auth.SecureCookie,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("app: session issuer: %w", err)
	}
	return issuer, auth.NewMemorySessions(10000), nil
}

// buildServer assembles the HTTP server.
func buildServer(cfg *config.Config, logger *slog.Logger, store *persistence.Store, issuer *auth.Issuer, sessions auth.Lookup, uploads *service.UploadService, objects *service.ObjectService, connections *service.ConnectionService, admin *httpapi.AdminHandler, driveClient *drive.Client, cacheBackend cache.Cache) *http.Server {
	ready := func(ctx context.Context) error {
		// Readiness consults the database. Liveness deliberately does not: a
		// database blip should not get the process restarted into the same blip,
		// which would turn a dependency problem into an outage loop.
		return store.Ping(ctx)
	}

	handler := httpapi.Router(httpapi.Config{
		Issuer:         issuer,
		Sessions:       sessions,
		Logger:         logger,
		Uploads:        httpapi.NewUploadHandler(uploads, driveClient, cfg.Uploads),
		Objects:        httpapi.NewObjectHandler(objects),
		Connections:    httpapi.NewConnectionHandler(connections),
		Admin:          admin,
		Health:         httpapi.NewHealthHandler(ready, cacheBackend),
		AllowedOrigins: cfg.Security.AllowedOrigins,
		UploadTimeout:  cfg.Server.UploadTimeout.Duration(),
		// Metadata requests get the routing budget, which is generous enough for a
		// preflight plus a database round trip.
		MetadataTimeout: cfg.Drive.RoutingBudget.Duration(),
		RateLimit:       600,
		RateInterval:    time.Minute,
	})

	return &http.Server{
		Addr:              cfg.Server.Addr(),
		Handler:           handler,
		ReadHeaderTimeout: cfg.Server.ReadHeaderTimeout.Duration(),
		ReadTimeout:       cfg.Server.ReadTimeout.Duration(),
		WriteTimeout:      cfg.Server.WriteTimeout.Duration(),
		IdleTimeout:       cfg.Server.IdleTimeout.Duration(),
		MaxHeaderBytes:    1 << 20,
	}
}

// keyringReferences lists configured key references for the startup log.
func keyringReferences(keyring *secrets.AESGCMKeyring) []string { return keyring.References() }
