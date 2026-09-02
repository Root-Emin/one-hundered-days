package main

import (
	"context"
	"errors"
	"time"
)

/*
The seams.

Every dependency a service cannot compute for itself - the database, the
clock, the id source, outbound mail - is an interface declared here, next to
the code that uses it. Three consequences:

  - a reader sees the whole dependency surface in one file
  - a test can replace any of them with twenty lines of fake
  - swapping Postgres for anything else is a new implementation, not a rewrite

Interfaces stay small on purpose. A five method interface is easy to fake; a
fifty method one is a class in disguise.
*/

var (
	ErrNotFound   = errors.New("user not found")
	ErrDuplicate  = errors.New("email already registered")
	ErrValidation = errors.New("invalid signup")
)

type User struct {
	ID        string
	Email     string
	Name      string
	Plan      string
	CreatedAt time.Time
	TrialEnds time.Time
}

// UserRepository is the storage seam.
type UserRepository interface {
	Create(ctx context.Context, user User) (User, error)
	ByEmail(ctx context.Context, email string) (User, error)
	ByID(ctx context.Context, id string) (User, error)
	Count(ctx context.Context) (int, error)
}

// Clock is the time seam. Without it, "the trial ends in 14 days" can only be
// tested by waiting 14 days.
type Clock interface {
	Now() time.Time
}

// IDGenerator is the randomness seam. Without it, assertions have to be
// written as "the id is not empty" instead of "the id is user_0001".
type IDGenerator interface {
	NewID() string
}

// Mailer is the outbound-effect seam. Without it, a unit test sends real
// email - which is how test suites end up on spam blocklists.
type Mailer interface {
	SendWelcome(ctx context.Context, user User) error
}

// AuditLogger is the observability seam, kept separate so a test can assert
// on what was recorded without parsing log output.
type AuditLogger interface {
	Record(ctx context.Context, action string, user User)
}
