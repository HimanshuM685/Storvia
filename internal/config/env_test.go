package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseEnvHandlesTheUsableForms(t *testing.T) {
	content := `# a comment line

KEY=value
export EXPORTED=exported-value
QUOTED="quoted value"
SINGLE='literal \ $ value'
WITH_COMMENT=value # trailing comment
EMPTY=
  SPACED  =  padded
INLINE_HASH="a # b"
`

	file, err := ParseEnv(content)
	if err != nil {
		t.Fatalf("ParseEnv: %v", err)
	}

	checks := map[string]string{
		"KEY":          "value",
		"EXPORTED":     "exported-value",
		"QUOTED":       "quoted value",
		"SINGLE":       `literal \ $ value`,
		"WITH_COMMENT": "value",
		"EMPTY":        "",
		"SPACED":       "padded",
		"INLINE_HASH":  "a # b",
	}
	for key, want := range checks {
		got, ok := file.Lookup(key)
		if !ok {
			t.Errorf("%s is missing", key)
			continue
		}
		if got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}

	if _, ok := file.Lookup("NOT_THERE"); ok {
		t.Error("Lookup must report absent keys")
	}
}

func TestParseEnvRejectsMalformedInput(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{"missing equals", "JUST_A_NAME", "KEY=value"},
		{"empty name", "=value", "empty variable name"},
		{"invalid name", "BAD-NAME=value", "invalid character"},
		{"unterminated double quote", `KEY="unterminated`, "unterminated"},
		{"unterminated single quote", `KEY='unterminated`, "unterminated"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseEnv(tc.content)
			if err == nil {
				t.Fatal("want an error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestApplyEnvFileDoesNotOverrideTheProcessEnvironment(t *testing.T) {
	t.Setenv("ALREADY_SET", "from-environment")
	t.Setenv("NOT_YET_SET", "")
	// Clear so the file value can apply.
	os.Unsetenv("NOT_YET_SET")

	file, err := ParseEnv("ALREADY_SET=from-file\nNOT_YET_SET=from-file\n")
	if err != nil {
		t.Fatalf("ParseEnv: %v", err)
	}

	exported := ApplyEnvFile(file)

	if got := os.Getenv("ALREADY_SET"); got != "from-environment" {
		t.Errorf("ALREADY_SET = %q; a real environment value must win over the file", got)
	}
	if got := os.Getenv("NOT_YET_SET"); got != "from-file" {
		t.Errorf("NOT_YET_SET = %q, want the file value to be applied", got)
	}

	// Only the newly exported name should be reported.
	if len(exported) != 1 || exported[0] != "NOT_YET_SET" {
		t.Errorf("exported = %v, want only NOT_YET_SET", exported)
	}
}

func TestEnvFileLookupPrefersTheProcessEnvironment(t *testing.T) {
	t.Setenv("SHARED", "environment-wins")

	file, err := ParseEnv("SHARED=file-loses\nONLY_FILE=file-value\n")
	if err != nil {
		t.Fatalf("ParseEnv: %v", err)
	}
	lookup := EnvFileLookup(file)

	if got, _ := lookup("SHARED"); got != "environment-wins" {
		t.Errorf("SHARED = %q, want the environment value", got)
	}
	if got, _ := lookup("ONLY_FILE"); got != "file-value" {
		t.Errorf("ONLY_FILE = %q, want the file value", got)
	}
	if _, ok := lookup("ABSENT"); ok {
		t.Error("an absent name must report false")
	}
}

func TestLoadEnvFileReadsFromDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte("KEY=value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := LoadEnvFile(path)
	if err != nil {
		t.Fatalf("LoadEnvFile: %v", err)
	}
	if got, _ := file.Lookup("KEY"); got != "value" {
		t.Errorf("KEY = %q, want value", got)
	}
	if err := os.WriteFile(path, []byte("BROKEN\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEnvFile(path); err == nil {
		t.Error("a malformed env file must be an error, not silently ignored")
	}
}

func TestRedactedReportsPresenceWithoutValues(t *testing.T) {
	file, err := ParseEnv("SECRET_TOKEN=super-secret\nOTHER=another-secret\n")
	if err != nil {
		t.Fatalf("ParseEnv: %v", err)
	}
	redacted := file.Redacted()

	if len(redacted) != 2 {
		t.Fatalf("Redacted has %d entries, want 2", len(redacted))
	}
	for key, value := range redacted {
		if value != "[SET]" {
			t.Errorf("%s = %q, want the redacted marker", key, value)
		}
	}
	rendered := strings.Join([]string{redacted["SECRET_TOKEN"], redacted["OTHER"]}, "")
	if strings.Contains(rendered, "super-secret") || strings.Contains(rendered, "another-secret") {
		t.Error("Redacted leaked a value")
	}
}

func TestKeysPreservesFileOrder(t *testing.T) {
	file, err := ParseEnv("FIRST=1\nSECOND=2\nTHIRD=3\n")
	if err != nil {
		t.Fatalf("ParseEnv: %v", err)
	}
	keys := file.Keys()
	if len(keys) != 3 || keys[0] != "FIRST" || keys[1] != "SECOND" || keys[2] != "THIRD" {
		t.Errorf("Keys = %v, want file order", keys)
	}
}

func TestFormatEnvRoundTrips(t *testing.T) {
	values := []string{
		"simple",
		"has space",
		`has"quote`,
		"has#hash",
		`has\backslash`,
		"has$dollar",
		"",
	}
	for _, value := range values {
		line := FormatEnv("KEY", value)
		file, err := ParseEnv(line)
		if err != nil {
			t.Fatalf("ParseEnv(%q) from FormatEnv(%q): %v", line, value, err)
		}
		got, ok := file.Lookup("KEY")
		if !ok {
			t.Fatalf("KEY missing after round-trip of %q (line %q)", value, line)
		}
		if got != value {
			t.Errorf("round-trip of %q produced %q (line %q)", value, got, line)
		}
	}
}

func TestLastKeyWinsOnDuplicate(t *testing.T) {
	file, err := ParseEnv("KEY=first\nKEY=second\n")
	if err != nil {
		t.Fatalf("ParseEnv: %v", err)
	}
	if got, _ := file.Lookup("KEY"); got != "second" {
		t.Errorf("KEY = %q, want the last assignment to win", got)
	}
	if len(file.Keys()) != 1 {
		t.Errorf("Keys = %v, want a single entry", file.Keys())
	}
}
