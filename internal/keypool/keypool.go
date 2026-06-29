package keypool

import (
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"alvus/internal/utils"
)

// KeyPool is a thread-safe, round-robin key pool with cooldown, disable, and request-tracking support.
type KeyPool struct {
	counter        uint64
	keys           []string
	names          []string
	cooldowns      []time.Time
	disabled       []bool
	requestHistory [][]time.Time // timestamps of requests in the last 60s per key
	lastUsed       []time.Time
	mu             sync.Mutex
}

// NewKeyPool creates a KeyPool from slices of API keys and optional names.
// names may be nil or shorter than keys — unnamed keys get empty string.
func NewKeyPool(keys []string, names []string) *KeyPool {
	n := make([]string, len(keys))
	for i := range keys {
		if i < len(names) {
			n[i] = names[i]
		}
	}
	return &KeyPool{
		keys:           keys,
		names:          n,
		cooldowns:      make([]time.Time, len(keys)),
		disabled:       make([]bool, len(keys)),
		requestHistory: make([][]time.Time, len(keys)),
		lastUsed:       make([]time.Time, len(keys)),
	}
}

// Keys returns a copy of all keys in the pool.
func (p *KeyPool) Keys() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	result := make([]string, len(p.keys))
	copy(result, p.keys)
	return result
}

// Name returns the name of a key by index, or empty string if index is out of range.
func (p *KeyPool) Name(idx int) string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx < 0 || idx >= len(p.names) {
		return ""
	}
	return p.names[idx]
}

// TimeUntilAvailable returns the shortest duration until any key becomes available,
// or -1 if all keys are disabled. Returns 0 if at least one key is ready.
func (p *KeyPool) TimeUntilAvailable() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	var soonest time.Duration = -1
	for i, cd := range p.cooldowns {
		if p.disabled[i] {
			continue
		}
		if now.After(cd) {
			return 0
		}
		if wait := cd.Sub(now); soonest < 0 || wait < soonest {
			soonest = wait
		}
	}
	return soonest
}

// Next returns the next available key in round-robin order. Returns index, key, and ok=false if none available.
func (p *KeyPool) Next() (int, string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := len(p.keys)
	if n == 0 {
		return -1, "", false
	}
	start := int(atomic.AddUint64(&p.counter, 1)-1) % n
	for i := 0; i < n; i++ {
		idx := (start + i) % n
		if !p.disabled[idx] && time.Now().After(p.cooldowns[idx]) {
			return idx, p.keys[idx], true
		}
	}
	return -1, "", false
}

// RequestsInLastMinute returns the number of requests made by a key in the last 60 seconds.
func (p *KeyPool) RequestsInLastMinute(idx int) int {
	cutoff := time.Now().Add(-60 * time.Second)
	count := 0
	for _, t := range p.requestHistory[idx] {
		if t.After(cutoff) {
			count++
		}
	}
	return count
}

// CleanupOldRequests removes request timestamps older than 60 seconds.
func (p *KeyPool) CleanupOldRequests(idx int) {
	cutoff := time.Now().Add(-60 * time.Second)
	var filtered []time.Time
	for _, t := range p.requestHistory[idx] {
		if t.After(cutoff) {
			filtered = append(filtered, t)
		}
	}
	p.requestHistory[idx] = filtered
}

// Cooldown sets a cooldown on a key for the given duration.
// Returns an error if the index is out of range.
func (p *KeyPool) Cooldown(idx int, d time.Duration) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx < 0 || idx >= len(p.keys) {
		return fmt.Errorf("key index %d out of range (0-%d)", idx, len(p.keys)-1)
	}
	if until := time.Now().Add(d); p.cooldowns[idx].Before(until) {
		p.cooldowns[idx] = until
	}
	name := ""
	if idx >= 0 && idx < len(p.names) {
		name = p.names[idx]
	}
	if name != "" {
		slog.Info("key on cooldown", "key_index", idx, "key_name", name, "duration", d)
	} else {
		slog.Info("key on cooldown", "key_index", idx, "duration", d)
	}
	return nil
}

// Disable marks a key as permanently disabled.
// Returns an error if the index is out of range.
func (p *KeyPool) Disable(idx int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx < 0 || idx >= len(p.keys) {
		return fmt.Errorf("key index %d out of range (0-%d)", idx, len(p.keys)-1)
	}
	p.disabled[idx] = true
	name := ""
	if idx >= 0 && idx < len(p.names) {
		name = p.names[idx]
	}
	if name != "" {
		slog.Info("key disabled", "key_index", idx, "key_name", name)
	}
	return nil
}

// IsDisabled returns whether a key is disabled by index.
// Returns false if the index is out of range.
func (p *KeyPool) IsDisabled(idx int) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx < 0 || idx >= len(p.disabled) {
		return false
	}
	return p.disabled[idx]
}

// ActiveCount returns the number of non-disabled keys.
func (p *KeyPool) ActiveCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for i := range p.keys {
		if !p.disabled[i] {
			n++
		}
	}
	return n
}

// DisabledCount returns the number of disabled keys.
func (p *KeyPool) DisabledCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, d := range p.disabled {
		if d {
			n++
		}
	}
	return n
}

// CoolingCount returns the number of keys currently in cooldown (not disabled, but cooldown not yet expired).
func (p *KeyPool) CoolingCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	n := 0
	for i := range p.keys {
		if !p.disabled[i] && now.Before(p.cooldowns[i]) {
			n++
		}
	}
	return n
}

// KeyStatusLabel returns a status string for a key (disabled, ready, or cooling).
func (p *KeyPool) KeyStatusLabel(i int, now time.Time) string {
	cd := p.cooldowns[i]
	switch {
	case p.disabled[i]:
		return "disabled"
	case now.After(cd):
		return "ready"
	default:
		return fmt.Sprintf("cooling(%.0fs)", cd.Sub(now).Seconds())
	}
}

// Status returns a human-readable status string for all keys.
func (p *KeyPool) Status() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	parts := make([]string, len(p.keys))
	for i := range p.keys {
		parts[i] = fmt.Sprintf("[%d]:%s", i, p.KeyStatusLabel(i, now))
	}
	return strings.Join(parts, " ")
}

// GetKeyDetails returns detailed status for each key in the pool.
func (p *KeyPool) GetKeyDetails() []map[string]interface{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	details := make([]map[string]interface{}, len(p.keys))
	for i := range p.keys {
		p.CleanupOldRequests(i)
		name := ""
		if i < len(p.names) {
			name = p.names[i]
		}
		keyDetail := map[string]interface{}{
			"index":               i,
			"key":                 utils.MaskKey(p.keys[i]),
			"name":                name,
			"disabled":            p.disabled[i],
			"requests_per_minute": p.RequestsInLastMinute(i),
			"last_used":           p.lastUsed[i].Format(time.RFC3339),
			"cooldown_until":      p.cooldowns[i].Format(time.RFC3339),
		}
		keyDetail["status"] = p.KeyStatusLabel(i, now)
		details[i] = keyDetail
	}
	return details
}

// IncrementRequestCount records a request timestamp for a key and updates its lastUsed timestamp.
func (p *KeyPool) IncrementRequestCount(idx int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.CleanupOldRequests(idx)
	p.requestHistory[idx] = append(p.requestHistory[idx], time.Now())
	p.lastUsed[idx] = time.Now()
}

// AddKey appends a new key to the pool and returns its index.
func (p *KeyPool) AddKey(key string, name string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.keys = append(p.keys, key)
	p.names = append(p.names, name)
	p.cooldowns = append(p.cooldowns, time.Time{})
	p.disabled = append(p.disabled, false)
	p.requestHistory = append(p.requestHistory, []time.Time{})
	p.lastUsed = append(p.lastUsed, time.Time{})
	idx := len(p.keys) - 1
	if name != "" {
		slog.Info("key added to pool", "key_index", idx, "key_name", name)
	} else {
		slog.Info("key added to pool", "key_index", idx)
	}
	return idx
}

// RemoveKey removes a key from the pool by index.
func (p *KeyPool) RemoveKey(idx int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx < 0 || idx >= len(p.keys) {
		return fmt.Errorf("key index %d out of range (0-%d)", idx, len(p.keys)-1)
	}
	name := ""
	if idx < len(p.names) {
		name = p.names[idx]
	}
	p.keys = append(p.keys[:idx], p.keys[idx+1:]...)
	p.names = append(p.names[:idx], p.names[idx+1:]...)
	p.cooldowns = append(p.cooldowns[:idx], p.cooldowns[idx+1:]...)
	p.disabled = append(p.disabled[:idx], p.disabled[idx+1:]...)
	p.requestHistory = append(p.requestHistory[:idx], p.requestHistory[idx+1:]...)
	p.lastUsed = append(p.lastUsed[:idx], p.lastUsed[idx+1:]...)
	if name != "" {
		slog.Info("key removed from pool", "key_index", idx, "key_name", name)
	} else {
		slog.Info("key removed from pool", "key_index", idx)
	}
	return nil
}
