package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

/*
Input validation.

The rule: everything that crosses a trust boundary is untrusted, including
data from an authenticated user. Authentication says who is calling, not that
what they sent is safe.

Validation here is allowlist-shaped - describe what is acceptable and reject
everything else. Denylists ("strip <script>") always lose, because the set of
bad inputs is infinite and the set of good ones is small and knowable.
*/

var ErrInvalidInput = errors.New("invalid input")

//
// PRIMITIVES
//

// emailPattern is deliberately conservative. Full RFC 5322 is not worth
// implementing; delivering a confirmation mail is the real validation.
var emailPattern = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

var usernamePattern = regexp.MustCompile(`^[a-z0-9_-]{3,32}$`)

func ValidateEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))

	switch {
	case value == "":
		return "", fmt.Errorf("%w: email is required", ErrInvalidInput)

	case len(value) > 254:
		// RFC 5321 limit. A length check before a regex also stops
		// catastrophic backtracking on a pathological input.
		return "", fmt.Errorf("%w: email is too long", ErrInvalidInput)

	case !emailPattern.MatchString(value):
		return "", fmt.Errorf("%w: email format is not accepted", ErrInvalidInput)
	}

	return value, nil
}

func ValidateUsername(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))

	if !usernamePattern.MatchString(value) {
		return "", fmt.Errorf("%w: username must be 3-32 characters of a-z, 0-9, _ or -", ErrInvalidInput)
	}

	// Reserved names stop impersonation of system endpoints and pages.
	for _, reserved := range []string{"admin", "root", "system", "support", "api"} {
		if value == reserved {
			return "", fmt.Errorf("%w: username %q is reserved", ErrInvalidInput, value)
		}
	}

	return value, nil
}

// ValidateText enforces length in runes (not bytes), requires valid UTF-8 and
// rejects control characters that break logs, terminals and CSV exports.
func ValidateText(field, value string, minRunes, maxRunes int) (string, error) {
	value = strings.TrimSpace(value)

	if !utf8.ValidString(value) {
		return "", fmt.Errorf("%w: %s is not valid UTF-8", ErrInvalidInput, field)
	}

	count := utf8.RuneCountInString(value)

	switch {
	case count < minRunes:
		return "", fmt.Errorf("%w: %s must be at least %d characters", ErrInvalidInput, field, minRunes)

	case count > maxRunes:
		return "", fmt.Errorf("%w: %s must be at most %d characters", ErrInvalidInput, field, maxRunes)
	}

	for _, char := range value {
		// Tab and newline are allowed in bodies; everything else in the
		// control range is not.
		if unicode.IsControl(char) && char != '\n' && char != '\t' {
			return "", fmt.Errorf("%w: %s contains a control character", ErrInvalidInput, field)
		}
	}

	return value, nil
}

// ValidateEnum is the allowlist in its purest form.
func ValidateEnum(field, value string, allowed ...string) (string, error) {
	for _, candidate := range allowed {
		if value == candidate {
			return value, nil
		}
	}

	return "", fmt.Errorf("%w: %s must be one of %s", ErrInvalidInput, field, strings.Join(allowed, ", "))
}

func ValidateRange(field string, value, minimum, maximum int) (int, error) {
	if value < minimum || value > maximum {
		return 0, fmt.Errorf("%w: %s must be between %d and %d", ErrInvalidInput, field, minimum, maximum)
	}

	return value, nil
}

//
// PATH TRAVERSAL
//

// SafeJoin resolves a user-supplied name inside a base directory and refuses
// anything that escapes it.
//
// "../../etc/passwd" and an absolute path are the obvious attacks; the subtle
// one is that a name can look safe until it is cleaned, which is why the
// containment check happens after path.Clean, not before.
func SafeJoin(base, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("%w: file name is required", ErrInvalidInput)
	}

	if strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("%w: file name contains a null byte", ErrInvalidInput)
	}

	if path.IsAbs(name) || strings.HasPrefix(name, "\\") {
		return "", fmt.Errorf("%w: absolute paths are not allowed", ErrInvalidInput)
	}

	cleanedBase := path.Clean("/" + base)
	joined := path.Clean(cleanedBase + "/" + name)

	if joined != cleanedBase && !strings.HasPrefix(joined, cleanedBase+"/") {
		return "", fmt.Errorf("%w: path escapes the base directory", ErrInvalidInput)
	}

	return joined, nil
}

//
// SSRF
//

// ValidateOutboundURL guards a "fetch this URL for me" feature.
//
// Without this check, a user-supplied URL turns the service into a proxy into
// its own private network: cloud metadata endpoints, internal admin panels,
// databases bound to localhost. Scheme and destination address are both
// checked, because a public hostname can resolve to a private address.
func ValidateOutboundURL(raw string) (*url.URL, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("%w: url is malformed", ErrInvalidInput)
	}

	if parsed.Scheme != "https" && parsed.Scheme != "http" {
		return nil, fmt.Errorf("%w: only http and https are allowed", ErrInvalidInput)
	}

	host := parsed.Hostname()

	if host == "" {
		return nil, fmt.Errorf("%w: url has no host", ErrInvalidInput)
	}

	// Resolution happens here, but a full defence also re-checks the address
	// at dial time (a DialContext hook), because DNS can change in between -
	// the DNS-rebinding attack.
	addresses, err := net.LookupIP(host)
	if err != nil {
		return nil, fmt.Errorf("%w: host does not resolve", ErrInvalidInput)
	}

	for _, address := range addresses {
		if isPrivateAddress(address) {
			return nil, fmt.Errorf("%w: host resolves to a private address (%s)", ErrInvalidInput, address)
		}
	}

	return parsed, nil
}

func isPrivateAddress(address net.IP) bool {
	if address.IsLoopback() || address.IsPrivate() ||
		address.IsLinkLocalUnicast() || address.IsLinkLocalMulticast() ||
		address.IsUnspecified() {
		return true
	}

	// 169.254.169.254 is the cloud metadata endpoint; IsLinkLocalUnicast
	// already covers it, and this makes the intent explicit.
	if address.Equal(net.IPv4(169, 254, 169, 254)) {
		return true
	}

	// Carrier-grade NAT range, routable in some environments.
	cgnat := net.IPNet{
		IP:   net.IPv4(100, 64, 0, 0),
		Mask: net.CIDRMask(10, 32),
	}

	return cgnat.Contains(address)
}

//
// OUTPUT SIDE
//

// EscapeForLog neutralises control characters before user input is written to
// a log line. Without it, a newline in a username lets an attacker forge log
// entries, and an ANSI escape can lie to whoever reads the terminal.
func EscapeForLog(value string) string {
	var builder strings.Builder

	for _, char := range value {
		switch {
		case char == '\n':
			builder.WriteString("\\n")
		case char == '\r':
			builder.WriteString("\\r")
		case char == '\t':
			builder.WriteString("\\t")
		case unicode.IsControl(char):
			builder.WriteString(fmt.Sprintf("\\x%02x", char))
		default:
			builder.WriteRune(char)
		}
	}

	if builder.Len() > 200 {
		return builder.String()[:200] + "..."
	}

	return builder.String()
}
