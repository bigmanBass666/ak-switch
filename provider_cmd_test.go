package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"alvus/internal/cmd"
	"alvus/internal/config"
)

// ── Provider CRUD Acceptance Tests ──────────────────────

// TestProviderAdd_CreatesProviderEntry verifies that
// "alvus provider add <name> --target <url> --port <port>
// creates a valid entry in the config.toml.
func TestProviderAdd_CreatesProviderEntry(t *testing.T) {
	resetConfigEnv()
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	// provider add auto-creates config.toml when it doesn't exist
	xdgPath, err := config.XDGConfigPath()
	if err != nil {
		t.Fatalf("XDGConfigPath failed: %v", err)
	}

	// Add a provider
	addArgs := []string{"alvus", "provider", "add", "test-provider",
		"--target", "https://test.api.com/v1",
		"--port", "9999",
		"--genai", "https://test.api.com",
		"--cooldown-sec", "30",
		"--max-retries", "5",
	}
	runCommand(t, addArgs...)

	// Verify the config file now contains the provider
	tc, err := config.LoadTomlConfig(xdgPath)
	if err != nil {
		t.Fatalf("LoadTomlConfig failed after add: %v", err)
	}
	p, ok := tc.Provider["test-provider"]
	if !ok {
		t.Fatal("provider 'test-provider' not found in config after add")
	}
	if p.Target != "https://test.api.com/v1" {
		t.Errorf("Target = %q, want %q", p.Target, "https://test.api.com/v1")
	}
	if tc.Port != 9999 {
		t.Errorf("Port = %d, want 9999", tc.Port)
	}
	if p.Genai != "https://test.api.com" {
		t.Errorf("Genai = %q, want %q", p.Genai, "https://test.api.com")
	}
	if p.CooldownSec != 30 {
		t.Errorf("CooldownSec = %d, want 30", p.CooldownSec)
	}
	if p.MaxRetries != 5 {
		t.Errorf("MaxRetries = %d, want 5", p.MaxRetries)
	}
}

// TestProviderAdd_DuplicateRejected verifies that adding
// a provider with a duplicate name is rejected.
func TestProviderAdd_DuplicateRejected(t *testing.T) {
	resetConfigEnv()
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	xdgPath, err := config.XDGConfigPath()
	if err != nil {
		t.Fatalf("XDGConfigPath failed: %v", err)
	}
	runCommand(t, "alvus", "config", "init", "-p", xdgPath)

	// First add succeeds
	runCommand(t, "alvus", "provider", "add", "dup-test",
		"--target", "https://test1.com/v1",
		"--port", "9101",
	)

	// Second add should fail
	err = runCommand(t, "alvus", "provider", "add", "dup-test",
		"--target", "https://test2.com/v1",
		"--port", "9102",
	)
	if err == nil {
		t.Fatal("expected error for duplicate provider add, got nil")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error message = %q, want it to contain 'already exists'", err.Error())
	}
}

// TestProviderList_ShowsProviders verifies that
// "alvus provider list" correctly lists configured providers.
func TestProviderList_ShowsProviders(t *testing.T) {
	resetConfigEnv()
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	xdgPath, err := config.XDGConfigPath()
	if err != nil {
		t.Fatalf("XDGConfigPath failed: %v", err)
	}
	runCommand(t, "alvus", "config", "init", "-p", xdgPath)

	// Add two providers
	runCommand(t, "alvus", "provider", "add", "alpha",
		"--target", "https://alpha.test/v1",
		"--port", "9101",
	)
	runCommand(t, "alvus", "provider", "add", "beta",
		"--target", "https://beta.test/v1",
		"--port", "9102",
	)

	// Capture list output
	var stdout bytes.Buffer
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err = runCommand(t, "alvus", "provider", "list")

	w.Close()
	os.Stdout = oldStdout
	io.Copy(&stdout, r)

	if err != nil {
		t.Fatalf("provider list failed: %v", err)
	}

	output := stdout.String()
	if !strings.Contains(output, "alpha") {
		t.Errorf("output missing 'alpha':\n%s", output)
	}
	if !strings.Contains(output, "beta") {
		t.Errorf("output missing 'beta':\n%s", output)
	}
	if !strings.Contains(output, "https://alpha.test/v1") {
		t.Errorf("output missing alpha target:\n%s", output)
	}
}

// TestProviderRemove_RemovesEntry verifies that
// "alvus provider remove <name>" correctly removes a provider.
func TestProviderRemove_RemovesEntry(t *testing.T) {
	resetConfigEnv()
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	xdgPath, err := config.XDGConfigPath()
	if err != nil {
		t.Fatalf("XDGConfigPath failed: %v", err)
	}
	runCommand(t, "alvus", "config", "init", "-p", xdgPath)
	runCommand(t, "alvus", "provider", "add", "remove-me",
		"--target", "https://remove.test/v1",
		"--port", "9201",
	)

	// Remove it
	runCommand(t, "alvus", "provider", "remove", "remove-me")

	// Verify it's gone
	tc, err := config.LoadTomlConfig(xdgPath)
	if err != nil {
		t.Fatalf("LoadTomlConfig failed: %v", err)
	}
	if _, exists := tc.Provider["remove-me"]; exists {
		t.Error("provider 'remove-me' still exists after remove")
	}
}

// TestProviderRemove_NotFound verifies that removing a
// nonexistent provider returns an error.
func TestProviderRemove_NotFound(t *testing.T) {
	resetConfigEnv()
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	xdgPath, err := config.XDGConfigPath()
	if err != nil {
		t.Fatalf("XDGConfigPath failed: %v", err)
	}
	runCommand(t, "alvus", "config", "init", "-p", xdgPath)

	err = runCommand(t, "alvus", "provider", "remove", "nonexistent")
	if err == nil {
		t.Fatal("expected error for removing nonexistent provider, got nil")
	}
}

// ── Helper ────────────────────────────────────────────

// runCommand executes alvus with the given arguments via cmd.Execute.
// It captures stderr but not stdout (use capture in the test if needed).
func runCommand(t testing.TB, args ...string) error {
	t.Helper()

	oldArgs := os.Args
	oldEnv := os.Environ()
	t.Cleanup(func() {
		os.Args = oldArgs
		// Restore env
		for _, kv := range oldEnv {
			parts := strings.SplitN(kv, "=", 2)
			if len(parts) == 2 {
				os.Setenv(parts[0], parts[1])
			}
		}
	})

	os.Args = args
	return cmd.Execute("")
}