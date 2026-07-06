package circuitbreaker

import (
	"math"
	"math/rand"
	"sync"
	"time"
)

// KeyState represents the state of a KeyCircuitBreaker.
type KeyState int

const (
	StateClosed    KeyState = iota // Key is available
	StateOpen                      // Key is cooling (backoff)
	StatePermanent                 // Key is permanently disabled
)

// KeyCircuitBreaker tracks per-key retry state with exponential backoff.
type KeyCircuitBreaker struct {
	mu            sync.Mutex
	state         KeyState
	attempt       int
	cooldownUntil time.Time
	trippedReason string

	base       time.Duration
	multiplier float64
	backoffCap time.Duration
	jitterFn   func(int) time.Duration // override for testing; nil = real random

	authFailCount   int // consecutive auth failures (401/403)
	permaThreshold  int // auth failures before permanent disable (0 = disable immediately)
}

// NewKeyCircuitBreaker creates a new KeyCircuitBreaker.
// permaThreshold is the number of consecutive auth failures before permanent disable.
// Use 0 to disable immediately (legacy behavior).
func NewKeyCircuitBreaker(base, backoffCap time.Duration, multiplier float64, permaThreshold ...int) *KeyCircuitBreaker {
	threshold := 0
	if len(permaThreshold) > 0 && permaThreshold[0] > 0 {
		threshold = permaThreshold[0]
	}
	return &KeyCircuitBreaker{
		base:            base,
		backoffCap:      backoffCap,
		multiplier:      multiplier,
		permaThreshold:  threshold,
	}
}

// RecordFailure records a 429 response and applies exponential backoff.
// Once the computed backoff reaches the cap, the key enters a long cooldown
// (equal to the cap duration) rather than permanent disable.
// Returns the cooldown duration that was applied.
func (k *KeyCircuitBreaker) RecordFailure() time.Duration {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.state == StatePermanent {
		return 0
	}

	// Calculate raw cooldown before capping
	raw := k.base * time.Duration(math.Pow(k.multiplier, float64(k.attempt)))

	// If raw cooldown reaches or exceeds backoffCap, use a long cooldown
	// at the cap duration instead of permanent disable.
	if raw >= k.backoffCap {
		k.state = StateOpen
		k.cooldownUntil = time.Now().Add(k.backoffCap)
		k.attempt = 0
		return k.backoffCap
	}

	// Add jitter
	var jitter time.Duration
	if k.jitterFn != nil {
		jitter = k.jitterFn(k.attempt)
	} else {
		jitter = defaultJitter(raw)
	}

	cooldown := raw + jitter
	if cooldown > k.backoffCap {
		cooldown = k.backoffCap
	}

	k.state = StateOpen
	k.cooldownUntil = time.Now().Add(cooldown)
	k.attempt++
	return cooldown
}

// RecordAuthFailure records an auth failure (401/403) and returns true if the key
// should be permanently disabled (permaThreshold reached or zero = disable immediately).
// The caller should call RecordPerma() when this returns true.
func (k *KeyCircuitBreaker) RecordAuthFailure() bool {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.state == StatePermanent {
		return false
	}
	k.authFailCount++
	if k.permaThreshold <= 0 || k.authFailCount >= k.permaThreshold {
		return true
	}
	// Not yet at threshold; apply a brief cooldown so the key is skipped for a bit
	k.state = StateOpen
	k.cooldownUntil = time.Now().Add(10 * time.Second)
	return false
}

// RecordPerma marks the key as permanently disabled (e.g., 401/403).
func (k *KeyCircuitBreaker) RecordPerma(reason string) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.state = StatePermanent
	k.trippedReason = reason
}

// RecordSuccess resets the breaker to closed state and zeroes the attempt count.
// No-op if the key is permanently disabled.
func (k *KeyCircuitBreaker) RecordSuccess() {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.state == StatePermanent {
		return
	}
	k.state = StateClosed
	k.attempt = 0
	k.authFailCount = 0
	k.cooldownUntil = time.Time{}
}

// Reset fully resets the breaker to closed state, clearing any cooldown
// or permanent failure. Unlike RecordSuccess, this also works on keys
// in the StatePermanent state.
func (k *KeyCircuitBreaker) Reset() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.state = StateClosed
	k.attempt = 0
	k.authFailCount = 0
	k.cooldownUntil = time.Time{}
	k.trippedReason = ""
}

// Allow returns true if the key is available for use (closed, or open with expired cooldown).
func (k *KeyCircuitBreaker) Allow() bool {
	k.mu.Lock()
	defer k.mu.Unlock()

	switch k.state {
	case StatePermanent:
		return false
	case StateClosed:
		return true
	case StateOpen:
		return time.Now().After(k.cooldownUntil)
	default:
		return false
	}
}

// State returns the current state.
func (k *KeyCircuitBreaker) State() KeyState {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.state
}

// Attempt returns the current attempt count.
func (k *KeyCircuitBreaker) Attempt() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.attempt
}

// TrippedReason returns the reason the key was permanently disabled.
func (k *KeyCircuitBreaker) TrippedReason() string {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.trippedReason
}

// AuthFailCount returns the consecutive auth failure count.
func (k *KeyCircuitBreaker) AuthFailCount() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.authFailCount
}

// CooldownRemaining returns the remaining cooldown duration.
// Returns 0 if closed (not cooling), -1 if permanently disabled.
func (k *KeyCircuitBreaker) CooldownRemaining() time.Duration {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.state == StatePermanent {
		return -1
	}
	if k.state == StateClosed {
		return 0
	}

	remaining := k.cooldownUntil.Sub(time.Now())
	if remaining < 0 {
		return 0
	}
	return remaining
}

// defaultJitter returns a random jitter between 0 and 50% of the raw cooldown.
func defaultJitter(raw time.Duration) time.Duration {
	if raw <= 0 {
		return 0
	}
	maxJitter := int64(raw / 2)
	if maxJitter <= 0 {
		return 0
	}
	return time.Duration(rand.Int63n(maxJitter))
}
