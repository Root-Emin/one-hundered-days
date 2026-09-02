// Package greeting is a placeholder for "the code CI builds and tests".
package greeting

import (
	"fmt"
	"strings"
	"time"
)

// Greet returns a greeting appropriate to the hour.
func Greet(name string, at time.Time) string {
	name = strings.TrimSpace(name)

	if name == "" {
		name = "there"
	}

	switch hour := at.Hour(); {
	case hour < 6:
		return fmt.Sprintf("Still up, %s?", name)
	case hour < 12:
		return fmt.Sprintf("Good morning, %s", name)
	case hour < 18:
		return fmt.Sprintf("Good afternoon, %s", name)
	default:
		return fmt.Sprintf("Good evening, %s", name)
	}
}
