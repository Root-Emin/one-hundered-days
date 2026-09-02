// Package domain holds Linkr's rules, and nothing else.
//
// It imports no transport, no database, no logger. The test for whether
// something belongs here: "is this link followable right now?" must be
// answerable without a server and without a database, because that is the
// question the whole service exists to answer.
//
// Everything in this package is a pure function of its inputs, which is why it
// has the densest tests in the project and the fastest ones.
package domain

import (
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"net/url"
	"strings"
	"time"
)

// Sentinel errors. These are the API; the message text is not.
var (
	// ErrInvalidCode means a code is empty, too long, or uses characters
	// outside the alphabet.
	ErrInvalidCode = errors.New("invalid code")
	// ErrInvalidTarget means the target URL is not something worth
	// redirecting to.
	ErrInvalidTarget = errors.New("invalid target url")
	// ErrNotFound means no link has that code.
	ErrNotFound = errors.New("link not found")
	// ErrGone means the link exists but is no longer followable.
	ErrGone = errors.New("link is gone")
	// ErrCodeTaken means the code is already in use. The caller retries with
	// a fresh one; with 62^7 codes this is rare enough to be a formality.
	ErrCodeTaken = errors.New("code already taken")
	// ErrUnauthorized means the credential is missing or unknown.
	ErrUnauthorized = errors.New("unauthorized")
	// ErrAlreadyProcessed means an event was already applied. It is not a
	// failure: at-least-once delivery makes it the expected outcome of a
	// redelivery, and the caller acks and moves on.
	ErrAlreadyProcessed = errors.New("event already processed")
)

// Alphabet is the character set codes are drawn from.
//
// Base62 - no punctuation, no separators - because a code ends up in a URL, in
// a spoken sentence and in a support ticket. Characters that need escaping,
// or that a reader has to spell out, cost more than the length they save.
const Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// CodeLength is the length of a generated code.
//
// 62^7 is about 3.5e12. At a million links, the chance a fresh random code
// collides is roughly 3e-7 - rare enough that the retry on insert is a
// formality, cheap enough that keeping it costs nothing.
const CodeLength = 7

// MaxCodeLength bounds a custom code. Long enough to be memorable, short
// enough that the column and the cache key stay small.
const MaxCodeLength = 32

// Code is a validated short code.
//
// It is a distinct type rather than a string so that a function taking a Code
// cannot be handed a target URL, an owner id, or an unvalidated path segment
// by mistake. Parse, do not validate: once you hold a Code, it is one.
type Code string

// ParseCode validates a code and returns it.
func ParseCode(value string) (Code, error) {
	trimmed := strings.TrimSpace(value)

	switch {
	case trimmed == "":
		return "", fmt.Errorf("%w: empty", ErrInvalidCode)
	case len(trimmed) > MaxCodeLength:
		return "", fmt.Errorf("%w: longer than %d characters", ErrInvalidCode, MaxCodeLength)
	}

	for _, character := range trimmed {
		if !strings.ContainsRune(Alphabet, character) {
			return "", fmt.Errorf("%w: %q is not in the alphabet", ErrInvalidCode, character)
		}
	}

	// Reserved words would shadow real routes. Rejecting them here - rather
	// than relying on route precedence - means the rule is testable without a
	// router, and cannot be broken by adding a route later.
	if reserved[strings.ToLower(trimmed)] {
		return "", fmt.Errorf("%w: %q is reserved", ErrInvalidCode, trimmed)
	}

	return Code(trimmed), nil
}

var reserved = map[string]bool{
	"api": true, "healthz": true, "readyz": true, "metrics": true,
	"static": true, "assets": true, "favicon.ico": true, "robots.txt": true,
	"admin": true, "docs": true,
}

// String returns the code as a plain string.
func (c Code) String() string {
	return string(c)
}

// NewCode generates a random code.
//
// crypto/rand, not math/rand: a predictable code is an enumerable corpus, and
// short links are routinely used for documents whose only protection is that
// the URL is unguessable.
func NewCode() (Code, error) {
	builder := make([]byte, CodeLength)

	limit := big.NewInt(int64(len(Alphabet)))

	for i := range builder {
		index, err := rand.Int(rand.Reader, limit)
		if err != nil {
			// crypto/rand failing means the system has no entropy source.
			// There is no sensible fallback: a predictable code is worse than
			// no code.
			return "", fmt.Errorf("generate code: %w", err)
		}

		builder[i] = Alphabet[index.Int64()]
	}

	return Code(builder), nil
}

// MaxTargetLength bounds the stored URL. Browsers stop being reliable well
// before this, and an unbounded column is a denial-of-service vector.
const MaxTargetLength = 2048

// ParseTarget validates a destination URL.
//
// The rules exist to stop the service being used as an open redirector into
// something it should not vouch for:
//
//   - http and https only: a javascript: or data: target turns every link into
//     a stored XSS delivered from your domain
//   - an absolute URL with a host, so the redirect cannot be relative to this
//     service and loop back into it
func ParseTarget(value string) (string, error) {
	trimmed := strings.TrimSpace(value)

	switch {
	case trimmed == "":
		return "", fmt.Errorf("%w: empty", ErrInvalidTarget)
	case len(trimmed) > MaxTargetLength:
		return "", fmt.Errorf("%w: longer than %d characters", ErrInvalidTarget, MaxTargetLength)
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("%w: %s", ErrInvalidTarget, err)
	}

	switch {
	case parsed.Scheme != "http" && parsed.Scheme != "https":
		return "", fmt.Errorf("%w: scheme %q is not http or https", ErrInvalidTarget, parsed.Scheme)
	case parsed.Host == "":
		return "", fmt.Errorf("%w: no host", ErrInvalidTarget)
	}

	return parsed.String(), nil
}

// Link is a short code pointing at a target.
type Link struct {
	Code      Code
	Owner     string
	Target    string
	Active    bool
	ExpiresAt time.Time // zero means no expiry
	CreatedAt time.Time
	Clicks    int64
}

// NewLink builds a validated link.
func NewLink(code Code, owner, target string, expiresAt time.Time, now time.Time) (Link, error) {
	validTarget, err := ParseTarget(target)
	if err != nil {
		return Link{}, err
	}

	if owner = strings.TrimSpace(owner); owner == "" {
		return Link{}, errors.New("owner is required")
	}

	if !expiresAt.IsZero() && !expiresAt.After(now) {
		// A link that is born expired is a bug in the caller, and accepting it
		// silently means the 410 arrives with no explanation.
		return Link{}, fmt.Errorf("%w: expiry %s is not in the future",
			ErrInvalidTarget, expiresAt.Format(time.RFC3339))
	}

	return Link{
		Code:      code,
		Owner:     owner,
		Target:    validTarget,
		Active:    true,
		ExpiresAt: expiresAt,
		CreatedAt: now,
	}, nil
}

// Expired reports whether the link's expiry has passed.
func (l Link) Expired(now time.Time) bool {
	return !l.ExpiresAt.IsZero() && !now.Before(l.ExpiresAt)
}

// Followable reports whether a redirect should be served.
//
// The distinction the handler needs is not "yes/no" but WHY not: a deactivated
// or expired link is 410 Gone, and a missing one is 404. Gone tells a crawler
// to forget the URL; Not Found invites it to try again tomorrow.
func (l Link) Followable(now time.Time) error {
	switch {
	case !l.Active:
		return fmt.Errorf("%w: deactivated", ErrGone)
	case l.Expired(now):
		return fmt.Errorf("%w: expired at %s", ErrGone, l.ExpiresAt.Format(time.RFC3339))
	default:
		return nil
	}
}

// Click is one recorded follow of a link.
type Click struct {
	Code       Code
	OccurredAt time.Time
	Referrer   string
	UserAgent  string
}

// Day returns the UTC date the click belongs to, which is the key
// click_daily aggregates on.
//
// UTC, always: bucketing by local time means the shape of a chart changes when
// the server moves, and a day boundary that depends on the machine is not a
// day boundary.
func (c Click) Day() string {
	return c.OccurredAt.UTC().Format(time.DateOnly)
}
