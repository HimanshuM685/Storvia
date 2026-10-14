package config

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// EnvFile is a parsed .env file: ordered key/value pairs from one file.
type EnvFile struct {
	keys   []string
	values map[string]string
}

// Keys returns the variable names in the order they appeared.
func (f *EnvFile) Keys() []string {
	out := make([]string, len(f.keys))
	copy(out, f.keys)
	return out
}

// Lookup implements the signature ExpandEnv expects.
func (f *EnvFile) Lookup(name string) (string, bool) {
	v, ok := f.values[name]
	return v, ok
}

// Redacted returns the file's keys mapped to a fixed marker. It exists so a
// startup diagnostic can prove which variables were present without printing
// any of their values.
func (f *EnvFile) Redacted() map[string]string {
	out := make(map[string]string, len(f.values))
	for key := range f.values {
		out[key] = "[SET]"
	}
	return out
}

// ParseEnv reads .env content.
//
// Supported forms are KEY=value, quoted values, # comments, blank lines, and an
// optional export prefix. Values already present in the process environment
// win, so an operator can override a file value without editing the file.
func ParseEnv(content string) (*EnvFile, error) {
	file := &EnvFile{values: make(map[string]string)}
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)

	line := 0
	for scanner.Scan() {
		line++
		raw := strings.TrimSpace(scanner.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		raw = strings.TrimPrefix(raw, "export ")

		name, value, found := strings.Cut(raw, "=")
		if !found {
			return nil, fmt.Errorf("config: env line %d: expected KEY=value", line)
		}
		name = strings.TrimSpace(name)
		if name == "" {
			return nil, fmt.Errorf("config: env line %d: empty variable name", line)
		}
		if err := validEnvName(name); err != nil {
			return nil, fmt.Errorf("config: env line %d: %w", line, err)
		}

		parsed, err := parseEnvValue(value)
		if err != nil {
			return nil, fmt.Errorf("config: env line %d (%s): %w", line, name, err)
		}
		if _, exists := file.values[name]; !exists {
			file.keys = append(file.keys, name)
		}
		file.values[name] = parsed
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("config: read env: %w", err)
	}
	return file, nil
}

func validEnvName(name string) error {
	for i, r := range name {
		switch {
		case r == '_':
		case r >= 'A' && r <= 'Z':
		case r >= 'a' && r <= 'z':
		case i > 0 && r >= '0' && r <= '9':
		default:
			return fmt.Errorf("invalid character %q in variable name", r)
		}
	}
	return nil
}

// parseEnvValue strips comments and surrounding quotes.
func parseEnvValue(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}

	switch {
	case strings.HasPrefix(value, `"`):
		end := strings.LastIndex(value, `"`)
		if end == 0 {
			return "", fmt.Errorf("unterminated double-quoted value")
		}
		// Decode the same escapes FormatEnv emits, so a value written by this
		// package reads back identically. Using strconv.Unquote rather than a
		// hand-rolled list keeps the two halves in step automatically.
		decoded, err := strconv.Unquote(value[:end+1])
		if err != nil {
			return "", fmt.Errorf("invalid double-quoted value: %w", err)
		}
		return decoded, nil
	case strings.HasPrefix(value, `'`):
		end := strings.LastIndex(value, `'`)
		if end == 0 {
			return "", fmt.Errorf("unterminated single-quoted value")
		}
		// Single quotes are literal, which is the safe form for a secret that
		// contains a backslash, a quote, or a dollar sign.
		return value[1:end], nil
	}

	// Unquoted: an inline # starts a comment only when preceded by whitespace.
	if idx := strings.Index(value, " #"); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	return value, nil
}

// LoadEnvFile reads path and returns the parsed file.
func LoadEnvFile(path string) (*EnvFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: read env file %s: %w", path, err)
	}
	file, err := ParseEnv(string(raw))
	if err != nil {
		return nil, fmt.Errorf("config: parse env file %s: %w", path, err)
	}
	return file, nil
}

// ApplyEnvFile exports every variable in file that is not already set in the
// process environment. It returns the names it exported so startup can log them
// as a name-only list.
func ApplyEnvFile(file *EnvFile) []string {
	exported := make([]string, 0, len(file.keys))
	for _, key := range file.keys {
		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		value, _ := file.Lookup(key)
		if err := os.Setenv(key, value); err == nil {
			exported = append(exported, key)
		}
	}
	return exported
}

// EnvFileLookup returns a lookup function that prefers the process environment
// and falls back to file. This is the resolver used for config substitution, so
// a shell override always wins over the .env file.
func EnvFileLookup(file *EnvFile) func(string) (string, bool) {
	return func(name string) (string, bool) {
		if value, ok := os.LookupEnv(name); ok {
			return value, true
		}
		if file == nil {
			return "", false
		}
		return file.Lookup(name)
	}
}

// FormatEnv renders a key/value pair in .env syntax with the quoting rule that
// keeps it round-trippable: quote when the value contains whitespace, a quote,
// a #, a backslash, or a dollar sign.
func FormatEnv(name, value string) string {
	needsQuote := value == "" || strings.ContainsAny(value, " \t\n\"'#\\$")
	if !needsQuote {
		return name + "=" + value
	}
	return name + "=" + strconv.Quote(value)
}
