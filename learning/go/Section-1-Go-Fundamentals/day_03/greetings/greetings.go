// Package greetings builds welcome messages and reports simple facts about names.
package greetings

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// ErrEmptyName is returned when a greeting is requested without a name.
var ErrEmptyName = errors.New("greetings: name is empty")

// Hello returns a welcome message for name. It returns ErrEmptyName if name is
// blank. Both results are named, so the success path ends with a naked return.
func Hello(name string) (message string, err error) {
	if strings.TrimSpace(name) == "" {
		return "", ErrEmptyName
	}
	message = fmt.Sprintf("Hi, %v. Welcome!", name)
	return
}

// Count reports how many letters and words name contains. Spaces are ignored
// when counting letters.
func Count(name string) (letters, words int) {
	words = len(strings.Fields(name))
	letters = utf8.RuneCountInString(strings.ReplaceAll(name, " ", ""))
	return
}
