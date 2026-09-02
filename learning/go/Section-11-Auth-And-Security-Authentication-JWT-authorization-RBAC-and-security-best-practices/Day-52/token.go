package main

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

/*
JWT issuing and validation.

The rules that make the difference between a token library and a security
hole:

  - the signing algorithm is pinned. A verifier that accepts whatever the
    token's header says will happily accept alg=none, or an HMAC token signed
    with the public RSA key it published.
  - issuer and audience are checked. A token minted by a different service of
    the same company must not open this one.
  - expiry is short. A stateless token cannot be un-issued, so its lifetime is
    the revocation window.
  - the secret comes from the environment, never from source.
  - a key id (kid) in the header makes rotation possible without a flag day.
*/

var (
	ErrInvalidToken = errors.New("invalid token")
	ErrExpiredToken = errors.New("token expired")
	ErrRevoked      = errors.New("token revoked")
)

const (
	issuer   = "onehundredday.day52"
	audience = "onehundredday.api"

	// Short-lived: this is the blast radius of a stolen access token.
	AccessTokenTTL = 15 * time.Minute

	// Long-lived but revocable, because it is stored server side.
	RefreshTokenTTL = 30 * 24 * time.Hour
)

//
// CLAIMS
//

// Claims embeds jwt.RegisteredClaims (iss, sub, aud, exp, nbf, iat, jti) and
// adds only what the API actually needs. Every claim is data the client can
// read: a JWT is signed, not encrypted, so nothing secret goes in here.
type Claims struct {
	Email string   `json:"email"`
	Roles []string `json:"roles"`

	jwt.RegisteredClaims
}

func (c Claims) HasRole(role string) bool {
	for _, candidate := range c.Roles {
		if candidate == role {
			return true
		}
	}

	return false
}

//
// KEYRING
//

// Keyring holds the active signing key plus any previous keys still accepted
// for verification. That overlap is what makes rotation possible: sign with
// the new key, keep verifying the old one until every issued token has
// expired, then drop it.
type Keyring struct {
	activeKID string
	keys      map[string][]byte
}

// LoadKeyring reads keys from the environment:
//
//	JWT_SIGNING_KEY      the active secret (required in production)
//	JWT_SIGNING_KEY_ID   its id, written into the token header. Default: "v1"
//	JWT_PREVIOUS_KEYS    optional "kid:secret,kid:secret" still accepted
//
// A secret in source control is a secret in every fork, every CI log and
// every laptop backup. Environment (or a secret manager) is the minimum bar.
func LoadKeyring() (*Keyring, error) {
	active := strings.TrimSpace(os.Getenv("JWT_SIGNING_KEY"))

	if active == "" {
		// Development convenience with a loud warning. A production build
		// should fail instead - see the check in main.
		generated := make([]byte, 32)

		if _, err := rand.Read(generated); err != nil {
			return nil, fmt.Errorf("generate development key: %w", err)
		}

		active = base64.RawURLEncoding.EncodeToString(generated)
	}

	if len(active) < 32 {
		return nil, fmt.Errorf("JWT_SIGNING_KEY must be at least 32 characters, got %d", len(active))
	}

	kid := strings.TrimSpace(os.Getenv("JWT_SIGNING_KEY_ID"))
	if kid == "" {
		kid = "v1"
	}

	keyring := &Keyring{
		activeKID: kid,
		keys:      map[string][]byte{kid: []byte(active)},
	}

	for _, pair := range strings.Split(os.Getenv("JWT_PREVIOUS_KEYS"), ",") {
		pair = strings.TrimSpace(pair)

		if pair == "" {
			continue
		}

		previousKID, secret, found := strings.Cut(pair, ":")
		if !found || previousKID == "" || secret == "" {
			return nil, fmt.Errorf("JWT_PREVIOUS_KEYS entry %q must be kid:secret", pair)
		}

		keyring.keys[previousKID] = []byte(secret)
	}

	return keyring, nil
}

func (k *Keyring) lookup(token *jwt.Token) (any, error) {
	kid, _ := token.Header["kid"].(string)

	if kid == "" {
		// Tokens without a kid cannot be rotated safely; reject them rather
		// than guessing which key to try.
		return nil, fmt.Errorf("%w: missing kid header", ErrInvalidToken)
	}

	key, ok := k.keys[kid]
	if !ok {
		return nil, fmt.Errorf("%w: unknown key id %q", ErrInvalidToken, kid)
	}

	return key, nil
}

//
// TOKEN SERVICE
//

type TokenService struct {
	keyring *Keyring

	// revoked holds the jti of access tokens that must stop working before
	// they expire (logout, compromised device). It is the pragmatic answer to
	// "JWTs cannot be revoked": a denylist that only has to remember tokens
	// for as long as they would have been valid anyway.
	mu      sync.RWMutex
	revoked map[string]time.Time
}

func NewTokenService(keyring *Keyring) *TokenService {
	return &TokenService{
		keyring: keyring,
		revoked: make(map[string]time.Time),
	}
}

// Issue signs an access token for a user.
func (s *TokenService) Issue(userID int64, email string, roles []string, ttl time.Duration) (string, Claims, error) {
	now := time.Now().UTC()

	jti, err := randomID()
	if err != nil {
		return "", Claims{}, err
	}

	claims := Claims{
		Email: email,
		Roles: roles,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    issuer,
			Subject:   fmt.Sprintf("%d", userID),
			Audience:  jwt.ClaimStrings{audience},
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
			NotBefore: jwt.NewNumericDate(now),
			IssuedAt:  jwt.NewNumericDate(now),
			ID:        jti,
		},
	}

	// HS256: symmetric, fine when the same service issues and verifies.
	// Use RS256/ES256 when a different party must verify without being able
	// to mint tokens.
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	token.Header["kid"] = s.keyring.activeKID

	signed, err := token.SignedString(s.keyring.keys[s.keyring.activeKID])
	if err != nil {
		return "", Claims{}, fmt.Errorf("sign token: %w", err)
	}

	return signed, claims, nil
}

// Verify parses and validates a token, returning its claims.
func (s *TokenService) Verify(raw string) (Claims, error) {
	var claims Claims

	_, err := jwt.ParseWithClaims(
		raw,
		&claims,
		s.keyring.lookup,
		// The allowlist. Without it, a token header can choose the algorithm,
		// which is how "alg: none" and RSA-to-HMAC confusion attacks work.
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(issuer),
		jwt.WithAudience(audience),
		jwt.WithExpirationRequired(),
		// A little clock skew tolerance; not enough to matter for a stolen
		// token, enough to survive an unsynced VM.
		jwt.WithLeeway(30*time.Second),
	)

	switch {
	case errors.Is(err, jwt.ErrTokenExpired):
		return Claims{}, fmt.Errorf("%w: %w", ErrExpiredToken, err)

	case err != nil:
		return Claims{}, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}

	if claims.Subject == "" {
		return Claims{}, fmt.Errorf("%w: missing subject", ErrInvalidToken)
	}

	if s.IsRevoked(claims.ID) {
		return Claims{}, ErrRevoked
	}

	return claims, nil
}

func (s *TokenService) Revoke(claims Claims) {
	if claims.ID == "" || claims.ExpiresAt == nil {
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	s.revoked[claims.ID] = claims.ExpiresAt.Time
}

func (s *TokenService) IsRevoked(jti string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, found := s.revoked[jti]

	return found
}

// PurgeRevoked drops denylist entries whose tokens have expired anyway. This
// is what keeps the "JWTs cannot be revoked" workaround bounded in memory:
// the list never grows past one token lifetime of traffic.
func (s *TokenService) PurgeRevoked() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	removed := 0

	for jti, expiry := range s.revoked {
		if now.After(expiry) {
			delete(s.revoked, jti)

			removed++
		}
	}

	return removed
}

//
// REFRESH TOKENS
//
// Opaque, random, stored server side as a hash - exactly like the sessions of
// Day 51. Refresh tokens are the revocable half of the design: the access
// token stays stateless and short, the refresh token is stateful and long.
//

type RefreshToken struct {
	UserID    int64
	ExpiresAt time.Time
	Used      bool
}

type RefreshStore struct {
	mu     sync.Mutex
	tokens map[string]RefreshToken // key: SHA-256 of the token
}

func NewRefreshStore() *RefreshStore {
	return &RefreshStore{tokens: make(map[string]RefreshToken)}
}

func (s *RefreshStore) Issue(userID int64, ttl time.Duration) (string, error) {
	raw := make([]byte, 32)

	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate refresh token: %w", err)
	}

	token := base64.RawURLEncoding.EncodeToString(raw)

	s.mu.Lock()
	defer s.mu.Unlock()

	s.tokens[hashToken(token)] = RefreshToken{
		UserID:    userID,
		ExpiresAt: time.Now().UTC().Add(ttl),
	}

	return token, nil
}

// Redeem implements refresh token rotation: a token is valid exactly once,
// and reuse of an already-redeemed token means it leaked, so every session of
// that user is dropped.
func (s *RefreshStore) Redeem(token string) (int64, error) {
	key := hashToken(token)

	s.mu.Lock()
	defer s.mu.Unlock()

	stored, found := s.tokens[key]
	if !found {
		return 0, fmt.Errorf("%w: unknown refresh token", ErrInvalidToken)
	}

	if stored.Used {
		// Replay detected. Assume the token was stolen and revoke the family.
		for candidate, value := range s.tokens {
			if value.UserID == stored.UserID {
				delete(s.tokens, candidate)
			}
		}

		return 0, fmt.Errorf("%w: refresh token replayed, all sessions revoked", ErrRevoked)
	}

	if time.Now().UTC().After(stored.ExpiresAt) {
		delete(s.tokens, key)

		return 0, ErrExpiredToken
	}

	stored.Used = true
	s.tokens[key] = stored

	return stored.UserID, nil
}

func (s *RefreshStore) RevokeAll(userID int64) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	removed := 0

	for key, value := range s.tokens {
		if value.UserID == userID {
			delete(s.tokens, key)

			removed++
		}
	}

	return removed
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))

	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func randomID() (string, error) {
	raw := make([]byte, 16)

	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate id: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(raw), nil
}
