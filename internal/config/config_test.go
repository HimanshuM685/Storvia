package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// lookupMap builds a lookup function from a map.
func lookupMap(values map[string]string) func(string) (string, bool) {
	return func(name string) (string, bool) {
		value, ok := values[name]
		return value, ok
	}
}

// minimalConfig is the smallest JSON that passes validation. Tests override one
// thing at a time so a failure points at exactly the rule under test.
func minimalConfig() string {
	return `{
      "environment": "test",
      "database": {"driver": "sqlite", "sqlite_path": "file::memory:"},
      "cache": {"driver": "memory"},
      "drive": {
        "redirect_uri": "http://127.0.0.1:8080/callback",
        "scopes": ["https://www.googleapis.com/auth/drive.file"]
      },
      "fallback": {"enabled": false},
      "uploads": {
        "max_file_size_bytes": 1048576,
        "allowed_mime_types": ["application/pdf"],
        "allowed_categories": ["report"]
      }
    }`
}

func TestParseMinimalConfigAppliesDefaults(t *testing.T) {
	cfg, err := Parse([]byte(minimalConfig()), lookupMap(map[string]string{
		EnvGoogleClientID:     "id",
		EnvGoogleClientSecret: "secret",
	}))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"server.host", cfg.Server.Host, "127.0.0.1"},
		{"server.port", cfg.Server.Port, 8080},
		{"cache.driver", cfg.Cache.Driver, DriverMemory},
		{"drive.root_folder_name", cfg.Drive.RootFolderName, "Storvia"},
		{"drive.preflight_attempts", cfg.Drive.PreflightAttempts, 2},
		{"drive.circuit_failures", cfg.Drive.CircuitFailures, 2},
		{"drive.redirect_uri kept", cfg.Drive.RedirectURI, "http://127.0.0.1:8080/callback"},
		{"logging.level", cfg.Logging.Level, "info"},
		{"logging.format", cfg.Logging.Format, "json"},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}

	// Durations must land as the documented values, not as their raw nanoseconds.
	if got := cfg.Drive.RoutingBudget.Duration().String(); got != "8s" {
		t.Errorf("routing budget = %s, want 8s", got)
	}
	if got := cfg.Drive.PreflightTimeout.Duration().String(); got != "3s" {
		t.Errorf("preflight timeout = %s, want 3s", got)
	}
}

func TestExpandEnvResolvesReferences(t *testing.T) {
	tests := []struct {
		name    string
		raw     string
		env     map[string]string
		want    string
		wantErr string
	}{
		{
			name: "plain reference",
			raw:  `{"a":"${SECRET}"}`,
			env:  map[string]string{"SECRET": "abc123"},
			want: `{"a":"abc123"}`,
		},
		{
			name: "reference with default",
			raw:  `{"a":"${MISSING:-fallback}"}`,
			env:  map[string]string{},
			want: `{"a":"fallback"}`,
		},
		{
			name: "present value wins over the default",
			raw:  `{"a":"${SECRET:-fallback}"}`,
			env:  map[string]string{"SECRET": "real"},
			want: `{"a":"real"}`,
		},
		{
			name:    "missing reference without a default is an error",
			raw:     `{"a":"${MISSING}"}`,
			env:     map[string]string{},
			wantErr: "MISSING",
		},
		{
			name:    "an empty value counts as missing",
			raw:     `{"a":"${EMPTY}"}`,
			env:     map[string]string{"EMPTY": ""},
			wantErr: "EMPTY",
		},
		{
			name: "multiple references resolve together",
			raw:  `{"a":"${A}","b":"${B}"}`,
			env:  map[string]string{"A": "1", "B": "2"},
			want: `{"a":"1","b":"2"}`,
		},
		{
			name: "text without references is untouched",
			raw:  `{"a":"plain"}`,
			env:  map[string]string{},
			want: `{"a":"plain"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExpandEnv([]byte(tc.raw), lookupMap(tc.env))
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("ExpandEnv = %q, want an error mentioning %q", got, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %v, want it to mention %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ExpandEnv: %v", err)
			}
			if string(got) != tc.want {
				t.Errorf("ExpandEnv = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestExpandEnvDoesNotLeakSecretInError is the check that a missing key names the
// variable without printing the value of any other.
func TestExpandEnvDoesNotLeakSecretInError(t *testing.T) {
	_, err := ExpandEnv([]byte(`{"a":"${PRESENT}","b":"${ABSENT}"}`),
		lookupMap(map[string]string{"PRESENT": "super-secret-value"}))
	if err == nil {
		t.Fatal("want an error for the absent reference")
	}
	if strings.Contains(err.Error(), "super-secret-value") {
		t.Error("the error message leaked a resolved secret value")
	}
}

func TestParseRejectsUnknownFields(t *testing.T) {
	raw := strings.Replace(minimalConfig(), `"environment": "test"`, `"environment": "test", "typo_field": true`, 1)
	_, err := Parse([]byte(raw), lookupMap(nil))
	if err == nil {
		t.Fatal("an unknown field must be rejected: a typo in a security key must fail loudly")
	}
	if !strings.Contains(err.Error(), "typo_field") {
		t.Errorf("error = %v, want it to name the unknown field", err)
	}
}

func TestValidateRejectsBroadDriveScope(t *testing.T) {
	raw := strings.Replace(minimalConfig(),
		`"scopes": ["https://www.googleapis.com/auth/drive.file"]`,
		`"scopes": ["https://www.googleapis.com/auth/drive"]`, 1)

	_, err := Parse([]byte(raw), lookupMap(nil))
	if err == nil {
		t.Fatal("the full drive scope must be rejected in this release")
	}
	if !strings.Contains(err.Error(), "drive.file") {
		t.Errorf("error = %v, want it to explain the scope restriction", err)
	}
}

func TestValidateRejectsEnabledAdminPanelOutsideDevelopment(t *testing.T) {
	raw := strings.Replace(minimalConfig(), `"environment": "test"`, `"environment": "production"`, 1)
	raw = strings.Replace(raw, `"fallback": {"enabled": false},`, `"fallback": {"enabled": false}, "admin_panel": {"enabled": true, "tenant_id": "tenant", "user_id": "user"},`, 1)
	if _, err := Parse([]byte(raw), lookupMap(map[string]string{
		EnvGoogleClientID:     "id",
		EnvGoogleClientSecret: "secret",
	})); err == nil || !strings.Contains(err.Error(), "admin_panel.enabled") {
		t.Fatalf("Parse error = %v, want the development-only admin-panel error", err)
	}
}

func TestValidateEnabledAdminPanelRequiresSeparateRedirectURI(t *testing.T) {
	raw := strings.Replace(minimalConfig(), `"environment": "test"`, `"environment": "development"`, 1)
	raw = strings.Replace(raw, `"fallback": {"enabled": false},`, `"fallback": {"enabled": false}, "admin_panel": {"enabled": true, "tenant_id": "tenant", "user_id": "user"},`, 1)
	if _, err := Parse([]byte(raw), lookupMap(map[string]string{
		EnvGoogleClientID:     "id",
		EnvGoogleClientSecret: "secret",
	})); err == nil || !strings.Contains(err.Error(), "admin_redirect_uri") {
		t.Fatalf("Parse error = %v, want the separate callback error", err)
	}
}

func TestValidateRejectsImpossibleRetryBudget(t *testing.T) {
	// Three attempts of five seconds cannot fit in an eight second budget. If the
	// budget were allowed, the coordinator would silently truncate the retry loop
	// the docs require.
	raw := strings.Replace(minimalConfig(),
		`"scopes": ["https://www.googleapis.com/auth/drive.file"]`,
		`"scopes": ["https://www.googleapis.com/auth/drive.file"],
         "preflight_attempts": 3, "preflight_timeout": "5s", "routing_budget": "8s"`, 1)

	_, err := Parse([]byte(raw), lookupMap(nil))
	if err == nil {
		t.Fatal("attempts x timeout exceeding the routing budget must be rejected")
	}
	if !strings.Contains(err.Error(), "routing_budget") {
		t.Errorf("error = %v, want it to mention the routing budget", err)
	}
}

func TestValidateRejectsFallbackWithoutDisclosureAcceptance(t *testing.T) {
	raw := strings.Replace(minimalConfig(), `"fallback": {"enabled": false}`,
		`"fallback": {"enabled": true, "consent_required": true, "backends": [
           {"id": "b1", "vendor": "cloudflare_r2", "bucket": "b"}
         ]}`, 1)

	_, err := Parse([]byte(raw), lookupMap(nil))
	if err == nil {
		t.Fatal("fallback must refuse to enable while the outage disclosure is unaccepted")
	}
	if !strings.Contains(err.Error(), "consent") {
		t.Errorf("error = %v, want it to mention consent", err)
	}
}

func TestValidateRejectsUnknownVendor(t *testing.T) {
	raw := strings.Replace(minimalConfig(), `"fallback": {"enabled": false}`,
		`"fallback": {"enabled": true, "consent_required": false, "backends": [
           {"id": "b1", "vendor": "minio", "bucket": "b"}
         ]}`, 1)

	if _, err := Parse([]byte(raw), lookupMap(nil)); err == nil {
		t.Fatal("an unsupported vendor must be rejected")
	}
}

func TestValidateRejectsDuplicateBackendIDs(t *testing.T) {
	raw := strings.Replace(minimalConfig(), `"fallback": {"enabled": false}`,
		`"fallback": {"enabled": true, "consent_required": false, "backends": [
           {"id": "dup", "vendor": "aws_s3", "bucket": "b"},
           {"id": "dup", "vendor": "cloudflare_r2", "bucket": "c"}
         ]}`, 1)

	if _, err := Parse([]byte(raw), lookupMap(nil)); err == nil {
		t.Fatal("duplicate backend IDs must be rejected")
	}
}

func TestValidateRejectsNonHTTPSEndpoint(t *testing.T) {
	raw := strings.Replace(minimalConfig(), `"fallback": {"enabled": false}`,
		`"fallback": {"enabled": true, "consent_required": false, "backends": [
           {"id": "b1", "vendor": "aws_s3", "endpoint": "http://evil.example.com",
            "bucket": "b"}
         ]}`, 1)

	_, err := Parse([]byte(raw), lookupMap(nil))
	if err == nil {
		t.Fatal("a plaintext endpoint must be rejected by the scheme allow-list")
	}
	if !strings.Contains(err.Error(), "endpoint") {
		t.Errorf("error = %v, want it to mention the endpoint", err)
	}
}

func TestValidateRejectsEndpointOutsideHostAllowList(t *testing.T) {
	raw := strings.Replace(minimalConfig(), `"fallback": {"enabled": false}`,
		`"security": {"allowed_endpoint_schemes": ["https"], "allowed_endpoint_hosts": ["s3.amazonaws.com"]},
         "fallback": {"enabled": true, "consent_required": false, "backends": [
           {"id": "b1", "vendor": "aws_s3", "endpoint": "https://169.254.169.254",
            "bucket": "b"}
         ]}`, 1)

	_, err := Parse([]byte(raw), lookupMap(nil))
	if err == nil {
		t.Fatal("an endpoint outside the host allow-list must be rejected")
	}
	if !strings.Contains(err.Error(), "endpoint") {
		t.Errorf("error = %v, want it to mention the endpoint", err)
	}
}

func TestValidateAllowsEndpointInsideHostAllowList(t *testing.T) {
	raw := strings.Replace(minimalConfig(), `"fallback": {"enabled": false}`,
		`"security": {"allowed_endpoint_schemes": ["https"], "allowed_endpoint_hosts": ["s3.amazonaws.com"]},
         "fallback": {"enabled": true, "consent_required": false, "backends": [
           {"id": "b1", "vendor": "aws_s3", "endpoint": "https://s3.amazonaws.com",
            "bucket": "b"}
         ]}`, 1)

	if _, err := Parse([]byte(raw), lookupMap(nil)); err != nil {
		t.Fatalf("an allow-listed endpoint must be accepted: %v", err)
	}
}

func TestValidateRejectsMissingPostgresDSN(t *testing.T) {
	raw := strings.Replace(minimalConfig(), `"driver": "sqlite", "sqlite_path": "file::memory:"`,
		`"driver": "postgres"`, 1)
	if _, err := Parse([]byte(raw), lookupMap(nil)); err == nil {
		t.Fatal("a postgres driver without a DSN must be rejected")
	}
}

func TestValidateRejectsEmptyMIMEAllowList(t *testing.T) {
	raw := strings.Replace(minimalConfig(), `"allowed_mime_types": ["application/pdf"]`,
		`"allowed_mime_types": []`, 1)
	if _, err := Parse([]byte(raw), lookupMap(nil)); err == nil {
		t.Fatal("an empty MIME allow-list must be rejected")
	}
}

func TestValidateRejectsDriveChunkNotMultipleOf256KiB(t *testing.T) {
	raw := strings.Replace(minimalConfig(), `"allowed_categories": ["report"]`,
		`"allowed_categories": ["report"], "drive_chunk_size_bytes": 1000`, 1)
	if _, err := Parse([]byte(raw), lookupMap(nil)); err == nil {
		t.Fatal("a Drive chunk size that is not a multiple of 256 KiB must be rejected")
	}
}

func TestParseDurationsFromStringsAndNumbers(t *testing.T) {
	tests := []struct {
		raw  string
		want string
	}{
		{`"3s"`, "3s"},
		{`"1m30s"`, "1m30s"},
		{`"2h"`, "2h0m0s"},
		{`5000000000`, "5s"},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			var d Duration
			if err := d.UnmarshalJSON([]byte(tc.raw)); err != nil {
				t.Fatalf("UnmarshalJSON(%s): %v", tc.raw, err)
			}
			if got := d.String(); got != tc.want {
				t.Errorf("Duration(%s) = %s, want %s", tc.raw, got, tc.want)
			}
		})
	}
}

func TestParseDurationRejectsGarbage(t *testing.T) {
	var d Duration
	if err := d.UnmarshalJSON([]byte(`"three seconds"`)); err == nil {
		t.Fatal("a non-duration string must be rejected rather than defaulting to zero")
	}
}

func TestLoadReadsFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(minimalConfig()), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(EnvGoogleClientID, "id")
	t.Setenv(EnvGoogleClientSecret, "secret")

	if _, err := Load(path); err != nil {
		t.Fatalf("Load: %v", err)
	}
}

func TestLoadMissingFileIsAnError(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent.json")); err == nil {
		t.Fatal("a missing config file must be an error")
	}
}

func TestSQLiteIsForcedToASingleConnection(t *testing.T) {
	raw := strings.Replace(minimalConfig(), `"driver": "sqlite", "sqlite_path": "file::memory:"`,
		`"driver": "sqlite", "sqlite_path": "file::memory:", "max_open_conns": 25`, 1)
	cfg, err := Parse([]byte(raw), lookupMap(nil))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if cfg.Database.MaxOpenConns != 1 {
		t.Errorf("MaxOpenConns = %d, want 1; multiple SQLite writers cause SQLITE_BUSY", cfg.Database.MaxOpenConns)
	}
}

func TestUploadsConfigHelpers(t *testing.T) {
	limits := UploadsConfig{
		MaxFileSizeBytes:  1000,
		AllowedMIMETypes:  []string{"application/pdf", "image/PNG"},
		AllowedCategories: []string{"report", "image"},
		CategoryLimits:    map[string]int64{"image": 100},
	}

	if !limits.MIMEAllowed("application/pdf") {
		t.Error("an allowed MIME must pass")
	}
	// MIME comparison must be case-insensitive: browsers send inconsistent casing.
	if !limits.MIMEAllowed("image/png") {
		t.Error("MIME comparison must be case-insensitive")
	}
	if limits.MIMEAllowed("application/octet-stream") {
		t.Error("an unlisted MIME must be rejected")
	}
	if limits.MIMEAllowed("") {
		t.Error("an empty MIME must be rejected")
	}

	if !limits.CategoryAllowed("report") || limits.CategoryAllowed("video") {
		t.Error("category allow-list is wrong")
	}

	if got := limits.EffectiveMaxSize("image"); got != 100 {
		t.Errorf("EffectiveMaxSize(image) = %d, want the category limit 100", got)
	}
	if got := limits.EffectiveMaxSize("report"); got != 1000 {
		t.Errorf("EffectiveMaxSize(report) = %d, want the global limit 1000", got)
	}
	// A category limit larger than the global max must not widen the ceiling.
	limits.CategoryLimits["report"] = 5000
	if got := limits.EffectiveMaxSize("report"); got != 1000 {
		t.Errorf("EffectiveMaxSize(report) = %d, want the global ceiling 1000", got)
	}
}

func TestEmptyMIMEAllowListRejectsEverything(t *testing.T) {
	var limits UploadsConfig
	if limits.MIMEAllowed("application/pdf") {
		t.Error("an unconfigured allow-list must reject everything, not accept everything")
	}
}

func TestSecretFieldsAndRedactedSummaryDoNotLeak(t *testing.T) {
	raw := strings.Replace(minimalConfig(), `"fallback": {"enabled": false}`,
		`"fallback": {"enabled": true, "consent_required": false, "backends": [
           {"id": "r2", "vendor": "cloudflare_r2", "bucket": "b"}
         ]}`, 1)
	cfg, err := Parse([]byte(raw), lookupMap(map[string]string{
		EnvGoogleClientID:     "cid",
		EnvGoogleClientSecret: "csecret",
		EnvR2AccessKeyID:      "AKIAREALKEY",
		EnvR2SecretAccessKey:  "veryrealsecret",
	}))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	fields := cfg.SecretFields()
	if len(fields) == 0 {
		t.Fatal("SecretFields must list the sensitive paths")
	}
	// The summary must name the vendor but never a credential.
	summary := cfg.RedactedSummary()
	vendors, ok := summary["fallback_vendors"].([]string)
	if !ok || len(vendors) != 1 || vendors[0] != VendorCloudflareR2 {
		t.Errorf("summary vendors = %v, want [cloudflare_r2]", summary["fallback_vendors"])
	}

	rendered := renderMap(summary) + renderStrings(fields)
	for _, secret := range []string{"AKIAREALKEY", "veryrealsecret", "csecret"} {
		if strings.Contains(rendered, secret) {
			t.Errorf("summary or field list leaked the secret %q", secret)
		}
	}
}

// TestCheckedInJSONHasNoSecretFields is the boundary the owner asked for: a
// checked-in configuration file contains ordinary settings only. There is no
// secret reference in it at all, so there is nothing secret-adjacent to review.
func TestCheckedInJSONHasNoSecretFields(t *testing.T) {
	for _, path := range []string{"../../config/config.development.json", "../../config/config.production.json"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read %s: %v", path, err)
		}

		var document map[string]any
		if err := json.Unmarshal(raw, &document); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}

		walkStrings(document, func(key, value string) {
			if strings.HasSuffix(key, "_ref") {
				t.Errorf("%s: field %q is a secret reference. Secrets belong in the environment "+
					"and their names are a code convention; a checked-in config carries neither.", path, key)
			}
			if strings.Contains(key, "secret") || strings.Contains(key, "password") ||
				strings.Contains(key, "access_key") || strings.Contains(key, "token") ||
				strings.Contains(key, "encryption_key") {
				t.Errorf("%s: field %q is secret-bearing and must not exist in a config file", path, key)
			}
			if value != "" && looksLikeCredential(value) {
				t.Errorf("%s: %q holds a credential-shaped value", path, key)
			}
		})
	}
}

// TestSecretEnvVarNamesAreCanonical checks the convention the code relies on.
func TestSecretEnvVarNamesAreCanonical(t *testing.T) {
	for _, name := range AllSecretEnvVars() {
		if !strings.HasPrefix(name, "STORVIA_") {
			t.Errorf("secret variable %q does not use the STORVIA_ prefix", name)
		}
		if !validEnvVarName(name) {
			t.Errorf("secret variable %q is not a valid environment variable name", name)
		}
	}
}

// TestFallbackCredentialsAreChosenByVendor keeps an R2 backend from being pointed
// at the AWS credentials.
func TestFallbackCredentialsAreChosenByVendor(t *testing.T) {
	env := map[string]string{
		EnvSessionSigningKey:  "a-signing-key-long-enough-for-the-test",
		EnvEncryptionKey:      strings.Repeat("a", 64),
		EnvR2AccessKeyID:      "r2-id",
		EnvR2SecretAccessKey:  "r2-secret",
		EnvAWSAccessKeyID:     "aws-id",
		EnvAWSSecretAccessKey: "aws-secret",
	}

	r2 := &Config{
		Database: DatabaseConfig{Driver: DriverSQLite, SQLitePath: "file::memory:"},
		Cache:    CacheConfig{Driver: DriverMemory},
		Fallback: FallbackConfig{Enabled: true, Backends: []S3BackendConfig{
			{ID: "r2", Vendor: VendorCloudflareR2, Bucket: "b"},
		}},
	}
	resolved, err := r2.ResolveSecrets(lookupMap(env))
	if err != nil {
		t.Fatalf("r2 ResolveSecrets: %v", err)
	}
	if resolved[EnvR2AccessKeyID] != "r2-id" {
		t.Error("an R2 backend must resolve the R2 credentials")
	}
	if _, present := resolved[EnvAWSAccessKeyID]; present {
		t.Error("an R2 backend must not pull in the AWS credentials")
	}

	aws := &Config{
		Database: DatabaseConfig{Driver: DriverSQLite, SQLitePath: "file::memory:"},
		Cache:    CacheConfig{Driver: DriverMemory},
		Fallback: FallbackConfig{Enabled: true, Backends: []S3BackendConfig{
			{ID: "aws", Vendor: VendorAWSS3, Bucket: "b"},
		}},
	}
	resolvedAWS, err := aws.ResolveSecrets(lookupMap(env))
	if err != nil {
		t.Fatalf("aws ResolveSecrets: %v", err)
	}
	if resolvedAWS[EnvAWSAccessKeyID] != "aws-id" {
		t.Error("an AWS backend must resolve the AWS credentials")
	}
	if _, present := resolvedAWS[EnvR2AccessKeyID]; present {
		t.Error("an AWS backend must not pull in the R2 credentials")
	}
}

func TestActiveBackendsSkipsDisabledAndIncomplete(t *testing.T) {
	fallback := FallbackConfig{
		Enabled: true,
		Backends: []S3BackendConfig{
			{ID: "ok", Vendor: VendorAWSS3, Bucket: "b"},
			{ID: "disabled", Vendor: VendorAWSS3, Bucket: "b", Disabled: true},
			{ID: "nobucket", Vendor: VendorAWSS3},
		},
	}
	active := fallback.ActiveBackends()
	if len(active) != 1 || active[0].ID != "ok" {
		t.Errorf("ActiveBackends = %v, want only the usable backend", ids(active))
	}
}

func renderMap(m map[string]any) string {
	var b strings.Builder
	for k, v := range m {
		b.WriteString(k)
		b.WriteString("=")
		b.WriteString(strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(
			valueString(v), "\n", ""), "  ", " ")))
		b.WriteString(";")
	}
	return b.String()
}

func renderStrings(values []string) string { return strings.Join(values, ";") }

func valueString(v any) string {
	switch typed := v.(type) {
	case string:
		return typed
	case []string:
		return strings.Join(typed, ",")
	default:
		return ""
	}
}

func ids(backends []S3BackendConfig) []string {
	out := make([]string, 0, len(backends))
	for _, b := range backends {
		out = append(out, b.ID)
	}
	return out
}
