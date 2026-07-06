package logstore

import (
	"sync"

	"akswitch/internal/utils"
)

// LogStore is a thread-safe, fixed-size log store for API usage logs.
type LogStore struct {
	mu     sync.Mutex
	logs   []utils.LogEntry
	maxLen int
}

// New creates a LogStore with the given max size.
func New(maxLen int) *LogStore {
	return &LogStore{
		logs:   make([]utils.LogEntry, 0, maxLen),
		maxLen: maxLen,
	}
}

// Append adds an entry. The entry's Key is masked immediately before storing.
// If the store is full, the oldest entries are dropped in bulk (O(1) amortized).
func (ls *LogStore) Append(entry utils.LogEntry) {
	entry.Key = utils.MaskKey(entry.Key)
	ls.mu.Lock()
	defer ls.mu.Unlock()
	ls.logs = append(ls.logs, entry)
	if len(ls.logs) > ls.maxLen {
		ls.logs = ls.logs[len(ls.logs)-ls.maxLen:]
	}
}

// Len returns the current number of entries (thread-safe convenience).
func (ls *LogStore) Len() int {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	return len(ls.logs)
}

// Snapshot returns a deep copy of all entries.
func (ls *LogStore) Snapshot() []utils.LogEntry {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	result := make([]utils.LogEntry, len(ls.logs))
	copy(result, ls.logs)
	return result
}

// Clear removes all entries.
func (ls *LogStore) Clear() {
	ls.mu.Lock()
	defer ls.mu.Unlock()
	ls.logs = make([]utils.LogEntry, 0, ls.maxLen)
}

