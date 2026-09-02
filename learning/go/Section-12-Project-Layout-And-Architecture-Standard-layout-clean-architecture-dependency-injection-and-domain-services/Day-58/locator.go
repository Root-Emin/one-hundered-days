package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
)

/*
The anti-pattern, written out so the difference is concrete rather than
theoretical.

A service locator hides dependencies in a global registry and looks them up at
call time. It looks convenient - constructors take no arguments, nothing has
to be threaded through - and it costs:

 1. The dependencies are invisible. Reading LocatorSignup tells you nothing
    about what it touches; you have to read every line of every method.
 2. Tests share mutable global state. Two tests that register different fakes
    interfere, and the failure depends on execution order.
 3. Parallel tests are impossible without locking the whole registry.
 4. A missing registration is a runtime panic in production, not a compile
    error or a startup failure.
 5. Nothing stops a deep helper from reaching for the database. Coupling grows
    invisibly, and the import graph stops telling the truth.

The functions below are here to be compared with service.go and then never
copied. locator_test.go demonstrates failure mode 2.
*/

// registry is the global bag of dependencies. This is the smell.
var (
	registryMu sync.RWMutex
	registry   = map[string]any{}
)

func Register(name string, dependency any) {
	registryMu.Lock()
	defer registryMu.Unlock()

	registry[name] = dependency
}

func Resolve[T any](name string) T {
	registryMu.RLock()
	defer registryMu.RUnlock()

	dependency, found := registry[name]
	if !found {
		// In a real service-locator codebase this is the 3am panic: a
		// dependency nobody registered on this code path.
		panic("service locator: nothing registered for " + name)
	}

	typed, ok := dependency.(T)
	if !ok {
		panic("service locator: wrong type registered for " + name)
	}

	return typed
}

func ResetRegistry() {
	registryMu.Lock()
	defer registryMu.Unlock()

	registry = map[string]any{}
}

// LocatorSignup does the same work as SignupService, the wrong way. Its zero
// value "works", which is exactly the problem: nothing forces the caller to
// supply anything, and nothing tells the reader what it needs.
type LocatorSignup struct{}

func (LocatorSignup) Signup(ctx context.Context, email, name, plan string) (User, error) {
	// Look at how much this hides. Four dependencies, none of them visible in
	// the signature, all of them resolved at the last possible moment.
	users := Resolve[UserRepository]("users")
	clock := Resolve[Clock]("clock")
	ids := Resolve[IDGenerator]("ids")
	mailer := Resolve[Mailer]("mailer")

	email = strings.ToLower(strings.TrimSpace(email))

	if email == "" || !strings.Contains(email, "@") {
		return User{}, errors.New("invalid email")
	}

	if _, err := users.ByEmail(ctx, email); err == nil {
		return User{}, ErrDuplicate
	}

	user := User{
		ID:        ids.NewID(),
		Email:     email,
		Name:      name,
		Plan:      plan,
		CreatedAt: clock.Now(),
	}

	if plan == "pro" {
		user.TrialEnds = user.CreatedAt.Add(14 * 24 * time.Hour)
	}

	created, err := users.Create(ctx, user)
	if err != nil {
		return User{}, err
	}

	if err := mailer.SendWelcome(ctx, created); err != nil {
		return created, nil
	}

	return created, nil
}
