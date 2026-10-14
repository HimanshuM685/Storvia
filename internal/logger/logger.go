// Package logger provides the single structured logging entry point.
//
// Two things matter here beyond wiring slog: correlation context that flows with
// a request, and redaction that keeps tokens, secrets, session URIs, and signed
// URLs out of every log line. The sensitive key list is applied by the handler
// itself, so a careless caller cannot leak by forgetting.
package logger

import (
	"context"
	"io"
	"log/slog"
	"strings"
)

type contextKey struct{}

// sensitiveKeys are never written, at any level, in any attribute. Comparison
// is case-insensitive and substring based so that access_token, google_refresh_token,
// and X-Amz-Signature are all covered by one entry each.
var sensitiveKeys = []string{
	"access_token",
	"refresh_token",
	"id_token",
	"authorization",
	"cookie",
	"set-cookie",
	"client_secret",
	"secret",
	"password",
	"api_key",
	"apikey",
	"x-amz-signature",
	"x-amz-credential",
	"x_amz_signature",
	"x_amz_credential",
	"session_uri",
	"session_uri_encrypted",
	"presigned",
	"signature",
	"private_key",
	"encryption_key",
}

// redacted is the placeholder written in place of a sensitive value.
const redacted = "[REDACTED]"

// IsSensitive reports whether a log key must be redacted.
func IsSensitive(key string) bool {
	lower := strings.ToLower(key)
	for _, candidate := range sensitiveKeys {
		if strings.Contains(lower, candidate) {
			return true
		}
	}
	return false
}

// RedactHandler wraps an slog.Handler and replaces the value of every sensitive
// attribute with a fixed placeholder. It keeps the key so an operator can still
// see that a token was present, which is what makes revocation debugging
// possible without printing the token.
func RedactHandler(inner slog.Handler) slog.Handler {
	return &redactHandler{inner: inner}
}

type redactHandler struct{ inner slog.Handler }

func (h *redactHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *redactHandler) Handle(ctx context.Context, r slog.Record) error {
	out := slog.NewRecord(r.Time, r.Level, r.Message, r.PC)
	r.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(redactAttr(a))
		return true
	})
	return h.inner.Handle(ctx, out)
}

func (h *redactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		redacted = append(redacted, redactAttr(a))
	}
	return &redactHandler{inner: h.inner.WithAttrs(redacted)}
}

func (h *redactHandler) WithGroup(name string) slog.Handler {
	return &redactHandler{inner: h.inner.WithGroup(name)}
}

// redactAttr redacts a single attribute, descending into groups so that a token
// nested under logger.WithGroup("google") is still caught.
func redactAttr(a slog.Attr) slog.Attr {
	if IsSensitive(a.Key) {
		return slog.String(a.Key, redacted)
	}
	if a.Value.Kind() == slog.KindGroup {
		group := a.Value.Group()
		out := make([]any, 0, len(group))
		for _, member := range group {
			out = append(out, redactAttr(member))
		}
		return slog.Group(a.Key, out...)
	}
	return a
}

// Options configures New.
type Options struct {
	// Level is the minimum level emitted. Defaults to slog.LevelInfo.
	Level slog.Level
	// Format is "json" or "text". Defaults to "json".
	Format string
	// Service is added to every record as service.name.
	Service string
	// Release is added to every record as service.version.
	Release string
}

// New builds the process logger. It always applies redaction, so no caller can
// construct an unredacted production logger by accident.
func New(w io.Writer, opts Options) *slog.Logger {
	level := opts.Level
	if level == 0 {
		level = slog.LevelInfo
	}
	handlerOpts := &slog.HandlerOptions{Level: level}

	var base slog.Handler
	switch strings.ToLower(opts.Format) {
	case "text":
		base = slog.NewTextHandler(w, handlerOpts)
	default:
		base = slog.NewJSONHandler(w, handlerOpts)
	}

	logger := slog.New(RedactHandler(base))
	if opts.Service == "" && opts.Release == "" {
		return logger
	}
	attrs := make([]any, 0, 2)
	if opts.Service != "" {
		attrs = append(attrs, slog.String("service.name", opts.Service))
	}
	if opts.Release != "" {
		attrs = append(attrs, slog.String("service.version", opts.Release))
	}
	return logger.With(attrs...)
}

// Context keys used to carry request-scoped values. The typed key struct keeps
// them from colliding with other packages' context values.
type contextKeyField string

const (
	correlationIDKey contextKeyField = "correlation_id"
	tenantIDKey      contextKeyField = "tenant_id"
	userIDKey        contextKeyField = "user_id"
	requestIDKey     contextKeyField = "request_id"
)

// WithCorrelationID attaches a correlation ID to the context.
func WithCorrelationID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, correlationIDKey, id)
}

// CorrelationID returns the correlation ID, or "" when none is set.
func CorrelationID(ctx context.Context) string {
	v, _ := ctx.Value(correlationIDKey).(string)
	return v
}

// WithTenant attaches the resolved tenant and user to the context.
func WithTenant(ctx context.Context, tenantID, userID string) context.Context {
	ctx = context.WithValue(ctx, tenantIDKey, tenantID)
	return context.WithValue(ctx, userIDKey, userID)
}

// With returns a logger that stamps the correlation ID and, when present, the
// tenant and user onto every record. Provider file IDs and user file names are
// deliberately absent from this set.
func With(ctx context.Context, l *slog.Logger) *slog.Logger {
	if l == nil {
		l = slog.Default()
	}
	attrs := make([]any, 0, 3)
	if id := CorrelationID(ctx); id != "" {
		attrs = append(attrs, slog.String(string(correlationIDKey), id))
	}
	if tenant, ok := ctx.Value(tenantIDKey).(string); ok && tenant != "" {
		attrs = append(attrs, slog.String(string(tenantIDKey), tenant))
	}
	if user, ok := ctx.Value(userIDKey).(string); ok && user != "" {
		attrs = append(attrs, slog.String(string(userIDKey), user))
	}
	if len(attrs) == 0 {
		return l
	}
	return l.With(attrs...)
}
