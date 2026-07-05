//go:build unit

package server

import (
	"log/slog"
	"strings"
	"testing"
)

// ── ApplyLogLevel ─────────────────────────────────

func TestApplyLogLevel_ValidLevels(t *testing.T) {
	levels := []struct {
		input string
		want  slog.Level
	}{
		{"debug", slog.LevelDebug},
		{"info", slog.LevelInfo},
		{"warn", slog.LevelWarn},
		{"error", slog.LevelError},
		{"DEBUG", slog.LevelDebug},
		{"Info", slog.LevelInfo},
		{"WARN", slog.LevelWarn},
		{"Error", slog.LevelError},
	}

	for _, tc := range levels {
		t.Run(tc.input, func(t *testing.T) {
			ApplyLogLevel(tc.input)
			// Verify the level is set correctly by checking Enabled()
			if !slog.Default().Enabled(nil, tc.want) {
				t.Errorf("ApplyLogLevel(%q): expected level %v to be enabled", tc.input, tc.want)
			}
		})
	}
}

func TestApplyLogLevel_InvalidLevelFallsBackToInfo(t *testing.T) {
	ApplyLogLevel("verbose")

	if !slog.Default().Enabled(nil, slog.LevelInfo) {
		t.Error("ApplyLogLevel(\"verbose\"): expected Info to be enabled")
	}
	if slog.Default().Enabled(nil, slog.LevelDebug) {
		t.Error("ApplyLogLevel(\"verbose\"): expected Debug NOT to be enabled")
	}
}

func TestApplyLogLevel_EmptyLevelFallsBackToInfo(t *testing.T) {
	ApplyLogLevel("")

	if !slog.Default().Enabled(nil, slog.LevelInfo) {
		t.Error("ApplyLogLevel(\"\"): expected Info to be enabled")
	}
}

// ── MaskSensitiveData ─────────────────────────────

func TestMaskSensitiveData_NormalText(t *testing.T) {
	input := "hello world this is a normal message"
	result := MaskSensitiveData(input, 100)
	if result != input {
		t.Errorf("expected %q, got %q", input, result)
	}
}

func TestMaskSensitiveData_MasksAPIKey(t *testing.T) {
	input := "using key sk-test-key-12345 for auth"
	result := MaskSensitiveData(input, 100)
	if strings.Contains(result, "sk-test-key-12345") {
		t.Errorf("expected key to be masked, got %q", result)
	}
	if !strings.Contains(result, "***") {
		t.Errorf("expected masked placeholder, got %q", result)
	}
}

func TestMaskSensitiveData_MultipleKeys(t *testing.T) {
	input := "first key sk-key-11111 and second sk-key-22222"
	result := MaskSensitiveData(input, 200)
	if strings.Contains(result, "sk-key-11111") || strings.Contains(result, "sk-key-22222") {
		t.Errorf("expected both keys to be masked, got %q", result)
	}
}

func TestMaskSensitiveData_Truncation(t *testing.T) {
	input := "this is a very long string that should be truncated to a much shorter length"
	result := MaskSensitiveData(input, 20)
	if len(result) > 20 {
		t.Errorf("expected max length 20, got %d: %q", len(result), result)
	}
}

func TestMaskSensitiveData_NoSkPrefix(t *testing.T) {
	input := "no api key here just regular text"
	result := MaskSensitiveData(input, 100)
	if result != input {
		t.Errorf("expected unchanged %q, got %q", input, result)
	}
}

func TestMaskSensitiveData_KeyAtStart(t *testing.T) {
	input := "sk-test-key-12345 is the key"
	result := MaskSensitiveData(input, 100)
	if strings.Contains(result, "sk-test-key") {
		t.Errorf("expected key at start to be masked, got %q", result)
	}
}

func TestMaskSensitiveData_KeyAtEnd(t *testing.T) {
	input := "the key is sk-test-key-12345"
	result := MaskSensitiveData(input, 100)
	if strings.Contains(result, "sk-test-key-12345") {
		t.Errorf("expected key at end to be masked, got %q", result)
	}
}
