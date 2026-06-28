package logstore

import (
	"alvus/internal/utils"
	"testing"
)

func TestAppendAndSnapshot(t *testing.T) {
	s := New(100)
	entries := []utils.LogEntry{
		{Timestamp: "2025-01-01T00:00:00Z", Key: "sk-real-key-12345", KeyIndex: 1, Method: "POST", URL: "https://example.com/v1/chat", Status: 200, RequestBodySize: 100},
		{Timestamp: "2025-01-01T00:00:01Z", Key: "another-key-67890", KeyIndex: 2, Method: "GET", URL: "https://example.com/v1/models", Status: 200, RequestBodySize: 0},
		{Timestamp: "2025-01-01T00:00:02Z", Key: "test-key-abcde", KeyIndex: 3, Method: "POST", URL: "https://example.com/v1/completions", Status: 400, RequestBodySize: 50},
	}

	for _, e := range entries {
		s.Append(e)
	}

	if s.Len() != 3 {
		t.Errorf("expected Len() = 3, got %d", s.Len())
	}

	snap := s.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("expected Snapshot len = 3, got %d", len(snap))
	}

	// Check all fields match (Key will be masked)
	expectedKeys := []string{
		utils.MaskKey("sk-real-key-12345"),
		utils.MaskKey("another-key-67890"),
		utils.MaskKey("test-key-abcde"),
	}

	for i, got := range snap {
		if got.Key != expectedKeys[i] {
			t.Errorf("entry[%d] Key = %q, want %q", i, got.Key, expectedKeys[i])
		}
		if got.Timestamp != entries[i].Timestamp {
			t.Errorf("entry[%d] Timestamp = %q, want %q", i, got.Timestamp, entries[i].Timestamp)
		}
		if got.KeyIndex != entries[i].KeyIndex {
			t.Errorf("entry[%d] KeyIndex = %d, want %d", i, got.KeyIndex, entries[i].KeyIndex)
		}
		if got.Method != entries[i].Method {
			t.Errorf("entry[%d] Method = %q, want %q", i, got.Method, entries[i].Method)
		}
		if got.URL != entries[i].URL {
			t.Errorf("entry[%d] URL = %q, want %q", i, got.URL, entries[i].URL)
		}
		if got.Status != entries[i].Status {
			t.Errorf("entry[%d] Status = %d, want %d", i, got.Status, entries[i].Status)
		}
		if got.RequestBodySize != entries[i].RequestBodySize {
			t.Errorf("entry[%d] RequestBodySize = %d, want %d", i, got.RequestBodySize, entries[i].RequestBodySize)
		}
	}
}

func TestFIFOLimit(t *testing.T) {
	s := New(3)
	for i := 0; i < 5; i++ {
		s.Append(utils.LogEntry{
			Timestamp:       "entry",
			Key:             "key",
			KeyIndex:        i + 1,
			Method:          "GET",
			URL:             "/",
			Status:          200,
			RequestBodySize: 0,
		})
	}

	if s.Len() != 3 {
		t.Errorf("expected Len() = 3, got %d", s.Len())
	}

	snap := s.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("expected Snapshot len = 3, got %d", len(snap))
	}

	// Should contain only the last 3 entries (indices 2, 3, 4 — 0-indexed)
	for i, entry := range snap {
		expectedIndex := i + 3 // we appended 0-4, so last 3 are 2,3,4 but KeyIndex is 1-based: 3,4,5
		if entry.KeyIndex != expectedIndex {
			t.Errorf("entry[%d] KeyIndex = %d, want %d", i, entry.KeyIndex, expectedIndex)
		}
	}
}

func TestClear(t *testing.T) {
	s := New(10)
	for i := 0; i < 3; i++ {
		s.Append(utils.LogEntry{
			Timestamp:       "entry",
			Key:             "key",
			KeyIndex:        i + 1,
			Method:          "GET",
			URL:             "/",
			Status:          200,
			RequestBodySize: 0,
		})
	}

	if s.Len() != 3 {
		t.Fatalf("expected Len() = 3 before Clear, got %d", s.Len())
	}

	s.Clear()

	if s.Len() != 0 {
		t.Errorf("expected Len() = 0 after Clear, got %d", s.Len())
	}

	snap := s.Snapshot()
	if snap == nil {
		t.Errorf("expected Snapshot to be non-nil empty slice, got nil")
	}
	if len(snap) != 0 {
		t.Errorf("expected Snapshot to be empty, got %d entries", len(snap))
	}
}

func TestCountByStatus(t *testing.T) {
	s := New(100)
	entries := []utils.LogEntry{
		{Timestamp: "t1", Key: "k1", KeyIndex: 1, Method: "GET", URL: "/", Status: 200, RequestBodySize: 0},
		{Timestamp: "t2", Key: "k2", KeyIndex: 2, Method: "GET", URL: "/", Status: 200, RequestBodySize: 0},
		{Timestamp: "t3", Key: "k3", KeyIndex: 3, Method: "POST", URL: "/", Status: 400, RequestBodySize: 50},
		{Timestamp: "t4", Key: "k4", KeyIndex: 4, Method: "POST", URL: "/", Status: 500, RequestBodySize: 100},
		{Timestamp: "t5", Key: "k5", KeyIndex: 5, Method: "GET", URL: "/", Status: 429, RequestBodySize: 0},
	}

	for _, e := range entries {
		s.Append(e)
	}

	// Count 2xx
	twoHundreds := s.CountByStatus(func(status int) bool {
		return status >= 200 && status < 300
	})
	if twoHundreds != 2 {
		t.Errorf("expected 2 entries with 2xx status, got %d", twoHundreds)
	}

	// Count 4xx
	fourHundreds := s.CountByStatus(func(status int) bool {
		return status >= 400 && status < 500
	})
	if fourHundreds != 2 {
		t.Errorf("expected 2 entries with 4xx status, got %d", fourHundreds)
	}

	// Count 5xx
	fiveHundreds := s.CountByStatus(func(status int) bool {
		return status >= 500 && status < 600
	})
	if fiveHundreds != 1 {
		t.Errorf("expected 1 entry with 5xx status, got %d", fiveHundreds)
	}

	// Count all
	all := s.CountByStatus(func(status int) bool { return true })
	if all != 5 {
		t.Errorf("expected 5 entries total, got %d", all)
	}

	// Count none
	none := s.CountByStatus(func(status int) bool { return false })
	if none != 0 {
		t.Errorf("expected 0 entries, got %d", none)
	}
}

func TestKeyMaskedOnAppend(t *testing.T) {
	s := New(10)
	rawKey := "sk-real-key-12345"
	expectedMasked := utils.MaskKey(rawKey)

	s.Append(utils.LogEntry{
		Timestamp:       "2025-01-01T00:00:00Z",
		Key:             rawKey,
		KeyIndex:        1,
		Method:          "POST",
		URL:             "https://example.com/v1/chat",
		Status:          200,
		RequestBodySize: 100,
	})

	snap := s.Snapshot()
	if len(snap) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(snap))
	}

	if snap[0].Key == rawKey {
		t.Errorf("Key was NOT masked — still contains raw key %q", rawKey)
	}

	if snap[0].Key != expectedMasked {
		t.Errorf("Key = %q, want masked value %q", snap[0].Key, expectedMasked)
	}

	// Verify the internal entry is also masked (access via Snapshot only)
	if snap[0].Key == rawKey || snap[0].Key == "sk-real-key-12345" {
		t.Error("Key appears unmasked in Snapshot; expected masking on Append")
	}
}
