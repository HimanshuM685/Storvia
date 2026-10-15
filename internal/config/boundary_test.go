package config

import (
	"strings"
	"testing"
)

// TestBuildDSNSeparatesSecretsFromOrdinaryConfig is the regression test for the
// configuration boundary.
//
// A complete DSN contains a password, so a whole DSN in a checked-in file is a
// credential in version control. The parameters are therefore split: host, port,
// user, and database are plain values, and only the password is injected.
func TestBuildDSNSeparatesSecretsFromOrdinaryConfig(t *testing.T) {
	cfg := DatabaseConfig{
		Driver:   DriverPostgres,
		Host:     "db.internal",
		Port:     5432,
		User:     "storvia",
		Database: "storvia",
		SSLMode:  "require",
	}

	withPassword := cfg.BuildDSN("hunter2")
	if !strings.Contains(withPassword, "db.internal:5432") {
		t.Errorf("DSN = %q, want the host and port from plain config", withPassword)
	}
	if !strings.Contains(withPassword, "storvia:") {
		t.Errorf("DSN = %q, want the user from plain config", withPassword)
	}
	if !strings.Contains(withPassword, "hunter2") {
		t.Errorf("DSN = %q, want the injected password", withPassword)
	}
	if !strings.Contains(withPassword, "sslmode=require") {
		t.Errorf("DSN = %q, want the sslmode from plain config", withPassword)
	}

	// A password with URL-reserved characters must be escaped, or a legitimate
	// password produces a malformed or wrong-host DSN.
	tricky := cfg.BuildDSN("p@ss:w/rd?")
	if strings.Contains(tricky, "p@ss:w/rd?") {
		t.Errorf("DSN = %q, want the password percent-encoded", tricky)
	}
}

func TestBuildDSNSQLiteUsesThePathAndNoPassword(t *testing.T) {
	cfg := DatabaseConfig{Driver: DriverSQLite, SQLitePath: "file:./storvia.dev.db"}
	if got := cfg.BuildDSN("ignored"); got != "file:./storvia.dev.db" {
		t.Errorf("BuildDSN = %q, want the sqlite path", got)
	}

	empty := DatabaseConfig{Driver: DriverSQLite}
	if got := empty.BuildDSN(""); got != "file::memory:" {
		t.Errorf("BuildDSN with no path = %q, want the in-memory default", got)
	}
}

// TestValidateSecretRefsRejectsACredentialInConfig is the guard that keeps a
// secret out of a JSON file.
//
// The failure mode this prevents is a developer pasting a real key where a variable
// name belongs. The old code accepted it silently: validation only checked that the
// reference string was non-empty, so the resulting configuration never
// authenticated and never said why.
func TestValidateSecretRefsRejectsACredentialInConfig(t *testing.T) {
	tests := []struct {
		name string
		ref  string
	}{
		{"aws access key id", "AKIAIOSFODNN7EXAMPLE"},
		{"aws session key", "ASIAIOSFODNN7EXAMPLE"},
		{"google oauth token", "ya29.a0AfH6SMBxxxxxxxx"},
		{"openai style key", "sk-proj-abc123def456"},
		{"github token", "ghp_16C7e42F292c6912E7710c838347Ae178B4a"},
		{"slack token", "xoxb-123456789012-abcdefghijkl"},
		{"private key header", "-----BEGIN RSA PRIVATE KEY-----"},
		{"long opaque blob", strings.Repeat("a1b2c3d4", 6)},
		{"value with spaces", "my secret key"},
		{"value with a slash", "some/credential/path"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateSecretRefs([]SecretRef{
				{Field: "fallback.backends[b].secret_access_key", EnvVar: tc.ref, Required: true},
			})
			if err == nil {
				t.Fatalf("a credential-shaped value was accepted as an environment reference: %q", tc.ref)
			}
			if !strings.Contains(err.Error(), "never in a JSON config file") {
				t.Errorf("error = %v, want it to explain the boundary", err)
			}
		})
	}
}

// TestValidateSecretRefsErrorDoesNotEchoTheSecret keeps the rejection message from
// reprinting the credential it just rejected.
func TestValidateSecretRefsErrorDoesNotEchoTheSecret(t *testing.T) {
	secret := "AKIAIOSFODNN7EXAMPLE"
	err := ValidateSecretRefs([]SecretRef{
		{Field: "fallback.backends[b].access_key_id", EnvVar: secret, Required: true},
	})
	if err == nil {
		t.Fatal("want an error")
	}
	if strings.Contains(err.Error(), secret) {
		t.Errorf("the error message reprinted the credential: %v", err)
	}
	if !strings.Contains(err.Error(), "fallback.backends[b].access_key_id") {
		t.Errorf("error = %v, want it to name the offending field", err)
	}
}

func TestValidateSecretRefsAcceptsVariableNames(t *testing.T) {
	accepted := []string{
		"STORVIA_GOOGLE_CLIENT_SECRET",
		"STORVIA_ENCRYPTION_KEY",
		"A",
		"_LEADING_UNDERSCORE",
		"lower_case_name",
		"WITH_123_NUMBERS",
	}
	for _, name := range accepted {
		if err := ValidateSecretRefs([]SecretRef{
			{Field: "f", EnvVar: name, Required: true},
		}); err != nil {
			t.Errorf("valid variable name %q was rejected: %v", name, err)
		}
	}
}

func TestValidateSecretRefsRequiresAReferenceWhenRequired(t *testing.T) {
	err := ValidateSecretRefs([]SecretRef{{Field: "security.encryption_key", EnvVar: "", Required: true}})
	if err == nil {
		t.Fatal("a required secret with no reference must be rejected")
	}
	// An optional reference may be absent.
	if err := ValidateSecretRefs([]SecretRef{
		{Field: "drive.client_id", EnvVar: "", Required: false},
	}); err != nil {
		t.Errorf("an optional absent reference must be allowed: %v", err)
	}
}

// TestResolveSecretsFailsFastOnAMissingSecret is the fix for the silent case: a
// missing secret must stop startup, not become an empty credential.
func TestResolveSecretsFailsFastOnAMissingSecret(t *testing.T) {
	cfg := &Config{
		Environment: "test",
		Database:    DatabaseConfig{Driver: DriverPostgres, Host: "h", Port: 5432, User: "u", Database: "d", PasswordRef: "DB_PASSWORD", SSLMode: "require"},
		Auth:        AuthConfig{SessionSigningKeyRef: "SESSION_KEY"},
		Security:    SecurityConfig{EncryptionKeyRef: "ENC_KEY"},
	}

	// All present: resolves cleanly.
	resolved, err := cfg.ResolveSecrets(lookupMap(map[string]string{
		"DB_PASSWORD": "dbpass",
		"SESSION_KEY": "signing-key-that-is-long-enough-here",
		"ENC_KEY":     strings.Repeat("a", 32),
	}))
	if err != nil {
		t.Fatalf("ResolveSecrets: %v", err)
	}
	if resolved["DB_PASSWORD"] != "dbpass" {
		t.Errorf("DB_PASSWORD = %q, want the resolved value", resolved["DB_PASSWORD"])
	}

	// Database password missing: must fail, and name the field.
	_, err = cfg.ResolveSecrets(lookupMap(map[string]string{
		"SESSION_KEY": "signing-key-that-is-long-enough-here",
		"ENC_KEY":     strings.Repeat("a", 32),
	}))
	if err == nil {
		t.Fatal("a missing required database password must fail startup")
	}
	if !strings.Contains(err.Error(), "database.password") {
		t.Errorf("error = %v, want it to name the missing field", err)
	}

	// An empty string counts as missing, not as an empty credential.
	_, err = cfg.ResolveSecrets(lookupMap(map[string]string{
		"DB_PASSWORD": "", "SESSION_KEY": "signing-key-that-is-long-enough-here",
		"ENC_KEY": strings.Repeat("a", 32),
	}))
	if err == nil {
		t.Fatal("an empty secret value must be treated as missing")
	}
}

// TestResolveSecretsOnlyRequiresWhatIsEnabled keeps optional secrets from blocking
// a deployment that does not use the feature they belong to.
func TestResolveSecretsOnlyRequiresWhatIsEnabled(t *testing.T) {
	base := func() *Config {
		return &Config{
			Environment: "test",
			Database:    DatabaseConfig{Driver: DriverSQLite, SQLitePath: "file::memory:"},
			Auth:        AuthConfig{SessionSigningKeyRef: "SESSION_KEY"},
			Security:    SecurityConfig{EncryptionKeyRef: "ENC_KEY"},
			Drive:       DriveConfig{ClientIDRef: "GOOGLE_ID", ClientSecretRef: "GOOGLE_SECRET"},
			Cache:       CacheConfig{Driver: DriverMemory},
		}
	}

	// Google credentials absent is fine: the service starts and reports it.
	if _, err := base().ResolveSecrets(lookupMap(map[string]string{
		"SESSION_KEY": "signing-key-that-is-long-enough-here",
		"ENC_KEY":     strings.Repeat("a", 32),
	})); err != nil {
		t.Errorf("absent optional Google credentials must not fail startup: %v", err)
	}

	// Redis enabled with a password reference means the password is required.
	withRedis := base()
	withRedis.Cache = CacheConfig{Driver: DriverRedis, RedisAddress: "localhost:6379", RedisPasswordRef: "REDIS_PASSWORD"}
	if _, err := withRedis.ResolveSecrets(lookupMap(map[string]string{
		"SESSION_KEY":    "signing-key-that-is-long-enough-here",
		"ENC_KEY":        strings.Repeat("a", 32),
		"REDIS_PASSWORD": "redis-pass",
	})); err != nil {
		t.Errorf("a resolved Redis password must not fail: %v", err)
	}

	// An enabled fallback backend requires both of its credentials.
	withFallback := base()
	withFallback.Fallback = FallbackConfig{
		Enabled: true,
		Backends: []S3BackendConfig{{
			ID: "b1", Vendor: VendorCloudflareR2, Bucket: "bucket",
			AccessKeyIDRef: "S3_ID", SecretAccessKeyRef: "S3_SECRET",
		}},
	}
	_, err := withFallback.ResolveSecrets(lookupMap(map[string]string{
		"SESSION_KEY": "signing-key-that-is-long-enough-here",
		"ENC_KEY":     strings.Repeat("a", 32),
	}))
	if err == nil {
		t.Fatal("an enabled backend with absent credentials must fail startup")
	}
	if !strings.Contains(err.Error(), "fallback.backends[b1]") {
		t.Errorf("error = %v, want it to name the backend", err)
	}

	// A disabled backend does not require its credentials.
	disabled := base()
	disabled.Fallback = FallbackConfig{
		Enabled:  true,
		Backends: []S3BackendConfig{{ID: "b1", Vendor: VendorCloudflareR2, Bucket: "b", Disabled: true, AccessKeyIDRef: "S3_ID", SecretAccessKeyRef: "S3_SECRET"}},
	}
	if _, err := disabled.ResolveSecrets(lookupMap(map[string]string{
		"SESSION_KEY": "signing-key-that-is-long-enough-here",
		"ENC_KEY":     strings.Repeat("a", 32),
	})); err != nil {
		t.Errorf("a disabled backend must not require credentials: %v", err)
	}
}

// TestSecretFieldsTracksTheSameReferences keeps the diagnostic list and the
// resolution list from drifting apart.
func TestSecretFieldsTracksTheSameReferences(t *testing.T) {
	cfg := &Config{
		Database: DatabaseConfig{Driver: DriverPostgres, Host: "h", Port: 5432, User: "u", Database: "d", PasswordRef: "DB_PASSWORD"},
		Auth:     AuthConfig{SessionSigningKeyRef: "SESSION_KEY"},
		Security: SecurityConfig{EncryptionKeyRef: "ENC_KEY", EncryptionKeyPrevious: "ENC_KEY_PREV"},
		Cache:    CacheConfig{Driver: DriverRedis, RedisAddress: "a", RedisPasswordRef: "REDIS_PASSWORD"},
	}

	refs := cfg.SecretRefs()
	fields := cfg.SecretFields()
	if len(refs) != len(fields) {
		t.Fatalf("SecretRefs returned %d and SecretFields returned %d", len(refs), len(fields))
	}
	for i, ref := range refs {
		if fields[i] != ref.Field {
			t.Errorf("field %d = %q, want %q", i, fields[i], ref.Field)
		}
	}

	// The previous encryption key must appear, so rotation is visible in the
	// diagnostic list.
	if !containsField(fields, "security.encryption_key_previous") {
		t.Errorf("fields = %v, want the previous encryption key listed", fields)
	}
}

// TestPostgresRequiresTLSInProduction protects the credential the DSN carries.
func TestPostgresRequiresTLSInProduction(t *testing.T) {
	raw := strings.Replace(minimalConfig(), `"driver": "sqlite", "sqlite_path": "file::memory:"`,
		`"driver": "postgres", "host": "db", "port": 5432, "user": "storvia",
         "database": "storvia", "password_ref": "DB_PASSWORD", "sslmode": "disable"`, 1)
	raw = strings.Replace(raw, `"environment": "test"`, `"environment": "production"`, 1)

	_, err := Parse([]byte(raw), lookupMap(map[string]string{"DB_PASSWORD": "pass"}))
	if err == nil {
		t.Fatal("a production Postgres deployment with sslmode=disable must be rejected")
	}
	if !strings.Contains(err.Error(), "sslmode") {
		t.Errorf("error = %v, want it to explain the sslmode requirement", err)
	}
}

// TestPostgresRequiresEveryOrdinaryParameter checks that the split did not make
// anything optional that is actually needed.
func TestPostgresRequiresEveryOrdinaryParameter(t *testing.T) {
	tests := []struct {
		name    string
		drop    string
		wantErr string
	}{
		{"no host", `"host": "db",`, "database.host"},
		{"no user", `"user": "storvia",`, "database.user"},
		{"no database", `"database": "storvia",`, "database.database"},
		{"no password ref", `"password_ref": "DB_PASSWORD",`, "database.password_ref"},
	}
	full := `"driver": "postgres", "host": "db", "port": 5432, "user": "storvia",
         "database": "storvia", "password_ref": "DB_PASSWORD", "sslmode": "require"`

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			raw := strings.Replace(minimalConfig(), `"driver": "sqlite", "sqlite_path": "file::memory:"`, full, 1)
			raw = strings.Replace(raw, tc.drop, "", 1)
			_, err := Parse([]byte(raw), lookupMap(map[string]string{"DB_PASSWORD": "pass"}))
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

// TestAuthSigningKeyRefDefaults documents the default while keeping the variable
// name in configuration rather than hardcoded in Go.
func TestAuthSigningKeyRefDefaults(t *testing.T) {
	cfg, err := Parse([]byte(minimalConfig()), lookupMap(nil))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Auth.SessionSigningKeyRef != "STORVIA_SESSION_SIGNING_KEY" {
		t.Errorf("SessionSigningKeyRef = %q, want the documented default", cfg.Auth.SessionSigningKeyRef)
	}
}

func containsField(fields []string, want string) bool {
	for _, field := range fields {
		if field == want {
			return true
		}
	}
	return false
}
