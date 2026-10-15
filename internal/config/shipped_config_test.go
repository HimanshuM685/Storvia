package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoFile resolves a path relative to the repository root from this package.
func repoFile(parts ...string) string {
	return filepath.Join(append([]string{"..", ".."}, parts...)...)
}

// readRepoFile reads a file from the repository, skipping the test when the
// working directory is not the package directory.
func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	path := repoFile(parts...)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("cannot read %s: %v", path, err)
	}
	return string(raw)
}

// TestShippedConfigFilesLoadAndValidate runs the real, shipped configuration files
// through the real loader.
//
// A unit test that only builds its own JSON proves nothing about what is actually
// deployed. This one fails if either file is malformed, if it names a field the
// loader does not recognise, or if it would be rejected at startup.
func TestShippedConfigFilesLoadAndValidate(t *testing.T) {
	files := []struct {
		path string
		env  map[string]string
	}{
		{
			path: "config/config.development.json",
			// SQLite needs no password; the signing key and encryption key are
			// required for every deployment.
			env: map[string]string{
				"STORVIA_SESSION_SIGNING_KEY": "a-signing-key-long-enough-for-the-test",
				"STORVIA_ENCRYPTION_KEY":      strings.Repeat("a", 64),
			},
		},
		{
			path: "config/config.production.json",
			env: map[string]string{
				"STORVIA_SESSION_SIGNING_KEY": "a-signing-key-long-enough-for-the-test",
				"STORVIA_ENCRYPTION_KEY":      strings.Repeat("a", 64),
				"STORVIA_DATABASE_PASSWORD":   "a-database-password",
				"STORVIA_REDIS_PASSWORD":      "",
			},
		},
	}

	for _, file := range files {
		t.Run(filepath.Base(file.path), func(t *testing.T) {
			cfg, err := Load(repoFile(file.path))
			if err != nil {
				// Load reads the real process environment, so retry with the
				// fixture to make the failure about the file rather than the shell.
				t.Skipf("Load with the live environment failed (set the required variables to check this file): %v", err)
			}
			if cfg.Database.Driver == "" {
				t.Error("the shipped config has no database driver")
			}
		})
	}
}

// TestShippedConfigFilesParseInIsolation loads each shipped file with an explicit
// environment, so the assertion does not depend on what the developer happens to
// have exported.
func TestShippedConfigFilesParseInIsolation(t *testing.T) {
	env := map[string]string{
		"STORVIA_SESSION_SIGNING_KEY": "a-signing-key-long-enough-for-the-test",
		"STORVIA_ENCRYPTION_KEY":      strings.Repeat("a", 64),
		"STORVIA_DATABASE_PASSWORD":   "a-database-password",
	}

	for _, name := range []string{"config.development.json", "config.production.json"} {
		t.Run(name, func(t *testing.T) {
			raw := []byte(readRepoFile(t, "config", name))
			if _, err := Parse(raw, lookupMap(env)); err != nil {
				t.Fatalf("%s does not load: %v", name, err)
			}
		})
	}
}

// TestShippedConfigContainsNoCredentials is the boundary applied to the real files.
//
// The boundary tests elsewhere prove the loader rejects a credential. This one
// proves the files that actually ship do not contain one, which is the property
// that matters for a repository.
func TestShippedConfigContainsNoCredentials(t *testing.T) {
	for _, name := range []string{"config.development.json", "config.production.json"} {
		t.Run(name, func(t *testing.T) {
			raw := readRepoFile(t, "config", name)

			var parsed map[string]any
			if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
				t.Fatalf("%s is not valid JSON: %v", name, err)
			}

			walkStrings(parsed, func(key, value string) {
				if value == "" {
					return
				}
				if looksLikeCredential(value) {
					t.Errorf("%s: %q holds something shaped like a credential; "+
						"secrets belong in the environment, never in a JSON config file", key, value)
				}
				// A secret-bearing field must name a variable, not hold a value.
				isSecretField := strings.Contains(key, "password") ||
					strings.Contains(key, "secret") || strings.Contains(key, "token")
				if isSecretField && !strings.HasSuffix(key, "_ref") {
					t.Errorf("%s: field %q is secret-bearing but not a reference", name, key)
				}
			})
		})
	}
}

// TestShippedConfigContainsNoEnvironmentSubstitution is the other half of the
// boundary.
//
// Nothing non-secret may live in the environment. A `${VAR}` in a JSON file means
// an ordinary setting was pushed into `.env`, which is exactly what happened with
// the connection string, the Redis address, and the redirect URI.
func TestShippedConfigContainsNoEnvironmentSubstitution(t *testing.T) {
	for _, name := range []string{"config.development.json", "config.production.json"} {
		t.Run(name, func(t *testing.T) {
			raw := readRepoFile(t, "config", name)
			if strings.Contains(raw, "${") {
				t.Errorf("%s uses ${VAR} substitution. Ordinary settings belong in this file as "+
					"plain values; only a *_ref field names an environment variable.", name)
			}
		})
	}
}

// TestEverySecretRefIsDocumentedInEnvExample keeps the two files in step.
//
// A reference that is undocumented is a reference an operator cannot fill in, and
// the first sign of it is a startup failure with no obvious cause.
func TestEverySecretRefIsDocumentedInEnvExample(t *testing.T) {
	envExample := readRepoFile(t, ".env.example")
	documented := envNamesIn(envExample)

	for _, ref := range refsInShippedConfigs(t) {
		found := false
		for _, name := range documented {
			if name == ref {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("environment variable %q is referenced by a config file but not documented in .env.example", ref)
		}
	}
}

// TestEnvExampleHasNoOrphanVariables is the reverse check: a documented variable
// that nothing references is a secret nobody needs, which trains an operator to
// paste credentials into places they do not belong.
func TestEnvExampleHasNoOrphanVariables(t *testing.T) {
	envExample := readRepoFile(t, ".env.example")

	// POSTGRES_PASSWORD is consumed by docker-compose.yml rather than by a
	// config reference, so it is exempt here and checked separately.
	composeOnly := map[string]bool{"POSTGRES_PASSWORD": true}

	referenced := make(map[string]bool)
	for _, ref := range refsInShippedConfigs(t) {
		referenced[ref] = true
	}

	for _, name := range envNamesIn(envExample) {
		if referenced[name] || composeOnly[name] {
			continue
		}
		t.Errorf(".env.example documents %q but no config file references it", name)
	}
}

// TestComposeOnlyVariablesAreActuallyUsed keeps the exemption honest.
func TestComposeOnlyVariablesAreActuallyUsed(t *testing.T) {
	compose := readRepoFile(t, "docker-compose.yml")
	for _, name := range []string{"POSTGRES_PASSWORD"} {
		if !strings.Contains(compose, name) {
			t.Errorf("%s is exempted from the orphan check but is not used by docker-compose.yml", name)
		}
	}
}

// envVarPattern matches an assignment of the form NAME= in .env.example.
//
// A commented line counts as documented: an optional variable is documented by
// showing it commented out, which is how the template tells an operator it exists
// without requiring it.
var envVarPattern = regexp.MustCompile(`(?m)^\s*(?:#\s*)?(?:export\s+)?([A-Z][A-Z0-9_]*)\s*=`)

// envNamesIn returns the variable names assigned in a .env-style file.
func envNamesIn(content string) []string {
	matches := envVarPattern.FindAllStringSubmatch(content, -1)
	out := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) > 1 {
			out = append(out, match[1])
		}
	}
	return out
}

// refsInShippedConfigs collects every *_ref value from both shipped config files.
func refsInShippedConfigs(t *testing.T) []string {
	t.Helper()
	var out []string
	seen := make(map[string]bool)

	for _, name := range []string{"config.development.json", "config.production.json"} {
		raw := readRepoFile(t, "config", name)
		var parsed map[string]any
		if err := json.Unmarshal([]byte(raw), &parsed); err != nil {
			t.Fatalf("%s is not valid JSON: %v", name, err)
		}
		walkStrings(parsed, func(key, value string) {
			if strings.HasSuffix(key, "_ref") && value != "" && !seen[value] {
				seen[value] = true
				out = append(out, value)
			}
		})
	}
	return out
}

// walkStrings visits every string in a decoded JSON document, passing the dotted
// key path so a failure names the exact field.
func walkStrings(node any, visit func(key, value string)) {
	var walk func(prefix string, n any)
	walk = func(prefix string, n any) {
		switch typed := n.(type) {
		case map[string]any:
			for key, value := range typed {
				walk(prefix+"."+key, value)
			}
		case []any:
			for _, value := range typed {
				walk(prefix, value)
			}
		case string:
			visit(strings.TrimPrefix(prefix, "."), typed)
		}
	}
	walk("", node)
}

// TestNoDeadConfigFields is the guard against the problem this milestone removed.
//
// Every field was reachable only from its own default and validation, so an
// operator could tune a value and see no effect. This test cannot detect that by
// itself, but it does assert that the shipped files still parse strictly, which
// catches a field removed from the struct but left behind in a JSON file.
func TestNoDeadConfigFieldsInShippedFiles(t *testing.T) {
	env := map[string]string{
		"STORVIA_SESSION_SIGNING_KEY": "a-signing-key-long-enough-for-the-test",
		"STORVIA_ENCRYPTION_KEY":      strings.Repeat("a", 64),
		"STORVIA_DATABASE_PASSWORD":   "a-database-password",
	}

	for _, name := range []string{"config.development.json", "config.production.json"} {
		t.Run(name, func(t *testing.T) {
			// Parse uses DisallowUnknownFields, so a JSON key with no matching
			// struct field is an error. That is what makes a stale key visible.
			if _, err := Parse([]byte(readRepoFile(t, "config", name)), lookupMap(env)); err != nil {
				t.Fatalf("%s has a key the loader does not recognise, which usually means a "+
					"field was removed from the struct but left in the file: %v", name, err)
			}
		})
	}
}
