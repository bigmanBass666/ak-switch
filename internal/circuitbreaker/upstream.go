// Package circuitbreaker implements circuit breaker patterns for upstream services and API keys.
package circuitbreaker

import (
	"sync"
	"time"
)

// UpstreamState represents the state of the upstream circuit breaker.
type UpstreamState int

const (
	UpstreamClosed   UpstreamState = iota // Normal, allow requests.
	UpstreamOpen                          // Tripped, fail fast.
	UpstreamHalfOpen                      // Probing, allow one probe request.
)

// UpstreamCircuitBreaker protects the upstream from request flooding when it is unhealthy.
// It tracks consecutive failures and opens the circuit when a threshold is reached.
// After a reset timeout, it allows a single probe request to test recovery.
type UpstreamCircuitBreaker struct {
	mu             sync.Mutex
	state          UpstreamState
	failureCount   int
	threshold      int
	resetTimeout   time.Duration
	openedAt       time.Time
	halfOpenProbed bool
}

// NewUpstreamCircuitBreaker creates a new UpstreamCircuitBreaker with the given failure threshold
// and reset timeout. Initial state is CLOSED.
func NewUpstreamCircuitBreaker(threshold int, resetTimeout time.Duration) *UpstreamCircuitBreaker {
	return &UpstreamCircuitBreaker{
		state:        UpstreamClosed,
		threshold:    threshold,
		resetTimeout: resetTimeout,
	}
}

// RecordFailure records an upstream failure (e.g., 502/503).
//   - In CLOSED state, it increments the consecutive failure counter. If the counter reaches the
//     threshold, the circuit transitions to OPEN and the openedAt timestamp is recorded.
//   - In HALF_OPEN state (failed probe), the circuit returns to OPEN with failureCount reset to 1.
func (u *UpstreamCircuitBreaker) RecordFailure() {
	u.mu.Lock()
	defer u.mu.Unlock()

	switch u.state {
	case UpstreamClosed:
		u.failureCount++
		if u.failureCount >= u.threshold {
			u.state = UpstreamOpen
			u.openedAt = time.Now()
		}
	case UpstreamHalfOpen:
		u.failureCount = 1
		u.state = UpstreamOpen
		u.openedAt = time.Now()
	}
	// In UpstreamOpen state, RecordFailure is a no-op.
}

// RecordSuccess records an upstream success.
// It resets the consecutive failure counter and returns the circuit to CLOSED.
func (u *UpstreamCircuitBreaker) RecordSuccess() {
	u.mu.Lock()
	defer u.mu.Unlock()

	u.failureCount = 0
	u.state = UpstreamClosed
}

// Allow checks whether a request should be allowed to pass through.
//   - CLOSED: always returns true.
//   - OPEN: returns true if the reset timeout has elapsed (transitions to HALF_OPEN and marks the
//     probe as used); otherwise returns false.
//   - HALF_OPEN: returns true for the first probe call, false for subsequent calls until the
//     state transitions again.
func (u *UpstreamCircuitBreaker) Allow() bool {
	u.mu.Lock()
	defer u.mu.Unlock()

	switch u.state {
	case UpstreamClosed:
		return true
	case UpstreamOpen:
		if time.Now().After(u.openedAt.Add(u.resetTimeout)) {
			u.state = UpstreamHalfOpen
			u.halfOpenProbed = true
			return true
		}
		return false
	case UpstreamHalfOpen:
		if !u.halfOpenProbed {
			u.halfOpenProbed = true
			return true
		}
		return false
	default:
		return false
	}
}

// State returns the current circuit breaker state.
func (u *UpstreamCircuitBreaker) State() UpstreamState {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.state
}

// FailureCount returns the current consecutive failure count.
func (u *UpstreamCircuitBreaker) FailureCount() int {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.failureCount
}
