package domain_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-100/internal/domain"
)

func TestParseCodeAccepts(t *testing.T) {
	for _, value := range []string{"abc123", "A", "0000000", strings.Repeat("z", domain.MaxCodeLength)} {
		code, err := domain.ParseCode(value)
		if err != nil {
			t.Errorf("ParseCode(%q) = %v", value, err)
		}

		if code.String() != value {
			t.Errorf("code = %q, want %q", code, value)
		}
	}
}

func TestParseCodeRejects(t *testing.T) {
	cases := map[string]string{
		"empty":          "",
		"whitespace":     "   ",
		"too long":       strings.Repeat("a", domain.MaxCodeLength+1),
		"hyphen":         "abc-123",
		"slash":          "abc/123",
		"dot":            "abc.123",
		"space inside":   "abc 123",
		"unicode":        "abcé",
		"reserved api":   "api",
		"reserved mixed": "HealthZ",
	}

	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := domain.ParseCode(value); !errors.Is(err, domain.ErrInvalidCode) {
				t.Errorf("ParseCode(%q) = %v, want ErrInvalidCode", value, err)
			}
		})
	}
}

// Reserved words must be rejected here rather than relying on route
// precedence: the rule has to survive someone adding a route later.
func TestReservedCodesCannotShadowRoutes(t *testing.T) {
	for _, reserved := range []string{"api", "healthz", "readyz", "metrics"} {
		if _, err := domain.ParseCode(reserved); err == nil {
			t.Errorf("ParseCode(%q) was accepted and would shadow a route", reserved)
		}
	}
}

func TestNewCodeIsValidAndRandom(t *testing.T) {
	seen := make(map[domain.Code]bool)

	for i := 0; i < 200; i++ {
		code, err := domain.NewCode()
		if err != nil {
			t.Fatalf("NewCode: %v", err)
		}

		if len(code) != domain.CodeLength {
			t.Fatalf("length = %d, want %d", len(code), domain.CodeLength)
		}

		if _, err := domain.ParseCode(code.String()); err != nil {
			t.Fatalf("generated code %q does not parse: %v", code, err)
		}

		if seen[code] {
			t.Fatalf("duplicate code %q in 200 draws - the generator is not random", code)
		}

		seen[code] = true
	}
}

func TestParseTargetAccepts(t *testing.T) {
	for _, value := range []string{
		"https://example.com",
		"http://example.com/path?query=1",
		"https://example.com:8443/deep/path#fragment",
	} {
		if _, err := domain.ParseTarget(value); err != nil {
			t.Errorf("ParseTarget(%q) = %v", value, err)
		}
	}
}

// The scheme rules are the ones that keep this from being a stored-XSS
// delivery service on your own domain.
func TestParseTargetRejects(t *testing.T) {
	cases := map[string]string{
		"empty":       "",
		"javascript":  "javascript:alert(1)",
		"data":        "data:text/html;base64,PHNjcmlwdD4=",
		"file":        "file:///etc/passwd",
		"relative":    "/just/a/path",
		"scheme only": "https://",
		"too long":    "https://example.com/" + strings.Repeat("a", domain.MaxTargetLength),
		"no scheme":   "example.com",
	}

	for name, value := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := domain.ParseTarget(value); !errors.Is(err, domain.ErrInvalidTarget) {
				t.Errorf("ParseTarget(%q) = %v, want ErrInvalidTarget", value, err)
			}
		})
	}
}

func TestNewLink(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

	code, err := domain.ParseCode("abc1234")
	if err != nil {
		t.Fatalf("ParseCode: %v", err)
	}

	link, err := domain.NewLink(code, "ada", "https://example.com", time.Time{}, now)
	if err != nil {
		t.Fatalf("NewLink: %v", err)
	}

	if !link.Active {
		t.Error("a new link should be active")
	}

	if link.CreatedAt != now {
		t.Errorf("created at = %s, want %s", link.CreatedAt, now)
	}

	if err := link.Followable(now); err != nil {
		t.Errorf("a new link should be followable: %v", err)
	}
}

func TestNewLinkRejectsBadInput(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)
	code := domain.Code("abc1234")

	if _, err := domain.NewLink(code, "", "https://example.com", time.Time{}, now); err == nil {
		t.Error("an empty owner was accepted")
	}

	if _, err := domain.NewLink(code, "ada", "javascript:alert(1)", time.Time{}, now); err == nil {
		t.Error("a javascript: target was accepted")
	}

	// A link born expired is a caller bug, and accepting it means the 410
	// arrives with no explanation.
	if _, err := domain.NewLink(code, "ada", "https://example.com", now.Add(-time.Hour), now); err == nil {
		t.Error("an expiry in the past was accepted")
	}

	if _, err := domain.NewLink(code, "ada", "https://example.com", now, now); err == nil {
		t.Error("an expiry exactly now was accepted")
	}
}

// The distinction the handler needs is not yes/no but WHY not: gone tells a
// crawler to forget the URL, not found invites it back tomorrow.
func TestFollowable(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name    string
		link    domain.Link
		wantErr error
	}{
		{
			name: "active, no expiry",
			link: domain.Link{Active: true},
		},
		{
			name: "active, expiry in the future",
			link: domain.Link{Active: true, ExpiresAt: now.Add(time.Hour)},
		},
		{
			name:    "deactivated",
			link:    domain.Link{Active: false},
			wantErr: domain.ErrGone,
		},
		{
			name:    "expired an hour ago",
			link:    domain.Link{Active: true, ExpiresAt: now.Add(-time.Hour)},
			wantErr: domain.ErrGone,
		},
		{
			name:    "expires exactly now",
			link:    domain.Link{Active: true, ExpiresAt: now},
			wantErr: domain.ErrGone,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.link.Followable(now)

			if testCase.wantErr == nil {
				if err != nil {
					t.Errorf("Followable = %v, want nil", err)
				}

				return
			}

			if !errors.Is(err, testCase.wantErr) {
				t.Errorf("Followable = %v, want %v", err, testCase.wantErr)
			}
		})
	}
}

// Bucketing by local time means the shape of a chart changes when the server
// moves, so the day is always UTC.
func TestClickDayIsUTC(t *testing.T) {
	// 23:30 in a zone 5 hours ahead of UTC is still the previous UTC day.
	zone := time.FixedZone("UTC+5", 5*60*60)

	click := domain.Click{OccurredAt: time.Date(2026, 9, 3, 2, 30, 0, 0, zone)}

	if day := click.Day(); day != "2026-09-02" {
		t.Errorf("Day = %s, want 2026-09-02 (UTC)", day)
	}
}

func TestExpired(t *testing.T) {
	now := time.Date(2026, 9, 2, 12, 0, 0, 0, time.UTC)

	if (domain.Link{}).Expired(now) {
		t.Error("a link with no expiry reported as expired")
	}

	if !(domain.Link{ExpiresAt: now.Add(-time.Second)}).Expired(now) {
		t.Error("a past expiry reported as not expired")
	}
}
