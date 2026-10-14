package logger

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

// decode parses one JSON log line.
func decode(t *testing.T, line string) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal([]byte(line), &out); err != nil {
		t.Fatalf("log line is not valid JSON: %v (line: %s)", err, line)
	}
	return out
}

// TestRedactsTopLevelSensitiveAttributes is the core guarantee: a token passed
// as a log attribute never reaches the output, whatever the caller's intent.
func TestRedactsTopLevelSensitiveAttributes(t *testing.T) {
	sensitive := []string{
		"access_token",
		"refresh_token",
		"id_token",
		"Authorization",
		"client_secret",
		"api_key",
		"X-Amz-Signature",
		"session_uri",
		"encrypted_session_uri",
		"presigned_url",
		"password",
		"encryption_key",
		"Cookie",
	}

	for _, key := range sensitive {
		t.Run(key, func(t *testing.T) {
			var buf bytes.Buffer
			log := New(&buf, Options{Level: slog.LevelDebug, Format: "json"})
			log.Info("event", key, "SUPER-SECRET-VALUE")

			out := buf.String()
			if strings.Contains(out, "SUPER-SECRET-VALUE") {
				t.Errorf("attribute %q leaked its value: %s", key, out)
			}
			if !strings.Contains(out, redacted) {
				t.Errorf("attribute %q was dropped instead of redacted: %s", key, out)
			}
		})
	}
}

// TestRedactsNestedGroupAttributes covers the case where a careless caller
// nests a token under a named group.
func TestRedactsNestedGroupAttributes(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, Options{Format: "json"})

	log.WithGroup("google").Info("event", "refresh_token", "NESTED-SECRET")
	log.Info("event", slog.Group("s3", "secret_access_key", "GROUP-SECRET"))

	out := buf.String()
	if strings.Contains(out, "NESTED-SECRET") {
		t.Errorf("a token nested under a group leaked: %s", out)
	}
	if strings.Contains(out, "GROUP-SECRET") {
		t.Errorf("a token inside a group attribute leaked: %s", out)
	}
}

// TestRedactsThroughWith confirms a logger configured with With is still
// redacting, which is the path application code actually uses.
func TestRedactsThroughWith(t *testing.T) {
	var buf bytes.Buffer
	base := New(&buf, Options{Format: "json"})
	base.With("access_token", "BOUND-SECRET").Info("event")

	if strings.Contains(buf.String(), "BOUND-SECRET") {
		t.Errorf("a token bound with With leaked: %s", buf.String())
	}
}

// TestKeepsNonSensitiveAttributes confirms redaction is targeted rather than
// blanket: an operator must still be able to see what they need.
func TestKeepsNonSensitiveAttributes(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, Options{Format: "json"})
	log.Info("event", "tenant_id", "tenant-1", "object_id", "obj_123", "bytes", 1024)

	record := decode(t, buf.String())
	if record["tenant_id"] != "tenant-1" {
		t.Errorf("tenant_id = %v, want tenant-1", record["tenant_id"])
	}
	if record["object_id"] != "obj_123" {
		t.Errorf("object_id = %v, want obj_123", record["object_id"])
	}
}

// TestIsSensitiveMatchesSubstrings documents that the check is substring based,
// which is what covers prefixed variants like google_refresh_token.
func TestIsSensitiveMatchesSubstrings(t *testing.T) {
	sensitive := []string{
		"google_refresh_token", "x_amz_credential", "aws_secret_access_key",
		"SESSION_URI_ENCRYPTED", "my_presigned_url", "database_password",
	}
	for _, key := range sensitive {
		if !IsSensitive(key) {
			t.Errorf("IsSensitive(%q) = false, want true", key)
		}
	}

	benign := []string{
		"tenant_id", "object_id", "provider", "status", "duration_ms",
		"request_id", "fallback_reason", "correlation_id",
	}
	for _, key := range benign {
		if IsSensitive(key) {
			t.Errorf("IsSensitive(%q) = true, want false; benign keys must survive", key)
		}
	}
}

func TestContextCorrelationAndTenant(t *testing.T) {
	ctx := context.Background()
	if CorrelationID(ctx) != "" {
		t.Error("an empty context must report no correlation ID")
	}

	ctx = WithCorrelationID(ctx, "corr-1")
	ctx = WithTenant(ctx, "tenant-1", "user-1")

	if CorrelationID(ctx) != "corr-1" {
		t.Errorf("CorrelationID = %q, want corr-1", CorrelationID(ctx))
	}

	var buf bytes.Buffer
	base := New(&buf, Options{Format: "json"})
	With(ctx, base).Info("event")

	record := decode(t, buf.String())
	if record["correlation_id"] != "corr-1" {
		t.Errorf("correlation_id = %v, want corr-1", record["correlation_id"])
	}
	if record["tenant_id"] != "tenant-1" {
		t.Errorf("tenant_id = %v, want tenant-1", record["tenant_id"])
	}
	if record["user_id"] != "user-1" {
		t.Errorf("user_id = %v, want user-1", record["user_id"])
	}
}

func TestWithOnAnEmptyContextIsANoOp(t *testing.T) {
	var buf bytes.Buffer
	base := New(&buf, Options{Format: "json"})
	With(context.Background(), base).Info("event")

	if strings.Contains(buf.String(), "tenant_id") {
		t.Errorf("an empty context must not add empty tenant attributes: %s", buf.String())
	}
}

func TestOptionsStampServiceIdentity(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, Options{Format: "json", Service: "storvia-api", Release: "1.2.3"})
	log.Info("event")

	record := decode(t, buf.String())
	if record["service.name"] != "storvia-api" {
		t.Errorf("service.name = %v, want storvia-api", record["service.name"])
	}
	if record["service.version"] != "1.2.3" {
		t.Errorf("service.version = %v, want 1.2.3", record["service.version"])
	}
}

func TestTextFormatIsAlsoRedacted(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, Options{Format: "text"})
	log.Info("event", "refresh_token", "TEXT-SECRET")

	if strings.Contains(buf.String(), "TEXT-SECRET") {
		t.Errorf("text format leaked a secret: %s", buf.String())
	}
}

func TestLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	log := New(&buf, Options{Format: "json", Level: slog.LevelWarn})
	log.Info("suppressed")
	if buf.Len() != 0 {
		t.Errorf("an info record was emitted at warn level: %s", buf.String())
	}

	log.Warn("emitted")
	if !strings.Contains(buf.String(), "emitted") {
		t.Error("a warn record must be emitted at warn level")
	}
}
