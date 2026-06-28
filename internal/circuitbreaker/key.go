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
}

// NewKeyCircuitBreaker creates a new KeyCircuitBreaker.
func NewKeyCircuitBreaker(base, backoffCap time.Duration, multiplier float64) *KeyCircuitBreaker {
	return &KeyCircuitBreaker{
		base:       base,
		backoffCap: backoffCap,
		multiplier: multiplier,
	}
}

// RecordFailure records a 429 response and applies exponential backoff.
// If the computed backoff reaches the cap, the key is permanently disabled
// (considered quota exhausted).
func (k *KeyCircuitBreaker) RecordFailure() {
	k.mu.Lock()
	defer k.mu.Unlock()

	if k.state == StatePermanent {
		return
	}

	// Calculate raw cooldown before capping
	raw := k.base * time.Duration(math.Pow(k.multiplier, float64(k.attempt)))

	// If raw cooldown reaches or exceeds backoffCap, mark as permanent.
	if raw >= k.backoffCap {
		k.state = StatePermanent
		k.trippedReason = "quota_exhausted"
		return
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
	k.cooldownUntil = time.Time{}
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
