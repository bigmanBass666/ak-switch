package cmd

import (
	"strings"
	"testing"
)

func TestFormatProviderTable_SingleProvider(t *testing.T) {
	det := map[string]interface{}{
		"alpha": map[string]interface{}{
			"keys":              3,
			"upstream_cb_state": "closed",
		},
	}
	result := formatProviderTable(det)
	if !strings.Contains(result, "PROVIDER") {
		t.Errorf("expected table header 'PROVIDER', got:\n%s", result)
	}
	if !strings.Contains(result, "alpha") {
		t.Errorf("expected provider name 'alpha', got:\n%s", result)
	}
	if !strings.Contains(result, "closed") {
		t.Errorf("expected cb_state 'closed', got:\n%s", result)
	}
}

func TestFormatProviderTable_MultiProvider(t *testing.T) {
	det := map[string]interface{}{
		"alpha": map[string]interface{}{
			"keys":              3,
			"upstream_cb_state": "closed",
		},
		"beta": map[string]interface{}{
			"keys":              5,
			"upstream_cb_state": "open",
		},
	}
	result := formatProviderTable(det)
	if !strings.Contains(result, "alpha") || !strings.Contains(result, "beta") {
		t.Errorf("expected both providers in output, got:\n%s", result)
	}
	// Verify header is before data (table structure)
	lines := strings.Split(strings.TrimSpace(result), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected at least 3 lines (header + 2 providers), got %d", len(lines))
	}
	if !strings.Contains(lines[0], "PROVIDER") {
		t.Errorf("first line should be header, got: %q", lines[0])
	}
}

func TestFormatProviderTable_Empty(t *testing.T) {
	det := map[string]interface{}{}
	result := formatProviderTable(det)
	// Should still produce a header row
	if !strings.Contains(result, "PROVIDER") {
		t.Errorf("expected header even with empty data, got:\n%s", result)
	}
}

func TestFormatProviderTable_NilValues(t *testing.T) {
	det := map[string]interface{}{
		"alpha": map[string]interface{}{
			"keys":              nil,
			"upstream_cb_state": nil,
		},
	}
	result := formatProviderTable(det)
	if !strings.Contains(result, "alpha") {
		t.Errorf("expected provider name, got:\n%s", result)
	}
}