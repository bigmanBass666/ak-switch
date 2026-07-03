package circuitbreaker

import (
	"testing"
	"time"
)

func newTestBreaker() *KeyCircuitBreaker {
	return &KeyCircuitBreaker{
		base:       30 * time.Second,
		backoffCap: 120 * time.Second,
		multiplier: 2,
		jitterFn:   func(int) time.Duration { return 0 },
	}
}

func TestKeyCircuitBreaker_InitialState(t *testing.T) {
	cb := NewKeyCircuitBreaker(30*time.Second, 120*time.Second, 2)
	if cb.State() != StateClosed {
		t.Errorf("State() = %d, want %d", cb.State(), StateClosed)
	}
	if !cb.Allow() {
		t.Error("Allow() = false, want true")
	}
	if cb.Attempt() != 0 {
		t.Errorf("Attempt() = %d, want 0", cb.Attempt())
	}
}

func TestKeyCircuitBreaker_RecordFailure(t *testing.T) {
	cb := newTestBreaker()
	cb.RecordFailure()

	if cb.State() != StateOpen {
		t.Errorf("State() = %d, want %d", cb.State(), StateOpen)
	}
	if cb.Allow() {
		t.Error("Allow() = true, want false")
	}
	if cb.Attempt() != 1 {
		t.Errorf("Attempt() = %d, want 1", cb.Attempt())
	}
}

func TestKeyCircuitBreaker_ExponentialBackoff(t *testing.T) {
	cb := newTestBreaker()
	cb.RecordFailure()
	d1 := cb.CooldownRemaining()

	// Ensure a tiny gap so the two measurements differ distinctly.
	time.Sleep(time.Millisecond)

	cb.RecordFailure()
	d2 := cb.CooldownRemaining()

	if cb.Attempt() != 2 {
		t.Errorf("Attempt() = %d, want 2", cb.Attempt())
	}
	if d2 <= d1 {
		t.Errorf("second cooldown (%v) should be longer than first (%v)", d2, d1)
	}
}

func TestKeyCircuitBreaker_ReachesCap(t *testing.T) {
	cb := newTestBreaker()

	// attempt=0 → raw=30s < 120s → OPEN
	cb.RecordFailure()
	// attempt=1 → raw=60s < 120s → OPEN
	cb.RecordFailure()
	// attempt=2 → raw=120s >= 120s → OPEN with long cooldown (not PERMANENT)
	cb.RecordFailure()

	if cb.State() != StateOpen {
		t.Errorf("State() = %d, want %d (StateOpen)", cb.State(), StateOpen)
	}
	if cb.Allow() {
		t.Error("Allow() = true, want false (key should still be cooling)")
	}
	// Cooldown should be approximately backoffCap (120s)
	remaining := cb.CooldownRemaining()
	if remaining <= 0 || remaining > 121*time.Second {
		t.Errorf("CooldownRemaining() = %v, want ~120s (backoffCap)", remaining)
	}
	// Attempt should be reset to 0
	if cb.Attempt() != 0 {
		t.Errorf("Attempt() = %d, want 0 (reset after cap)", cb.Attempt())
	}
}

func TestKeyCircuitBreaker_RecordPerma(t *testing.T) {
	cb := newTestBreaker()
	cb.RecordPerma("auth_rejected")

	if cb.State() != StatePermanent {
		t.Errorf("State() = %d, want %d", cb.State(), StatePermanent)
	}
	if cb.Allow() {
		t.Error("Allow() = true, want false")
	}
	if cb.TrippedReason() != "auth_rejected" {
		t.Errorf("TrippedReason() = %q, want %q", cb.TrippedReason(), "auth_rejected")
	}
}

func TestKeyCircuitBreaker_RecordSuccess(t *testing.T) {
	cb := newTestBreaker()
	cb.RecordFailure() // attempt=1, OPEN
	cb.RecordSuccess()

	if cb.State() != StateClosed {
		t.Errorf("State() = %d, want %d", cb.State(), StateClosed)
	}
	if cb.Attempt() != 0 {
		t.Errorf("Attempt() = %d, want 0", cb.Attempt())
	}
}

func TestKeyCircuitBreaker_ResetBackoff(t *testing.T) {
	cb := newTestBreaker()

	cb.RecordFailure() // attempt=1, cooldown=30s
	cb.RecordSuccess() // attempt=0, CLOSED
	cb.RecordFailure() // restart from base: attempt=1, cooldown=30s

	if cb.Attempt() != 1 {
		t.Errorf("Attempt() = %d, want 1", cb.Attempt())
	}

	// Cooldown should be ~30s (from base), not 60s+.
	d := cb.CooldownRemaining()
	if d <= 0 || d > 31*time.Second {
		t.Errorf("expected ~30s cooldown after reset, got %v", d)
	}
}

func TestKeyCircuitBreaker_PermaSuccessNoop(t *testing.T) {
	cb := newTestBreaker()
	cb.RecordPerma("quota_exhausted")
	cb.RecordSuccess()

	if cb.State() != StatePermanent {
		t.Error("RecordSuccess() should not transition from PERMA")
	}
	if cb.TrippedReason() != "quota_exhausted" {
		t.Errorf("TrippedReason() = %q, want %q", cb.TrippedReason(), "quota_exhausted")
	}
}

func TestKeyCircuitBreaker_CooldownExpired(t *testing.T) {
	cb := &KeyCircuitBreaker{
		base:       5 * time.Millisecond,
		backoffCap: 120 * time.Second,
		multiplier: 2,
		jitterFn:   func(int) time.Duration { return 0 },
	}

	cb.RecordFailure()
	if cb.Allow() {
		t.Error("Allow() = true before cooldown expired")
	}

	// Wait for cooldown to expire.
	time.Sleep(20 * time.Millisecond)

	if !cb.Allow() {
		t.Error("Allow() = false after cooldown expired")
	}

	// State should still be OPEN (Allow does not auto-transition).
	if cb.State() != StateOpen {
		t.Errorf("State() = %d, want %d after expired cooldown", cb.State(), StateOpen)
	}
}

func TestKeyCircuitBreaker_CooldownRemaining(t *testing.T) {
	cb := newTestBreaker()

	// CLOSED → 0
	if got := cb.CooldownRemaining(); got != 0 {
		t.Errorf("CooldownRemaining() for CLOSED = %v, want 0", got)
	}

	// OPEN → positive remaining time
	cb.RecordFailure()
	got := cb.CooldownRemaining()
	if got <= 0 || got > 30*time.Second {
		t.Errorf("CooldownRemaining() for OPEN = %v, want between 0 and 30s", got)
	}

	// PERMA → -1
	cb2 := newTestBreaker()
	cb2.RecordPerma("test")
	if got := cb2.CooldownRemaining(); got != -1 {
		t.Errorf("CooldownRemaining() for PERMA = %v, want -1", got)
	}
}
