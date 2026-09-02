package logging_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"example.com/onehundredday/Section-15-Observability-And-Resilience-Structured-logging-metrics-tracing-retries-circuit-breakers-and-timeouts/Day-71/internal/logging"
)

// captureLogger writes JSON lines into a buffer so a test can assert on the
// structure rather than on a formatted string.
func captureLogger(t *testing.T) (*slog.Logger, *bytes.Buffer) {
	t.Helper()

	buffer := &bytes.Buffer{}

	// The same options the real handler uses, so the redaction under test is
	// the redaction that ships.
	handler := slog.NewJSONHandler(buffer, &slog.HandlerOptions{
		Level:       slog.LevelDebug,
		ReplaceAttr: logging.RedactAttr,
	})

	return slog.New(handler), buffer
}

func decodeLines(t *testing.T, buffer *bytes.Buffer) []map[string]any {
	t.Helper()

	var records []map[string]any

	for _, line := range strings.Split(strings.TrimSpace(buffer.String()), "\n") {
		if line == "" {
			continue
		}

		var record map[string]any

		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatalf("log line is not JSON: %q (%v)", line, err)
		}

		records = append(records, record)
	}

	return records
}

func TestSecretsAreRedacted(t *testing.T) {
	t.Parallel()

	logger, buffer := captureLogger(t)

	logger.Info("login attempt",
		slog.String("user_id", "u-1"),
		slog.String("password", "hunter2"),
		slog.String("token", "eyJhbGciOi..."),
		slog.String("authorization", "Bearer secret"),
		slog.String("api_key", "sk_live_123"),
	)

	line := buffer.String()

	for _, secret := range []string{"hunter2", "eyJhbGciOi", "Bearer secret", "sk_live_123"} {
		if strings.Contains(line, secret) {
			t.Fatalf("a secret reached the log: %q in %s", secret, line)
		}
	}

	records := decodeLines(t, buffer)

	if records[0]["password"] != "[REDACTED]" {
		t.Fatalf("password = %v, want [REDACTED]", records[0]["password"])
	}

	// The non-secret field is untouched: redaction must not eat useful data.
	if records[0]["user_id"] != "u-1" {
		t.Fatalf("user_id = %v", records[0]["user_id"])
	}
}

func TestEmailIsMasked(t *testing.T) {
	t.Parallel()

	logger, buffer := captureLogger(t)

	logger.Info("order created", slog.String("email", "ada.lovelace@example.com"))

	records := decodeLines(t, buffer)

	masked, _ := records[0]["email"].(string)

	if strings.Contains(masked, "ada.lovelace") {
		t.Fatalf("the address was not masked: %q", masked)
	}

	// Still recognisable enough for support to match an account.
	if !strings.HasSuffix(masked, "@example.com") || !strings.HasPrefix(masked, "a") {
		t.Fatalf("masked email = %q, want a****e@example.com shape", masked)
	}
}

func TestMaskEmail(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"ada@example.com": "a*a@example.com",
		"ab@example.com":  "**@example.com",
		"a@example.com":   "**@example.com",
		"not-an-email":    "[redacted]",
		"":                "[redacted]",
	}

	for input, want := range tests {
		if got := logging.MaskEmail(input); got != want {
			t.Errorf("MaskEmail(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestRequestScopedLoggerCarriesTheID(t *testing.T) {
	t.Parallel()

	logger, buffer := captureLogger(t)

	ctx := logging.WithRequestID(context.Background(), logger, "req-123")

	// A handler somewhere deep in the call stack logs without knowing about
	// request ids at all.
	logging.FromContext(ctx).Info("something happened")

	records := decodeLines(t, buffer)

	if records[0][logging.FieldRequestID] != "req-123" {
		t.Fatalf("request_id = %v, want req-123", records[0][logging.FieldRequestID])
	}

	if logging.RequestIDFromContext(ctx) != "req-123" {
		t.Fatal("request id is not readable from the context")
	}
}

func TestFromContextAlwaysReturnsALogger(t *testing.T) {
	t.Parallel()

	// No logger in the context: callers must still be able to log rather than
	// nil-check at every site.
	if logging.FromContext(context.Background()) == nil {
		t.Fatal("FromContext returned nil")
	}
}

func TestDurationIsMilliseconds(t *testing.T) {
	t.Parallel()

	logger, buffer := captureLogger(t)

	logger.Info("done", logging.Duration(1500*1000*1000)) // 1.5s

	records := decodeLines(t, buffer)

	value, ok := records[0][logging.FieldDuration].(float64)

	if !ok || value < 1499 || value > 1501 {
		t.Fatalf("duration_ms = %v, want ~1500", records[0][logging.FieldDuration])
	}
}
