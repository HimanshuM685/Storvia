// Package config loads Storvia's JSON configuration and resolves every secret
// reference from the process environment.
//
// The split is deliberate and is the project's rule: JSON files hold structure
// and non-secret values and are safe to review and commit, while .env files hold
// API keys, database passwords, and signing material and never enter version
// control. A JSON string of the form ${VAR} or ${VAR:-fallback} is resolved at
// load time, which keeps a config file readable without ever hard-coding a
// credential in it.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// Config is the whole service configuration.
type Config struct {
	Environment string          `json:"environment"`
	Service     ServiceConfig   `json:"service"`
	Server      ServerConfig    `json:"server"`
	Logging     LoggingConfig   `json:"logging"`
	Database    DatabaseConfig  `json:"database"`
	Cache       CacheConfig     `json:"cache"`
	Auth        AuthConfig      `json:"auth"`
	Security    SecurityConfig  `json:"security"`
	Uploads     UploadsConfig   `json:"uploads"`
	Drive       DriveConfig     `json:"drive"`
	Fallback    FallbackConfig  `json:"fallback"`
	Telemetry   TelemetryConfig `json:"telemetry"`
}

// ServiceConfig carries build identity stamped onto every log record.
type ServiceConfig struct {
	Name    string `json:"name"`
	Release string `json:"release"`
}

// ServerConfig controls the HTTP listener and its timeouts.
//
// The upload deadline is intentionally separate from the routing budget: a slow
// client pushing a large file must never be classified as a Drive outage.
type ServerConfig struct {
	Host               string   `json:"host"`
	Port               int      `json:"port"`
	ReadHeaderTimeout  Duration `json:"read_header_timeout"`
	ReadTimeout        Duration `json:"read_timeout"`
	WriteTimeout       Duration `json:"write_timeout"`
	IdleTimeout        Duration `json:"idle_timeout"`
	UploadTimeout      Duration `json:"upload_timeout"`
	DownloadTimeout    Duration `json:"download_timeout"`
	ShutdownGrace      Duration `json:"shutdown_grace"`
	MaxRequestBodySize int64    `json:"max_request_body_size"`
	TrustedProxies     []string `json:"trusted_proxies"`
}

// Addr returns the listen address.
func (s ServerConfig) Addr() string { return fmt.Sprintf("%s:%d", s.Host, s.Port) }

// LoggingConfig controls slog output.
type LoggingConfig struct {
	Level  string `json:"level"`
	Format string `json:"format"`
}

// DatabaseConfig selects the relational metadata store.
//
// SQLite is for development and single-node evaluation; PostgreSQL is the
// production target. The DSN is always a secret reference.
type DatabaseConfig struct {
	Driver          string   `json:"driver"`
	DSN             string   `json:"dsn"`
	MaxOpenConns    int      `json:"max_open_conns"`
	MaxIdleConns    int      `json:"max_idle_conns"`
	ConnMaxLifetime Duration `json:"conn_max_lifetime"`
	ConnMaxIdleTime Duration `json:"conn_max_idle_time"`
	AutoMigrate     bool     `json:"auto_migrate"`
	MigrationsDir   string   `json:"migrations_dir"`
}

// DriverSQLite and DriverPostgres are the accepted database drivers.
const (
	DriverSQLite   = "sqlite"
	DriverPostgres = "postgres"
)

// CacheConfig selects the cache backend. Memory is the default; Redis is for
// multi-instance deployments that must share token and circuit state.
type CacheConfig struct {
	Driver            string   `json:"driver"`
	RedisAddress      string   `json:"redis_address"`
	RedisPassword     string   `json:"redis_password"`
	RedisDatabase     int      `json:"redis_database"`
	JanitorInterval   Duration `json:"janitor_interval"`
	AccessTokenTTL    Duration `json:"access_token_ttl"`
	PreflightTTL      Duration `json:"preflight_ttl"`
	CircuitStateTTL   Duration `json:"circuit_state_ttl"`
	AvailabilityScope string   `json:"availability_scope"`
}

// DriverMemory and DriverRedis are the accepted cache drivers.
const (
	DriverMemory = "memory"
	DriverRedis  = "redis"
)

// AuthConfig controls application sign-in. Application identity is deliberately
// separate from Google Drive authorization.
type AuthConfig struct {
	SessionCookieName string   `json:"session_cookie_name"`
	SessionTTL        Duration `json:"session_ttl"`
	SecureCookie      bool     `json:"secure_cookie"`
	TrustedProxies    []string `json:"trusted_proxies"`
}

// SecurityConfig holds references to key material. Values arrive from the
// environment and are never written back to a config file or a log.
type SecurityConfig struct {
	EncryptionKeyRef       string   `json:"encryption_key_ref"`
	EncryptionKeyPrevious  string   `json:"encryption_key_previous_ref"`
	AllowedOrigins         []string `json:"allowed_origins"`
	AllowedEndpointSchemes []string `json:"allowed_endpoint_schemes"`
	AllowedEndpointHosts   []string `json:"allowed_endpoint_hosts"`
	MaxClockSkew           Duration `json:"max_clock_skew"`
}

// UploadsConfig bounds what a tenant may store and how transfer proceeds.
type UploadsConfig struct {
	MaxFileSizeBytes    int64            `json:"max_file_size_bytes"`
	AllowedMIMETypes    []string         `json:"allowed_mime_types"`
	AllowedCategories   []string         `json:"allowed_categories"`
	DriveChunkSizeBytes int              `json:"drive_chunk_size_bytes"`
	S3PartSizeBytes     int              `json:"s3_part_size_bytes"`
	PresignedURLTTL     Duration         `json:"presigned_url_ttl"`
	PresignedPartCount  int              `json:"presigned_part_count"`
	MinPartSizeBytes    int              `json:"min_part_size_bytes"`
	CategoryLimits      map[string]int64 `json:"category_limits"`
}

// CategoryAllowed reports whether category is accepted.
func (u UploadsConfig) CategoryAllowed(category string) bool {
	for _, allowed := range u.AllowedCategories {
		if allowed == category {
			return true
		}
	}
	return false
}

// MIMEAllowed reports whether mimeType passes the allow-list. Client-declared
// MIME is untrusted metadata used for routing and download headers only; it is
// never treated as content validation.
func (u UploadsConfig) MIMEAllowed(mimeType string) bool {
	if len(u.AllowedMIMETypes) == 0 {
		return false
	}
	for _, allowed := range u.AllowedMIMETypes {
		if strings.EqualFold(allowed, mimeType) {
			return true
		}
	}
	return false
}

// EffectiveMaxSize returns the category-specific limit, falling back to the
// global maximum when the category has none.
func (u UploadsConfig) EffectiveMaxSize(category string) int64 {
	if limit, ok := u.CategoryLimits[category]; ok && limit > 0 && limit < u.MaxFileSizeBytes {
		return limit
	}
	return u.MaxFileSizeBytes
}

// DriveConfig holds Google OAuth settings and the availability routing budget.
//
// Every secret is an environment reference. The narrow drive.file scope is the
// only scope the MVP requests.
type DriveConfig struct {
	ClientIDRef       string   `json:"client_id_ref"`
	ClientSecretRef   string   `json:"client_secret_ref"`
	RedirectURI       string   `json:"redirect_uri"`
	Scopes            []string `json:"scopes"`
	RootFolderName    string   `json:"root_folder_name"`
	PreflightTimeout  Duration `json:"preflight_timeout"`
	PreflightAttempts int      `json:"preflight_attempts"`
	RoutingBudget     Duration `json:"routing_budget"`
	CircuitFailures   int      `json:"circuit_failures"`
	CircuitCooldown   Duration `json:"circuit_cooldown"`
}

// FallbackConfig lists the S3-compatible backends eligible for outage uploads.
// One backend is active per deployment at first; selection is operator
// configuration and never client supplied.
type FallbackConfig struct {
	Enabled         bool              `json:"enabled"`
	ConsentRequired bool              `json:"consent_required"`
	Backends        []S3BackendConfig `json:"backends"`
}

// S3BackendConfig is one S3-compatible destination.
//
// Vendor is informational and selects capability behaviour; the adapter targets
// the common S3 subset only. Endpoint and credentials are operator configuration,
// which is what prevents a client from steering writes at an arbitrary host.
type S3BackendConfig struct {
	ID                 string   `json:"id"`
	Vendor             string   `json:"vendor"`
	Endpoint           string   `json:"endpoint"`
	Region             string   `json:"region"`
	Bucket             string   `json:"bucket"`
	Prefix             string   `json:"prefix"`
	AccessKeyIDRef     string   `json:"access_key_id_ref"`
	SecretAccessKeyRef string   `json:"secret_access_key_ref"`
	SessionTokenRef    string   `json:"session_token_ref"`
	UsePathStyle       bool     `json:"use_path_style"`
	Disabled           bool     `json:"disabled"`
	MultipartAbortTTL  Duration `json:"multipart_abort_ttl"`
	OrphanGracePeriod  Duration `json:"orphan_grace_period"`
}

// VendorCloudflareR2 and VendorAWSS3 are the vendors Storvia recognizes.
const (
	VendorCloudflareR2 = "cloudflare_r2"
	VendorAWSS3        = "aws_s3"
)

// ActiveBackends returns the backends that are neither disabled nor missing a
// required field.
func (f FallbackConfig) ActiveBackends() []S3BackendConfig {
	out := make([]S3BackendConfig, 0, len(f.Backends))
	for _, b := range f.Backends {
		if b.Disabled || b.Bucket == "" || b.AccessKeyIDRef == "" {
			continue
		}
		out = append(out, b)
	}
	return out
}

// TelemetryConfig controls metrics and health endpoints.
type TelemetryConfig struct {
	MetricsEnabled   bool     `json:"metrics_enabled"`
	MetricsPath      string   `json:"metrics_path"`
	HealthPath       string   `json:"health_path"`
	ReadinessTimeout Duration `json:"readiness_timeout"`
}

// envPattern matches ${NAME} and ${NAME:-default}.
var envPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?::-([^}]*))?\}`)

// ExpandEnv replaces ${NAME} and ${NAME:-default} in raw with values from lookup.
//
// A reference with no value and no default is an error, not an empty string: a
// silently empty access key is how a fallback backend ends up misconfigured in
// production.
func ExpandEnv(raw []byte, lookup func(string) (string, bool)) ([]byte, error) {
	var missing []string
	out := envPattern.ReplaceAllFunc(raw, func(match []byte) []byte {
		groups := envPattern.FindSubmatch(match)
		name := string(groups[1])
		if value, ok := lookup(name); ok && value != "" {
			return []byte(value)
		}
		if groups[2] != nil {
			return groups[2]
		}
		missing = append(missing, name)
		return nil
	})
	if len(missing) > 0 {
		return nil, fmt.Errorf("config: unresolved environment reference(s): %s",
			strings.Join(dedupe(missing), ", "))
	}
	return out, nil
}

func dedupe(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// Load reads and validates a JSON configuration file.
//
// Unknown fields are rejected. A typo in a security-relevant key must fail at
// startup rather than silently falling back to a zero value that disables a
// control.
func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read %s: %w", path, err)
	}
	return Parse(raw, os.LookupEnv)
}

// Parse decodes configuration from raw bytes, resolving environment references
// through lookup. Tests use it directly with a map-backed lookup.
func Parse(raw []byte, lookup func(string) (string, bool)) (*Config, error) {
	expanded, err := ExpandEnv(raw, lookup)
	if err != nil {
		return nil, err
	}

	var cfg Config
	decoder := json.NewDecoder(bytes.NewReader(expanded))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&cfg); err != nil {
		return nil, fmt.Errorf("config: decode: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// applyDefaults fills optional values. It never invents a secret: a missing
// credential must stay missing so Validate can report it.
func (c *Config) applyDefaults() {
	if c.Service.Name == "" {
		c.Service.Name = "storvia-api"
	}
	if c.Server.Host == "" {
		c.Server.Host = "127.0.0.1"
	}
	if c.Server.Port == 0 {
		c.Server.Port = 8080
	}
	if c.Server.ReadHeaderTimeout == 0 {
		c.Server.ReadHeaderTimeout = Duration(10 * time.Second)
	}
	if c.Server.ReadTimeout == 0 {
		c.Server.ReadTimeout = Duration(60 * time.Second)
	}
	if c.Server.WriteTimeout == 0 {
		c.Server.WriteTimeout = 0 // streaming downloads manage their own deadline
	}
	if c.Server.IdleTimeout == 0 {
		c.Server.IdleTimeout = Duration(120 * time.Second)
	}
	if c.Server.UploadTimeout == 0 {
		c.Server.UploadTimeout = Duration(30 * time.Minute)
	}
	if c.Server.DownloadTimeout == 0 {
		c.Server.DownloadTimeout = Duration(15 * time.Minute)
	}
	if c.Server.ShutdownGrace == 0 {
		c.Server.ShutdownGrace = Duration(30 * time.Second)
	}
	if c.Server.MaxRequestBodySize == 0 {
		c.Server.MaxRequestBodySize = 2 << 20
	}
	if c.Logging.Level == "" {
		c.Logging.Level = "info"
	}
	if c.Logging.Format == "" {
		c.Logging.Format = "json"
	}
	if c.Database.MaxOpenConns == 0 {
		c.Database.MaxOpenConns = 10
	}
	if c.Database.MaxIdleConns == 0 {
		c.Database.MaxIdleConns = 5
	}
	if c.Database.ConnMaxLifetime == 0 {
		c.Database.ConnMaxLifetime = Duration(30 * time.Minute)
	}
	if c.Database.ConnMaxIdleTime == 0 {
		c.Database.ConnMaxIdleTime = Duration(5 * time.Minute)
	}
	if c.Database.MigrationsDir == "" {
		c.Database.MigrationsDir = "migrations"
	}
	if c.Cache.Driver == "" {
		c.Cache.Driver = DriverMemory
	}
	if c.Cache.JanitorInterval == 0 {
		c.Cache.JanitorInterval = Duration(time.Minute)
	}
	if c.Cache.AccessTokenTTL == 0 {
		// Deliberately under an hour: an access token outliving its expiry in
		// the cache would turn a revocation into a delayed revocation.
		c.Cache.AccessTokenTTL = Duration(30 * time.Minute)
	}
	if c.Cache.PreflightTTL == 0 {
		c.Cache.PreflightTTL = Duration(5 * time.Second)
	}
	if c.Cache.CircuitStateTTL == 0 {
		c.Cache.CircuitStateTTL = Duration(2 * time.Minute)
	}
	if c.Auth.SessionCookieName == "" {
		c.Auth.SessionCookieName = "storvia_session"
	}
	if c.Auth.SessionTTL == 0 {
		c.Auth.SessionTTL = Duration(12 * time.Hour)
	}
	if c.Security.MaxClockSkew == 0 {
		c.Security.MaxClockSkew = Duration(2 * time.Minute)
	}
	if len(c.Security.AllowedEndpointSchemes) == 0 {
		c.Security.AllowedEndpointSchemes = []string{"https"}
	}
	if c.Uploads.MaxFileSizeBytes == 0 {
		c.Uploads.MaxFileSizeBytes = 5 << 30 // 5 GiB
	}
	if c.Uploads.DriveChunkSizeBytes == 0 {
		// 8 MiB: a multiple of Drive's 256 KiB chunk requirement with enough
		// headroom to keep request count low.
		c.Uploads.DriveChunkSizeBytes = 8 << 20
	}
	if c.Uploads.S3PartSizeBytes == 0 {
		c.Uploads.S3PartSizeBytes = 8 << 20
	}
	if c.Uploads.MinPartSizeBytes == 0 {
		c.Uploads.MinPartSizeBytes = 5 << 20
	}
	if c.Uploads.PresignedURLTTL == 0 {
		c.Uploads.PresignedURLTTL = Duration(15 * time.Minute)
	}
	if c.Uploads.PresignedPartCount == 0 {
		c.Uploads.PresignedPartCount = 8
	}
	if c.Drive.RootFolderName == "" {
		c.Drive.RootFolderName = "Storvia"
	}
	if len(c.Drive.Scopes) == 0 {
		c.Drive.Scopes = []string{"https://www.googleapis.com/auth/drive.file"}
	}
	if c.Drive.PreflightTimeout == 0 {
		c.Drive.PreflightTimeout = Duration(3 * time.Second)
	}
	if c.Drive.PreflightAttempts == 0 {
		c.Drive.PreflightAttempts = 2
	}
	if c.Drive.RoutingBudget == 0 {
		c.Drive.RoutingBudget = Duration(8 * time.Second)
	}
	if c.Drive.CircuitFailures == 0 {
		c.Drive.CircuitFailures = 2
	}
	if c.Drive.CircuitCooldown == 0 {
		c.Drive.CircuitCooldown = Duration(30 * time.Second)
	}
	if c.Telemetry.HealthPath == "" {
		c.Telemetry.HealthPath = "/healthz"
	}
	if c.Telemetry.MetricsPath == "" {
		c.Telemetry.MetricsPath = "/metrics"
	}
	if c.Telemetry.ReadinessTimeout == 0 {
		c.Telemetry.ReadinessTimeout = Duration(3 * time.Second)
	}
}

// Validate reports configuration that would be unsafe or non-functional.
func (c *Config) Validate() error {
	if err := c.validateDatabase(); err != nil {
		return err
	}
	if err := c.validateCache(); err != nil {
		return err
	}
	if err := c.validateDrive(); err != nil {
		return err
	}
	if err := c.validateFallback(); err != nil {
		return err
	}
	if err := c.validateUploads(); err != nil {
		return err
	}
	return nil
}

func (c *Config) validateDatabase() error {
	switch c.Database.Driver {
	case DriverSQLite:
		// SQLite is a development driver. One writer at a time avoids
		// SQLITE_BUSY under concurrent uploads; the job-lease queries use the
		// dialect's own conditional update instead of SKIP LOCKED.
		if c.Database.MaxOpenConns > 1 {
			c.Database.MaxOpenConns = 1
		}
	case DriverPostgres:
		if c.Database.DSN == "" {
			return fmt.Errorf("config: database.dsn is required for the %s driver", DriverPostgres)
		}
	case "":
		return fmt.Errorf("config: database.driver is required (use %q or %q)", DriverSQLite, DriverPostgres)
	default:
		return fmt.Errorf("config: unsupported database.driver %q", c.Database.Driver)
	}
	if c.Database.Driver == DriverSQLite && c.Database.DSN == "" {
		return fmt.Errorf("config: database.dsn is required for the %s driver", DriverSQLite)
	}
	return nil
}

func (c *Config) validateCache() error {
	switch c.Cache.Driver {
	case DriverMemory:
		return nil
	case DriverRedis:
		if c.Cache.RedisAddress == "" {
			return fmt.Errorf("config: cache.redis_address is required for the %s driver", DriverRedis)
		}
		return nil
	case "":
		return fmt.Errorf("config: cache.driver is required (use %q or %q)", DriverMemory, DriverRedis)
	default:
		return fmt.Errorf("config: unsupported cache.driver %q", c.Cache.Driver)
	}
}

func (c *Config) validateDrive() error {
	// The narrow per-file scope is the MVP contract. A broader scope in a
	// config file is a stop-the-line error, not a warning.
	for _, scope := range c.Drive.Scopes {
		if scope != "https://www.googleapis.com/auth/drive.file" {
			return fmt.Errorf("config: drive.scopes may only contain the drive.file scope in this release, got %q", scope)
		}
	}
	if c.Drive.PreflightAttempts < 1 {
		return fmt.Errorf("config: drive.preflight_attempts must be at least 1")
	}
	if c.Drive.PreflightTimeout <= 0 || c.Drive.RoutingBudget <= 0 {
		return fmt.Errorf("config: drive preflight timeouts must be positive")
	}
	// A single attempt must fit inside the total routing budget, otherwise the
	// budget silently truncates the retry loop the docs require.
	if time.Duration(c.Drive.PreflightAttempts)*c.Drive.PreflightTimeout.Duration() > c.Drive.RoutingBudget.Duration() {
		return fmt.Errorf("config: drive.preflight_attempts x preflight_timeout exceeds drive.routing_budget")
	}
	if c.Drive.CircuitFailures < 1 || c.Drive.CircuitCooldown <= 0 {
		return fmt.Errorf("config: drive circuit thresholds must be positive")
	}
	return nil
}

func (c *Config) validateFallback() error {
	if !c.Fallback.Enabled {
		return nil
	}
	if c.Fallback.ConsentRequired {
		return fmt.Errorf("config: fallback.consent_required must be false; Storvia refuses to enable outage uploads until the disclosure is accepted")
	}
	if len(c.Fallback.Backends) == 0 {
		return fmt.Errorf("config: fallback.enabled requires at least one backend")
	}
	seen := make(map[string]struct{}, len(c.Fallback.Backends))
	for _, b := range c.Fallback.Backends {
		if b.ID == "" {
			return fmt.Errorf("config: every fallback backend requires an id")
		}
		if _, dup := seen[b.ID]; dup {
			return fmt.Errorf("config: duplicate fallback backend id %q", b.ID)
		}
		seen[b.ID] = struct{}{}
		switch b.Vendor {
		case VendorCloudflareR2, VendorAWSS3:
		default:
			return fmt.Errorf("config: unsupported fallback vendor %q for backend %q", b.Vendor, b.ID)
		}
		if b.Disabled {
			continue
		}
		if b.Bucket == "" {
			return fmt.Errorf("config: fallback backend %q requires a bucket", b.ID)
		}
		if b.AccessKeyIDRef == "" || b.SecretAccessKeyRef == "" {
			return fmt.Errorf("config: fallback backend %q requires credential references", b.ID)
		}
		if b.Endpoint != "" && !c.endpointAllowed(b.Endpoint) {
			return fmt.Errorf("config: fallback backend %q endpoint is not permitted by security.allowed_endpoint_schemes/hosts", b.ID)
		}
	}
	return nil
}

// endpointAllowed enforces the operator allow-list that stops a misconfigured or
// tampered endpoint from pointing Storvia at an internal or hostile host.
func (c *Config) endpointAllowed(endpoint string) bool {
	normalized := endpoint
	for _, scheme := range c.Security.AllowedEndpointSchemes {
		if strings.HasPrefix(normalized, scheme+"://") {
			normalized = strings.TrimPrefix(normalized, scheme+"://")
			goto schemeOk
		}
	}
	return false

schemeOk:
	if idx := strings.IndexAny(normalized, "/?#"); idx >= 0 {
		normalized = normalized[:idx]
	}
	if idx := strings.LastIndex(normalized, ":"); idx >= 0 && !strings.Contains(normalized[idx:], "]") {
		normalized = normalized[:idx]
	}
	normalized = strings.ToLower(normalized)
	if len(c.Security.AllowedEndpointHosts) == 0 {
		return true
	}
	for _, allowed := range c.Security.AllowedEndpointHosts {
		if strings.EqualFold(allowed, normalized) {
			return true
		}
	}
	return false
}

func (c *Config) validateUploads() error {
	if c.Uploads.MaxFileSizeBytes <= 0 {
		return fmt.Errorf("config: uploads.max_file_size_bytes must be positive")
	}
	if len(c.Uploads.AllowedMIMETypes) == 0 {
		return fmt.Errorf("config: uploads.allowed_mime_types must not be empty; Storvia does not accept arbitrary uploads")
	}
	if len(c.Uploads.AllowedCategories) == 0 {
		return fmt.Errorf("config: uploads.allowed_categories must not be empty")
	}
	for category, limit := range c.Uploads.CategoryLimits {
		if limit <= 0 {
			return fmt.Errorf("config: uploads.category_limits[%q] must be positive", category)
		}
		if limit > c.Uploads.MaxFileSizeBytes {
			return fmt.Errorf("config: uploads.category_limits[%q] exceeds uploads.max_file_size_bytes", category)
		}
	}
	// Drive requires chunks to be a multiple of 256 KiB.
	const driveChunkMultiple = 256 << 10
	if c.Uploads.DriveChunkSizeBytes%driveChunkMultiple != 0 {
		return fmt.Errorf("config: uploads.drive_chunk_size_bytes must be a multiple of %d", driveChunkMultiple)
	}
	if c.Uploads.S3PartSizeBytes < c.Uploads.MinPartSizeBytes {
		return fmt.Errorf("config: uploads.s3_part_size_bytes must be at least uploads.min_part_size_bytes")
	}
	return nil
}

// SecretFields returns the dotted paths of every field holding resolved secret
// material. Diagnostics print these paths with a redacted marker so an operator
// can confirm a key arrived without seeing its value.
func (c *Config) SecretFields() []string {
	paths := []string{
		"database.dsn",
		"cache.redis_password",
		"drive.client_id",
		"drive.client_secret",
		"security.encryption_key",
	}
	for _, b := range c.Fallback.Backends {
		paths = append(paths,
			"fallback.backends["+b.ID+"].access_key_id",
			"fallback.backends["+b.ID+"].secret_access_key",
		)
	}
	return paths
}

// RedactedSummary renders a startup summary with no secret values, used by the
// bootstrap log line.
func (c *Config) RedactedSummary() map[string]any {
	return map[string]any{
		"environment":      c.Environment,
		"service":          c.Service.Name,
		"release":          c.Service.Release,
		"listen":           c.Server.Addr(),
		"database_driver":  c.Database.Driver,
		"cache_driver":     c.Cache.Driver,
		"drive_scopes":     c.Drive.Scopes,
		"fallback_enabled": c.Fallback.Enabled,
		"fallback_vendors": c.FallbackActiveVendors(),
		"upload_max_bytes": c.Uploads.MaxFileSizeBytes,
		"routing_budget":   c.Drive.RoutingBudget.String(),
	}
}

// FallbackActiveVendors lists the vendors of backends eligible for outage use.
func (c *Config) FallbackActiveVendors() []string {
	out := make([]string, 0, len(c.Fallback.Backends))
	for _, b := range c.Fallback.ActiveBackends() {
		out = append(out, b.Vendor)
	}
	return out
}
