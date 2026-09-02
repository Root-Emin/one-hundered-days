package storage

import (
	"time"

	"example.com/onehundredday/Section-12-Project-Layout-And-Architecture-Standard-layout-clean-architecture-dependency-injection-and-domain-services/Day-57/internal/domain"
)

// SystemClock is the production implementation of the clock port. It is the
// only place in the service that calls time.Now.
type SystemClock struct{}

var _ domain.Clock = SystemClock{}

func (SystemClock) Now() time.Time { return time.Now().UTC() }
