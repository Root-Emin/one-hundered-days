// Package logging sets up structured logging with log/slog and the
// request-scoped fields every line should carry.
//
// slog is in the standard library since Go 1.21, so a service does not need
// zap or zerolog to log structurally. What it does need is discipline:
// consistent field names, honest levels, and nothing secret on the line.
package logging

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"
)

type contextKey string

const (
	requestIDContextKey contextKey = "request_id"
	loggerContextKey    contextKey = "logger"
)

// Field names are constants, because "user_id" in one file and "userId" in
// another means the log aggregator cannot group them.
const (
	FieldRequestID = "request_id"
	FieldMethod    = "method"
	FieldPath      = "path"
	FieldStatus    = "status"
	FieldDuration  = "duration_ms"
	FieldUser      = "user_id"
	FieldError     = "error"
	FieldComponent = "component"
)

// New builds the root logger.
//
//	LOG_FORMAT=json   machine readable, what production wants
//	LOG_FORMAT=text   human readable, what a terminal wants
//	LOG_LEVEL=debug|info|warn|error
func New() *slog.Logger {
	level := parseLevel(os.Getenv("LOG_LEVEL"))

	options := &slog.HandlerOptions{
		Level: level,
		// AddSource is expensive (it walks the stack); enable it only when
		// debugging, never as a default in a hot service.
		AddSource:   level == slog.LevelDebug,
		ReplaceAttr: RedactAttr,
	}

	var handler slog.Handler = slog.NewJSONHandler(os.Stdout, options)

	if strings.EqualFold(os.Getenv("LOG_FORMAT"), "text") {
		handler = slog.NewTextHandler(os.Stdout, options)
	}

	// Service-wide fields belong on the logger, not on every call site.
	return slog.New(handler).With(
		slog.String("service", envOr("SERVICE_NAME", "day71")),
		slog.String("env", envOr("ENV", "development")),
	)
}

func parseLevel(value string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// sensitiveKeys are field names that must never reach a log sink, whatever a
// caller passes. A denylist is not a substitute for not logging secrets, but
// it catches the accident.
var sensitiveKeys = map[string]struct{}{
	"password":      {},
	"passwd":        {},
	"secret":        {},
	"token":         {},
	"access_token":  {},
	"refresh_token": {},
	"authorization": {},
	"api_key":       {},
	"card_number":   {},
	"cvv":           {},
	"ssn":           {},
}

// RedactAttr runs on every attribute of every record. It is exported so the
// tests exercise exactly the function the production handler installs.
//
// This is the safety net, not the policy: the policy is "do not put secrets in
// a log call". Defence in depth matters here because one leaked token in a log
// aggregator is a breach that outlives the process that wrote it.
func RedactAttr(groups []string, attr slog.Attr) slog.Attr {
	if _, sensitive := sensitiveKeys[strings.ToLower(attr.Key)]; sensitive {
		return slog.String(attr.Key, "[REDACTED]")
	}

	// Emails are personal data. Logging a hash or a masked form keeps the line
	// useful for support without storing the address in every index.
	if strings.EqualFold(attr.Key, "email") {
		return slog.String(attr.Key, MaskEmail(attr.Value.String()))
	}

	return attr
}

// MaskEmail keeps enough to recognise an account, not enough to contact it.
func MaskEmail(email string) string {
	local, domain, found := strings.Cut(email, "@")

	if !found || local == "" {
		return "[redacted]"
	}

	if len(local) <= 2 {
		return "**@" + domain
	}

	return local[:1] + strings.Repeat("*", len(local)-2) + local[len(local)-1:] + "@" + domain
}

//
// REQUEST SCOPE
//

// WithRequestID stores the correlation id, and returns a context whose logger
// already carries it - so no handler has to remember to add the field.
func WithRequestID(ctx context.Context, logger *slog.Logger, requestID string) context.Context {
	ctx = context.WithValue(ctx, requestIDContextKey, requestID)

	return context.WithValue(ctx, loggerContextKey, logger.With(slog.String(FieldRequestID, requestID)))
}

// WithLogger attaches an already-decorated logger.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerContextKey, logger)
}

// FromContext returns the request-scoped logger, or the default one.
//
// Returning a usable logger rather than nil means a caller can always log,
// which is what keeps "log or not?" out of business code.
func FromContext(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(loggerContextKey).(*slog.Logger); ok {
		return logger
	}

	return slog.Default()
}

func RequestIDFromContext(ctx context.Context) string {
	id, _ := ctx.Value(requestIDContextKey).(string)

	return id
}

// Duration formats a duration as milliseconds, so dashboards do not have to
// parse "1.234ms" strings.
func Duration(d time.Duration) slog.Attr {
	return slog.Float64(FieldDuration, float64(d.Nanoseconds())/1e6)
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}

	return fallback
}
