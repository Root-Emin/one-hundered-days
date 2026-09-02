package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/bcrypt"
)

/*
Password handling.

Rules that are not negotiable:

  - a plaintext password is never stored, never logged, never returned
  - hashing is deliberately slow, so a leaked database is expensive to crack
  - every hash carries its own random salt, so identical passwords produce
    different hashes and rainbow tables are useless
  - verification uses a constant-time comparison, so response time does not
    leak how much of the secret was correct

Two implementations are here on purpose: bcrypt is the boring default that
ships with every stack, argon2id is the current recommendation (OWASP) and
shows what "memory-hard" means in practice.
*/

var (
	ErrInvalidCredentials = errors.New("invalid credentials")
	ErrWeakPassword       = errors.New("password does not meet the policy")
)

//
// POLICY
//

const (
	minPasswordLength = 12
	maxPasswordLength = 128 // bcrypt silently truncates past 72 bytes; reject long input instead
)

// ValidatePassword enforces length first: length beats character-class rules
// for real-world strength, and a maximum protects against a hashing
// denial-of-service with a megabyte-long "password".
func ValidatePassword(password string) error {
	if len(password) < minPasswordLength {
		return fmt.Errorf("%w: at least %d characters required", ErrWeakPassword, minPasswordLength)
	}

	if len(password) > maxPasswordLength {
		return fmt.Errorf("%w: at most %d characters allowed", ErrWeakPassword, maxPasswordLength)
	}

	var hasLetter, hasDigit bool

	for _, char := range password {
		switch {
		case unicode.IsLetter(char):
			hasLetter = true
		case unicode.IsDigit(char):
			hasDigit = true
		}
	}

	if !hasLetter || !hasDigit {
		return fmt.Errorf("%w: mix letters and digits", ErrWeakPassword)
	}

	// A tiny denylist stands in for the "check against known breached
	// passwords" step a real service would do (for example the k-anonymity
	// Have I Been Pwned range API).
	for _, common := range []string{"password", "123456", "qwerty", "letmein"} {
		if strings.Contains(strings.ToLower(password), common) {
			return fmt.Errorf("%w: contains a well-known password", ErrWeakPassword)
		}
	}

	return nil
}

//
// HASHER INTERFACE
//

// Hasher lets the algorithm change without touching the login flow. Password
// hashing schemes are replaced every few years; the code that calls them
// should not have to be.
type Hasher interface {
	Hash(password string) (string, error)
	Verify(password, encoded string) error
	Name() string
}

//
// BCRYPT
//

type BcryptHasher struct {
	// Cost is a work factor: each +1 doubles the time. Tune it so hashing
	// takes ~100-250ms on production hardware, then revisit yearly.
	Cost int
}

func NewBcryptHasher() BcryptHasher {
	return BcryptHasher{Cost: bcrypt.DefaultCost}
}

func (h BcryptHasher) Name() string { return "bcrypt" }

func (h BcryptHasher) Hash(password string) (string, error) {
	// bcrypt generates and embeds the salt itself; the output already
	// contains the cost, the salt and the digest.
	hashed, err := bcrypt.GenerateFromPassword([]byte(password), h.Cost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}

	return string(hashed), nil
}

func (h BcryptHasher) Verify(password, encoded string) error {
	// CompareHashAndPassword is constant time with respect to the digest.
	if err := bcrypt.CompareHashAndPassword([]byte(encoded), []byte(password)); err != nil {
		// Never surface which part failed: "no such user" and "wrong
		// password" must be indistinguishable to the caller.
		return ErrInvalidCredentials
	}

	return nil
}

//
// ARGON2ID
//

type Argon2Hasher struct {
	Time    uint32 // iterations
	Memory  uint32 // KiB - the "memory hard" part that defeats GPU cracking
	Threads uint8
	KeyLen  uint32
	SaltLen uint32
}

// NewArgon2Hasher uses the OWASP baseline: 19 MiB, 2 iterations, 1 thread.
func NewArgon2Hasher() Argon2Hasher {
	return Argon2Hasher{
		Time:    2,
		Memory:  19 * 1024,
		Threads: 1,
		KeyLen:  32,
		SaltLen: 16,
	}
}

func (h Argon2Hasher) Name() string { return "argon2id" }

// Hash produces the standard PHC string:
// $argon2id$v=19$m=19456,t=2,p=1$<salt>$<hash>
//
// Encoding the parameters means a future change of cost does not invalidate
// existing hashes: each one is verified with the parameters it was made with.
func (h Argon2Hasher) Hash(password string) (string, error) {
	salt := make([]byte, h.SaltLen)

	// crypto/rand, never math/rand: a predictable salt is no salt.
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	digest := argon2.IDKey([]byte(password), salt, h.Time, h.Memory, h.Threads, h.KeyLen)

	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		h.Memory, h.Time, h.Threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(digest),
	), nil
}

func (h Argon2Hasher) Verify(password, encoded string) error {
	parts := strings.Split(encoded, "$")

	if len(parts) != 6 || parts[1] != "argon2id" {
		return fmt.Errorf("%w: unrecognised hash format", ErrInvalidCredentials)
	}

	var version int

	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return fmt.Errorf("%w: unsupported argon2 version", ErrInvalidCredentials)
	}

	var (
		memory  uint32
		time    uint32
		threads uint8
	)

	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return fmt.Errorf("%w: unreadable argon2 parameters", ErrInvalidCredentials)
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return fmt.Errorf("%w: unreadable salt", ErrInvalidCredentials)
	}

	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return fmt.Errorf("%w: unreadable digest", ErrInvalidCredentials)
	}

	computed := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(expected)))

	// subtle.ConstantTimeCompare does not return early on the first differing
	// byte, so an attacker cannot measure how close a guess was.
	if subtle.ConstantTimeCompare(expected, computed) != 1 {
		return ErrInvalidCredentials
	}

	return nil
}
