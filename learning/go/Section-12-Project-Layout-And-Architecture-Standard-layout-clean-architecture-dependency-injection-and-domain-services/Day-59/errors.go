package main

import (
	"errors"
	"fmt"
	"strings"
)

/*
Typed domain errors.

A service that returns errors.New("bad input") forces its caller to match on
strings. These types carry structure instead: which field failed, which
resource was missing, which rule was violated - so the transport layer can map
them to a status code and a useful body without parsing prose.

Each type implements Is(), so callers can use errors.Is with a sentinel and
errors.As when they need the detail.
*/

var (
	// Sentinels for errors.Is. The concrete types below report themselves as
	// these, so a caller can ask the coarse question without a type switch.
	ErrValidation = errors.New("validation failed")
	ErrNotFound   = errors.New("not found")
	ErrConflict   = errors.New("conflict")
	ErrForbidden  = errors.New("forbidden")
)

//
// VALIDATION
//

// FieldError names the field and the rule it broke. Clients can highlight the
// right input box instead of showing one generic banner.
type FieldError struct {
	Field   string
	Rule    string
	Message string
}

func (e FieldError) Error() string {
	return fmt.Sprintf("%s: %s", e.Field, e.Message)
}

func (e FieldError) Is(target error) bool { return target == ErrValidation }

func invalid(field, rule, format string, args ...any) FieldError {
	return FieldError{Field: field, Rule: rule, Message: fmt.Sprintf(format, args...)}
}

// ValidationError collects several FieldErrors, because a form with three
// mistakes should report three, not the first one and then two more round
// trips.
type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	messages := make([]string, 0, len(e.Fields))

	for _, field := range e.Fields {
		messages = append(messages, field.Error())
	}

	return "validation failed: " + strings.Join(messages, "; ")
}

func (e *ValidationError) Is(target error) bool { return target == ErrValidation }

func (e *ValidationError) Add(err error) {
	if err == nil {
		return
	}

	var fieldError FieldError

	if errors.As(err, &fieldError) {
		e.Fields = append(e.Fields, fieldError)

		return
	}

	e.Fields = append(e.Fields, FieldError{Field: "_", Rule: "invalid", Message: err.Error()})
}

func (e *ValidationError) OrNil() error {
	if len(e.Fields) == 0 {
		return nil
	}

	return e
}

//
// NOT FOUND
//

type NotFoundError struct {
	Resource string
	ID       string
}

func (e NotFoundError) Error() string {
	return fmt.Sprintf("%s %s: not found", e.Resource, e.ID)
}

func (e NotFoundError) Is(target error) bool { return target == ErrNotFound }

//
// CONFLICT / STATE
//

// StateError is the "you cannot do that right now" error: the request is well
// formed and the caller is allowed, but the entity is in the wrong state.
type StateError struct {
	Entity  string
	State   string
	Action  string
	Because string
}

func (e StateError) Error() string {
	message := fmt.Sprintf("cannot %s a %s %s", e.Action, e.State, e.Entity)

	if e.Because != "" {
		message += ": " + e.Because
	}

	return message
}

func (e StateError) Is(target error) bool { return target == ErrConflict }

type DuplicateError struct {
	Resource string
	Key      string
}

func (e DuplicateError) Error() string {
	return fmt.Sprintf("%s %s already exists", e.Resource, e.Key)
}

func (e DuplicateError) Is(target error) bool { return target == ErrConflict }
