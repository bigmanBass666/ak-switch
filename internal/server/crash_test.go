package server

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCrashRecover_WritesToCrashLog(t *testing.T) {
	// Use a temp dir as home so crash.log goes to a known location.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome) // Windows fallback

	// Pre-create the crash log directory to avoid MkdirAll issues on Windows
	crashDir := filepath.Join(tmpHome, ".akswitch")
	if err := os.MkdirAll(crashDir, 0755); err != nil {
		t.Fatalf("failed to create test crash dir: %v", err)
	}

	// Trigger a panic and recover
	func() {
		defer CrashRecover("testCrash")
		panic("test panic message")
	}()

	crashPath := filepath.Join(crashDir, "crash.log")
	data, err := os.ReadFile(crashPath)
	if err != nil {
		t.Fatalf("crash.log not found at %s: %v", crashPath, err)
	}

	content := string(data)
	if !containsStr(content, "test panic message") {
		t.Errorf("crash.log missing panic message, got: %s", content)
	}
	if !containsStr(content, "testCrash") {
		t.Errorf("crash.log missing context label, got: %s", content)
	}
	if !containsStr(content, "Stack") {
		t.Errorf("crash.log missing stack trace, got: %s", content)
	}
}

func TestCrashRecover_NoPanic(t *testing.T) {
	// When no panic occurs, CrashRecover returns nil and no crash file is written
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)

	result := func() (r any) {
		defer func() { r = CrashRecover("noPanic") }()
		return nil
	}()

	if result != nil {
		t.Errorf("CrashRecover with no panic returned %v, want nil", result)
	}

	crashPath := filepath.Join(tmpHome, ".akswitch", "crash.log")
	if _, err := os.Stat(crashPath); err == nil {
		t.Error("crash.log created despite no panic")
	}
}

func TestDefaultCrashLogPath(t *testing.T) {
	path := defaultCrashLogPath()
	if !containsStr(path, ".akswitch") {
		t.Errorf("defaultCrashLogPath = %q, should contain .akswitch", path)
	}
	if !containsStr(path, "crash.log") {
		t.Errorf("defaultCrashLogPath = %q, should end with crash.log", path)
	}
}

func TestSetupCrashLogDir(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	t.Setenv("USERPROFILE", tmpHome)

	path := SetupCrashLogDir()
	dir := filepath.Dir(path)
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Errorf("crash log directory not created: %s", dir)
	}
}

// containsStr reports whether s contains substr.
func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && stringContains(s, substr)
}

// stringContains is a simple substring check without using strings.Contains.
func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}