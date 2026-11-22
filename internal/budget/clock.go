package budget

import "time"

// Clock makes time injectable. Production code uses SystemClock; tests use
// FixedClock so results never depend on the current date.
type Clock interface {
	Now() time.Time
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now() }

// SystemClock returns a Clock backed by time.Now.
func SystemClock() Clock { return systemClock{} }

// FixedClock always returns T. It is intended for tests and deterministic runs.
type FixedClock struct {
	T time.Time
}

// Now implements Clock.
func (f FixedClock) Now() time.Time { return f.T }
