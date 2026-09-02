// Package auth issues and verifies API keys.
//
// The design, and why (ADR 0004):
//
//   - the key is 32 bytes from crypto/rand, base64url encoded, with a "lk_"
//     prefix so it is recognisable in a log or a leaked paste
//   - it is shown ONCE, at creation, and stored as a SHA-256 hash
//   - verification hashes the presented key and looks the hash up, so the
//     plaintext never reaches the database or a query log
//
// SHA-256 rather than bcrypt because the input is 256 bits of entropy from a
// CSPRNG: brute force is not the threat model, and this check runs on every
// API request. bcrypt is for passwords, which humans choose badly.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"example.com/onehundredday/Section-20-Capstone-And-Professional-Delivery-Capstone-planning-hardening-deployment-CI-CD-and-final-review/Day-98/internal/domain"
)

// Prefix marks a Linkr key. A recognisable prefix is what lets a secret
// scanner spot one in a commit before an attacker does.
const Prefix = "lk_"

// keyBytes is the entropy behind a key. 32 bytes is 256 bits.
const keyBytes = 32

// GeneratedKey is what a new key looks like exactly once.
type GeneratedKey struct {
	// ID identifies the key without revealing it, for logs and revocation.
	ID string
	// Plaintext is shown to the user once and never stored.
	Plaintext string
	// Hash is what goes in the database.
	Hash string
}

// Generate creates a key.
func Generate() (GeneratedKey, error) {
	buffer := make([]byte, keyBytes)

	if _, err := rand.Read(buffer); err != nil {
		// No entropy means no key. A predictable credential is worse than no
		// credential, so there is no fallback here.
		return GeneratedKey{}, fmt.Errorf("generate key: %w", err)
	}

	plaintext := Prefix + base64.RawURLEncoding.EncodeToString(buffer)

	identifier := make([]byte, 8)

	if _, err := rand.Read(identifier); err != nil {
		return GeneratedKey{}, fmt.Errorf("generate key id: %w", err)
	}

	return GeneratedKey{
		ID:        hex.EncodeToString(identifier),
		Plaintext: plaintext,
		Hash:      Hash(plaintext),
	}, nil
}

// Hash returns the stored form of a key.
func Hash(plaintext string) string {
	sum := sha256.Sum256([]byte(plaintext))

	return hex.EncodeToString(sum[:])
}

// Equal compares two hashes in constant time.
//
// A timing side channel on a hash comparison is unlikely to be exploitable,
// and the constant-time version costs nothing - which makes choosing the
// leaky one hard to justify in review.
func Equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}

// Resolver maps a key hash to its owner.
type Resolver interface {
	// OwnerForHash returns domain.ErrUnauthorized when the hash is unknown.
	OwnerForHash(ctx context.Context, hash string) (string, error)
}

// contextKey is unexported so nothing outside this package can forge an
// identity by writing to the same context key.
type contextKey string

const ownerKey contextKey = "owner"

// OwnerFrom returns the authenticated owner, if any.
func OwnerFrom(ctx context.Context) (string, bool) {
	owner, ok := ctx.Value(ownerKey).(string)

	return owner, ok && owner != ""
}

// WithOwner attaches an owner, for tests and for handlers that authenticate
// by another route.
func WithOwner(ctx context.Context, owner string) context.Context {
	return context.WithValue(ctx, ownerKey, owner)
}

// ExtractKey reads the credential from a request.
//
// Authorization: Bearer <key> only. Not a query parameter: a key in a URL ends
// up in access logs, in Referer headers sent to third parties, and in browser
// history - three places nobody audits.
func ExtractKey(r *http.Request) (string, error) {
	header := r.Header.Get("Authorization")
	if header == "" {
		return "", fmt.Errorf("%w: no Authorization header", domain.ErrUnauthorized)
	}

	scheme, credential, found := strings.Cut(header, " ")
	if !found || !strings.EqualFold(scheme, "bearer") {
		return "", fmt.Errorf("%w: expected \"Authorization: Bearer <key>\"", domain.ErrUnauthorized)
	}

	credential = strings.TrimSpace(credential)
	if credential == "" {
		return "", fmt.Errorf("%w: empty bearer token", domain.ErrUnauthorized)
	}

	return credential, nil
}

// Middleware authenticates a request and attaches the owner.
//
// It writes a 401 and stops; it never falls through. A middleware that
// forwards an unauthenticated request "for the handler to check" is how an
// endpoint ends up unprotected after a refactor.
func Middleware(resolver Resolver, onError func(http.ResponseWriter, *http.Request, error)) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			key, err := ExtractKey(r)
			if err != nil {
				onError(w, r, err)

				return
			}

			owner, err := resolver.OwnerForHash(r.Context(), Hash(key))
			if err != nil {
				if errors.Is(err, domain.ErrUnauthorized) {
					// The response says "unauthorized" and nothing else: "no
					// such key" and "key belongs to another account" are the
					// same answer to a caller and different answers to an
					// attacker.
					onError(w, r, fmt.Errorf("%w: unknown key", domain.ErrUnauthorized))

					return
				}

				onError(w, r, err)

				return
			}

			next.ServeHTTP(w, r.WithContext(WithOwner(r.Context(), owner)))
		})
	}
}
