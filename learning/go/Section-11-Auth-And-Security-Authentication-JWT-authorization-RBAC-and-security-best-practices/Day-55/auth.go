package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/time/rate"
)

/*
Everything Section 11 built, assembled: password hashing (Day 51), JWTs
(Day 52), roles (Day 53), validation and rate limiting (Day 54).
*/

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrInvalidToken       = errors.New("invalid token")
	ErrExpiredToken       = errors.New("token expired")
	ErrInvalidInput       = errors.New("invalid input")
	ErrForbidden          = errors.New("forbidden")
	ErrSuspended          = errors.New("account suspended")
)

//
// ROLES (Day 53)
//

type Role string

const (
	RoleMember Role = "member"
	RoleEditor Role = "editor"
	RoleAdmin  Role = "admin"
)

type Permission string

const (
	PermNoteCreate    Permission = "note:create"
	PermNoteReadAny   Permission = "note:read:any"
	PermNoteDeleteAny Permission = "note:delete:any"
	PermUserList      Permission = "user:list"
	PermUserSuspend   Permission = "user:suspend"
	PermAuditRead     Permission = "audit:read"
)

var rolePermissions = map[Role][]Permission{
	RoleMember: {PermNoteCreate},
	RoleEditor: {PermNoteCreate, PermNoteReadAny},
	RoleAdmin:  {PermNoteCreate, PermNoteReadAny, PermNoteDeleteAny, PermUserList, PermUserSuspend, PermAuditRead},
}

func (r Role) Valid() bool {
	_, known := rolePermissions[r]

	return known
}

func (r Role) Can(permission Permission) bool {
	for _, granted := range rolePermissions[r] {
		if granted == permission {
			return true
		}
	}

	return false
}

// AuthorizeNote combines the role check with ownership: members see and delete
// their own notes, editors read any, admins delete any.
func AuthorizeNote(user User, action string, note Note) error {
	switch action {
	case "read":
		if note.OwnerID == user.ID || user.Role.Can(PermNoteReadAny) {
			return nil
		}

	case "delete":
		if note.OwnerID == user.ID || user.Role.Can(PermNoteDeleteAny) {
			return nil
		}

	default:
		// Default deny: an action nobody wrote a rule for is not allowed.
		return fmt.Errorf("%w: unknown action %q", ErrForbidden, action)
	}

	return fmt.Errorf("%w: user %d (%s) may not %s note %d",
		ErrForbidden, user.ID, user.Role, action, note.ID)
}

//
// PASSWORDS (Day 51)
//

type PasswordHasher struct {
	Cost int
}

func (h PasswordHasher) Hash(password string) (string, error) {
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), h.Cost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}

	return string(hashed), nil
}

func (h PasswordHasher) Verify(password, hash string) error {
	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return ErrInvalidCredentials
	}

	return nil
}

//
// VALIDATION (Day 54)
//

var emailPattern = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

func ValidateEmail(value string) (string, error) {
	value = strings.ToLower(strings.TrimSpace(value))

	if value == "" || len(value) > 254 || !emailPattern.MatchString(value) {
		return "", fmt.Errorf("%w: a valid email is required", ErrInvalidInput)
	}

	return value, nil
}

func ValidatePassword(password string) error {
	if len(password) < 12 || len(password) > 128 {
		return fmt.Errorf("%w: password must be 12-128 characters", ErrInvalidInput)
	}

	var hasLetter, hasDigit bool

	for _, char := range password {
		switch {
		case char >= '0' && char <= '9':
			hasDigit = true
		case (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z'):
			hasLetter = true
		}
	}

	if !hasLetter || !hasDigit {
		return fmt.Errorf("%w: password must mix letters and digits", ErrInvalidInput)
	}

	for _, common := range []string{"password", "123456", "qwerty"} {
		if strings.Contains(strings.ToLower(password), common) {
			return fmt.Errorf("%w: password is too common", ErrInvalidInput)
		}
	}

	return nil
}

func ValidateText(field, value string, minRunes, maxRunes int) (string, error) {
	value = strings.TrimSpace(value)

	if !utf8.ValidString(value) {
		return "", fmt.Errorf("%w: %s is not valid UTF-8", ErrInvalidInput, field)
	}

	count := utf8.RuneCountInString(value)

	if count < minRunes || count > maxRunes {
		return "", fmt.Errorf("%w: %s must be %d-%d characters", ErrInvalidInput, field, minRunes, maxRunes)
	}

	for _, char := range value {
		if char < 0x20 && char != '\n' && char != '\t' {
			return "", fmt.Errorf("%w: %s contains a control character", ErrInvalidInput, field)
		}
	}

	return value, nil
}

// SanitizeForLog keeps user input from forging log lines.
func SanitizeForLog(value string) string {
	replacer := strings.NewReplacer("\n", "\\n", "\r", "\\r", "\x1b", "\\x1b")

	cleaned := replacer.Replace(value)

	if len(cleaned) > 120 {
		return cleaned[:120] + "..."
	}

	return cleaned
}

//
// JWT (Day 52)
//

const (
	tokenIssuer     = "onehundredday.day55"
	tokenAudience   = "onehundredday.api"
	AccessTokenTTL  = 15 * time.Minute
	RefreshTokenTTL = 14 * 24 * time.Hour
)

type Claims struct {
	Email string `json:"email"`
	Role  Role   `json:"role"`

	jwt.RegisteredClaims
}

type TokenService struct {
	kid    string
	keys   map[string][]byte
	access time.Duration

	mu      sync.RWMutex
	revoked map[string]time.Time
}

func NewTokenService(accessTTL time.Duration) (*TokenService, error) {
	secret := strings.TrimSpace(os.Getenv("JWT_SIGNING_KEY"))

	if secret == "" {
		if strings.EqualFold(os.Getenv("ENV"), "production") {
			return nil, errors.New("JWT_SIGNING_KEY is required when ENV=production")
		}

		generated := make([]byte, 32)

		if _, err := rand.Read(generated); err != nil {
			return nil, fmt.Errorf("generate development key: %w", err)
		}

		secret = base64.RawURLEncoding.EncodeToString(generated)
	}

	if len(secret) < 32 {
		return nil, errors.New("JWT_SIGNING_KEY must be at least 32 characters")
	}

	kid := strings.TrimSpace(os.Getenv("JWT_SIGNING_KEY_ID"))
	if kid == "" {
		kid = "v1"
	}

	return &TokenService{
		kid:     kid,
		keys:    map[string][]byte{kid: []byte(secret)},
		access:  accessTTL,
		revoked: make(map[string]time.Time),
	}, nil
}

func (s *TokenService) Issue(user User) (string, Claims, error) {
	now := time.Now().UTC()

	jti, err := RandomToken()
	if err != nil {
		return "", Claims{}, err
	}

	claims := Claims{
		Email: user.Email,
		Role:  user.Role,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    tokenIssuer,
			Subject:   strconv.FormatInt(user.ID, 10),
			Audience:  jwt.ClaimStrings{tokenAudience},
			IssuedAt:  jwt.NewNumericDate(now),
			NotBefore: jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.access)),
			ID:        jti,
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	token.Header["kid"] = s.kid

	signed, err := token.SignedString(s.keys[s.kid])
	if err != nil {
		return "", Claims{}, fmt.Errorf("sign token: %w", err)
	}

	return signed, claims, nil
}

func (s *TokenService) Verify(raw string) (Claims, error) {
	var claims Claims

	_, err := jwt.ParseWithClaims(raw, &claims, func(token *jwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)

		key, known := s.keys[kid]
		if !known {
			return nil, fmt.Errorf("unknown key id %q", kid)
		}

		return key, nil
	},
		// Pinned algorithm, checked issuer and audience, expiry required.
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(tokenIssuer),
		jwt.WithAudience(tokenAudience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(30*time.Second),
	)

	switch {
	case errors.Is(err, jwt.ErrTokenExpired):
		return Claims{}, ErrExpiredToken
	case err != nil:
		return Claims{}, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}

	s.mu.RLock()
	_, revoked := s.revoked[claims.ID]
	s.mu.RUnlock()

	if revoked {
		return Claims{}, fmt.Errorf("%w: revoked", ErrInvalidToken)
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

func RandomToken() (string, error) {
	raw := make([]byte, 32)

	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate token: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(raw), nil
}

//
// RATE LIMITING (Day 54)
//

type Limiter struct {
	rate  rate.Limit
	burst int
	ttl   time.Duration

	mu      sync.Mutex
	buckets map[string]*limiterEntry
}

type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

func NewLimiter(perSecond float64, burst int, ttl time.Duration) *Limiter {
	return &Limiter{
		rate:    rate.Limit(perSecond),
		burst:   burst,
		ttl:     ttl,
		buckets: make(map[string]*limiterEntry),
	}
}

func (l *Limiter) Allow(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()

	entry, found := l.buckets[key]
	if !found {
		entry = &limiterEntry{limiter: rate.NewLimiter(l.rate, l.burst)}
		l.buckets[key] = entry
	}

	entry.lastSeen = time.Now()

	reservation := entry.limiter.Reserve()
	if !reservation.OK() {
		return false, time.Second
	}

	if delay := reservation.Delay(); delay > 0 {
		reservation.Cancel()

		return false, delay
	}

	return true, 0
}

func (l *Limiter) Cleanup() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := time.Now().Add(-l.ttl)
	removed := 0

	for key, entry := range l.buckets {
		if entry.lastSeen.Before(cutoff) {
			delete(l.buckets, key)

			removed++
		}
	}

	return removed
}

func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return host
}
