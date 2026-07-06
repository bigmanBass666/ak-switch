//go:build unit

package logstore

import (
	"akswitch/internal/utils"
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
