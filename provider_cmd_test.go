//go:build integration

package main

import (
	"bytes"
	"io"
	"os"
	"strings"
	"testing"

	"akswitch/internal/cmd"
	"akswitch/internal/config"
)

// ── Provider CRUD Acceptance Tests ──────────────────────

// TestProviderAdd_CreatesProviderEntry verifies that
// "akswitch provider add <name> --target <url> --port <port>
// creates a valid entry in the config.toml.
func TestProviderAdd_CreatesProviderEntry(t *testing.T) {
	cmd.ResetConfigEnv()
	tmpDir := t.TempDir()
	config.ConfigDir = tmpDir
	t.Cleanup(func() { config.ConfigDir = "" })

	// provider add auto-creates config.toml when it doesn't exist
	xdgPath, err := config.XDGConfigPath()
	if err != nil {
		t.Fatalf("XDGConfigPath failed: %v", err)
	}

	// Add a provider
	addArgs := []string{"akswitch", "provider", "add", "test-provider",
		"--target", "https://test.api.com/v1",
		"--port", "9999",
		"--genai", "https://test.api.com",
		"--cooldown-sec", "30",
		"--max-retries", "5",
	}
	cmd.RunCommand(t, addArgs...)

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
	cmd.ResetConfigEnv()
	tmpDir := t.TempDir()
	config.ConfigDir = tmpDir
	t.Cleanup(func() { config.ConfigDir = "" })

	xdgPath, err := config.XDGConfigPath()
	if err != nil {
		t.Fatalf("XDGConfigPath failed: %v", err)
	}
	cmd.RunCommand(t, "akswitch", "config", "init", "-p", xdgPath)

	// First add succeeds
	cmd.RunCommand(t, "akswitch", "provider", "add", "dup-test",
		"--target", "https://test1.com/v1",
		"--port", "9101",
	)

	// Second add should fail
	err = cmd.RunCommand(t, "akswitch", "provider", "add", "dup-test",
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
// "akswitch provider list" correctly lists configured providers.
func TestProviderList_ShowsProviders(t *testing.T) {
	cmd.ResetConfigEnv()
	tmpDir := t.TempDir()
	config.ConfigDir = tmpDir
	t.Cleanup(func() { config.ConfigDir = "" })

	xdgPath, err := config.XDGConfigPath()
	if err != nil {
		t.Fatalf("XDGConfigPath failed: %v", err)
	}
	cmd.RunCommand(t, "akswitch", "config", "init", "-p", xdgPath)

	// Add two providers
	cmd.RunCommand(t, "akswitch", "provider", "add", "alpha",
		"--target", "https://alpha.test/v1",
		"--port", "9101",
	)
	cmd.RunCommand(t, "akswitch", "provider", "add", "beta",
		"--target", "https://beta.test/v1",
		"--port", "9102",
	)

	// Capture list output
	var stdout bytes.Buffer
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	err = cmd.RunCommand(t, "akswitch", "provider", "list")

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
// "akswitch provider remove <name>" correctly removes a provider.
func TestProviderRemove_RemovesEntry(t *testing.T) {
	cmd.ResetConfigEnv()
	tmpDir := t.TempDir()
	config.ConfigDir = tmpDir
	t.Cleanup(func() { config.ConfigDir = "" })

	xdgPath, err := config.XDGConfigPath()
	if err != nil {
		t.Fatalf("XDGConfigPath failed: %v", err)
	}
	cmd.RunCommand(t, "akswitch", "config", "init", "-p", xdgPath)
	cmd.RunCommand(t, "akswitch", "provider", "add", "remove-me",
		"--target", "https://remove.test/v1",
		"--port", "9201",
	)

	// Remove it
	cmd.RunCommand(t, "akswitch", "provider", "remove", "remove-me")

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
	cmd.ResetConfigEnv()
	tmpDir := t.TempDir()
	config.ConfigDir = tmpDir
	t.Cleanup(func() { config.ConfigDir = "" })

	xdgPath, err := config.XDGConfigPath()
	if err != nil {
		t.Fatalf("XDGConfigPath failed: %v", err)
	}
	cmd.RunCommand(t, "akswitch", "config", "init", "-p", xdgPath)

	err = cmd.RunCommand(t, "akswitch", "provider", "remove", "nonexistent")
	if err == nil {
		t.Fatal("expected error for removing nonexistent provider, got nil")
	}
}

// ── Test: provider add --default ─────────────────────────
//
// "akswitch provider add <name> --default" 应将 DefaultProvider 设为该 provider。
func TestProviderAdd_DefaultFlag(t *testing.T) {
	cmd.ResetConfigEnv()
	tmpDir := t.TempDir()
	config.ConfigDir = tmpDir
	t.Cleanup(func() { config.ConfigDir = "" })

	xdgPath, err := config.XDGConfigPath()
	if err != nil {
		t.Fatalf("XDGConfigPath failed: %v", err)
	}

	cmd.RunCommand(t, "akswitch", "provider", "add", "primary",
		"--target", "https://primary.test/v1",
		"--port", "9501",
		"--default",
	)

	tc, err := config.LoadTomlConfig(xdgPath)
	if err != nil {
		t.Fatalf("LoadTomlConfig failed: %v", err)
	}
	if tc.DefaultProvider != "primary" {
		t.Errorf("DefaultProvider = %q, want %q", tc.DefaultProvider, "primary")
	}
}

// ── Test: provider default <name> ─────────────────────────
//
// "akswitch provider default <name>" 应正确设置 default_provider。
func TestProviderDefault_SetsDefault(t *testing.T) {
	cmd.ResetConfigEnv()
	tmpDir := t.TempDir()
	config.ConfigDir = tmpDir
	t.Cleanup(func() { config.ConfigDir = "" })

	xdgPath, err := config.XDGConfigPath()
	if err != nil {
		t.Fatalf("XDGConfigPath failed: %v", err)
	}

	cmd.RunCommand(t, "akswitch", "provider", "add", "alpha",
		"--target", "https://alpha.test/v1",
		"--port", "9501",
	)
	cmd.RunCommand(t, "akswitch", "provider", "add", "beta",
		"--target", "https://beta.test/v1",
	)

	cmd.RunCommand(t, "akswitch", "provider", "default", "beta")

	tc, err := config.LoadTomlConfig(xdgPath)
	if err != nil {
		t.Fatalf("LoadTomlConfig failed: %v", err)
	}
	if tc.DefaultProvider != "beta" {
		t.Errorf("DefaultProvider = %q, want %q", tc.DefaultProvider, "beta")
	}
}

// ── Test: provider default <name> 不存在 ──────────────────
//
// "akswitch provider default <name>" 对不存在的 provider 应报错。
func TestProviderDefault_NotFound(t *testing.T) {
	cmd.ResetConfigEnv()
	tmpDir := t.TempDir()
	config.ConfigDir = tmpDir
	t.Cleanup(func() { config.ConfigDir = "" })

	xdgPath, err := config.XDGConfigPath()
	if err != nil {
		t.Fatalf("XDGConfigPath failed: %v", err)
	}
	cmd.RunCommand(t, "akswitch", "config", "init", "-p", xdgPath)

	err = cmd.RunCommand(t, "akswitch", "provider", "default", "nonexistent")
	if err == nil {
		t.Fatal("expected error for default with nonexistent provider, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error message = %q, want it to contain 'not found'", err.Error())
	}
}
