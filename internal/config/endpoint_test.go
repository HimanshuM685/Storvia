package config

import (
	"os"
	"strings"
	"testing"
)

// endpointFallbackJSON is a minimal configuration with one R2 backend and no endpoint
// in the JSON, which is the shipped shape.
const endpointFallbackJSON = `{
  "environment": "test",
  "database": {"driver": "sqlite", "sqlite_path": "file::memory:"},
  "cache": {"driver": "memory"},
  "fallback": {"enabled": true, "consent_required": false, "backends": [
     {"id": "r2-primary", "vendor": "cloudflare_r2", "bucket": "storvia-testing"}
  ]},
  "uploads": {"max_file_size_bytes": 1024,
              "allowed_mime_types": ["application/pdf"],
              "allowed_categories": ["report"]}
}`

// TestEndpointComesFromTheEnvironment is the behaviour the owner asked for: the
// account-specific endpoint is not in the checked-in JSON.
func TestEndpointComesFromTheEnvironment(t *testing.T) {
	cfg, err := Parse([]byte(endpointFallbackJSON), lookupMap(map[string]string{
		EnvSessionSigningKey: "a-signing-key-long-enough-for-the-test",
		EnvEncryptionKey:     strings.Repeat("a", 64),
		EnvR2Endpoint:        r2TestEndpoint,
		EnvR2AccessKeyID:     "r2-id",
		EnvR2SecretAccessKey: "r2-secret",
	}))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if got := cfg.Fallback.Backends[0].Endpoint; got != r2TestEndpoint {
		t.Errorf("endpoint = %q, want the value from %s", got, EnvR2Endpoint)
	}
}

// TestMissingEndpointIsAnErrorNotAnEmptyString matters because the failure it prevents
// is silent. An empty endpoint would fall back to the AWS SDK's own default, which for
// an R2 backend is not an R2 request at all.
func TestMissingEndpointIsAnErrorNotAnEmptyString(t *testing.T) {
	_, err := Parse([]byte(endpointFallbackJSON), lookupMap(map[string]string{
		EnvSessionSigningKey: "a-signing-key-long-enough-for-the-test",
		EnvEncryptionKey:     strings.Repeat("a", 64),
		EnvR2AccessKeyID:     "r2-id",
		EnvR2SecretAccessKey: "r2-secret",
	}))
	if err == nil {
		t.Fatal("an enabled backend with no endpoint must fail startup")
	}
	if !strings.Contains(err.Error(), EnvR2Endpoint) {
		t.Errorf("error = %v, want it to name the variable to set", err)
	}
}

// TestExplicitEndpointOverridesTheEnvironment keeps the multi-backend case working: two
// R2 backends cannot both be served by one vendor-level variable.
func TestExplicitEndpointOverridesTheEnvironment(t *testing.T) {
	raw := strings.Replace(endpointFallbackJSON,
		`{"id": "r2-primary", "vendor": "cloudflare_r2", "bucket": "storvia-testing"}`,
		`{"id": "r2-primary", "vendor": "cloudflare_r2", "bucket": "b",
		  "endpoint": "https://elsewhere.example.com"}`, 1)

	cfg, err := Parse([]byte(raw), lookupMap(map[string]string{
		EnvSessionSigningKey: "a-signing-key-long-enough-for-the-test",
		EnvEncryptionKey:     strings.Repeat("a", 64),
		EnvR2Endpoint:        r2TestEndpoint,
		EnvR2AccessKeyID:     "r2-id",
		EnvR2SecretAccessKey: "r2-secret",
	}))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := cfg.Fallback.Backends[0].Endpoint; got != "https://elsewhere.example.com" {
		t.Errorf("endpoint = %q, want the explicit JSON value to win", got)
	}
}

// TestDisabledBackendNeedsNoEndpoint: a disabled backend is not contacted, so it must
// not be forced to name an endpoint it will never use.
func TestDisabledBackendNeedsNoEndpoint(t *testing.T) {
	raw := strings.Replace(endpointFallbackJSON, `"bucket": "storvia-testing"}`,
		`"bucket": "storvia-testing", "disabled": true}`, 1)

	if _, err := Parse([]byte(raw), lookupMap(map[string]string{
		EnvSessionSigningKey: "a-signing-key-long-enough-for-the-test",
		EnvEncryptionKey:     strings.Repeat("a", 64),
	})); err != nil {
		t.Fatalf("a disabled backend must not require an endpoint: %v", err)
	}
}

// TestEndpointEnvVarIsChosenByVendor keeps an R2 backend from being pointed at an AWS
// endpoint variable, mirroring how the credentials are chosen.
func TestEndpointEnvVarIsChosenByVendor(t *testing.T) {
	if got := EndpointEnvVar(VendorCloudflareR2); got != EnvR2Endpoint {
		t.Errorf("R2 endpoint var = %q, want %q", got, EnvR2Endpoint)
	}
	if got := EndpointEnvVar(VendorAWSS3); got != EnvAWSEndpoint {
		t.Errorf("AWS endpoint var = %q, want %q", got, EnvAWSEndpoint)
	}
}

// TestHostAllowedWildcardIsOneLabelOnly is a security test. The wildcard exists so the
// account identifier does not have to sit in the checked-in config, which means it must
// not be a way to match an arbitrary host.
func TestHostAllowedWildcardIsOneLabelOnly(t *testing.T) {
	allowed := "*.r2.cloudflarestorage.com"

	if !hostAllowed(allowed, "0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com") {
		t.Error("the wildcard must match an account endpoint")
	}
	if !hostAllowed(allowed, "ABC.R2.CLOUDFLARESTORAGE.COM") {
		t.Error("host matching must be case-insensitive")
	}

	// Too many labels: a subdomain of the account endpoint is not an R2 endpoint.
	if hostAllowed(allowed, "a.b.r2.cloudflarestorage.com") {
		t.Error("the wildcard matched two labels, which widens it beyond one account")
	}
	// An empty label is not a host.
	if hostAllowed(allowed, ".r2.cloudflarestorage.com") {
		t.Error("the wildcard matched an empty label")
	}
	// Unrelated hosts, including ones that merely end in the same text.
	if hostAllowed(allowed, "evil-r2.cloudflarestorage.com.attacker.test") {
		t.Error("the wildcard matched an attacker-controlled suffix")
	}
	if hostAllowed(allowed, "s3.amazonaws.com") {
		t.Error("the wildcard matched a different provider entirely")
	}

	// An exact entry still has to be exact.
	if hostAllowed("s3.amazonaws.com", "s3.amazonaws.com.evil.test") {
		t.Error("an exact entry matched a host with an appended suffix")
	}
	if !hostAllowed("s3.amazonaws.com", "s3.amazonaws.com") {
		t.Error("an exact entry failed to match itself")
	}
}

// TestShippedAllowListPermitsTheVendors checks the shipped allow-lists against real
// vendor host shapes, since the account identifier is no longer in the configuration.
func TestShippedAllowListPermitsTheVendors(t *testing.T) {
	cfg := &Config{Security: SecurityConfig{
		AllowedEndpointSchemes: []string{"https"},
		AllowedEndpointHosts:   []string{"*.r2.cloudflarestorage.com", "s3.amazonaws.com"},
	}}

	for _, ok := range []string{
		"https://0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com",
		"https://s3.amazonaws.com",
	} {
		if !cfg.endpointAllowed(ok) {
			t.Errorf("%s must be permitted by the shipped allow-list", ok)
		}
	}
	for _, bad := range []string{
		"http://0123456789abcdef0123456789abcdef.r2.cloudflarestorage.com", // not https
		"https://internal.corp.example.com",
		"https://r2.cloudflarestorage.com.evil.test",
	} {
		if cfg.endpointAllowed(bad) {
			t.Errorf("%s must be refused", bad)
		}
	}
}

// TestEndpointEnvVarsAreDocumented keeps .env.example usable.
func TestEndpointEnvVarsAreDocumented(t *testing.T) {
	example := ""
	if raw, err := os.ReadFile(repoFile(".env.example")); err == nil {
		example = string(raw)
	}
	for _, name := range AllEndpointEnvVars() {
		if !strings.Contains(example, name) {
			t.Errorf(".env.example does not document %s", name)
		}
	}
	// An endpoint is not a secret and must not be treated as one.
	for _, name := range AllSecretEnvVars() {
		if name == EnvR2Endpoint || name == EnvAWSEndpoint {
			t.Errorf("%s is an endpoint and must not be listed as a secret", name)
		}
	}
}
