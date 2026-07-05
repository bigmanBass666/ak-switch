//go:build integration

package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"akswitch/internal/cmd"
	"akswitch/internal/config"
)

// TestConfigInit_CreatesFile verifies that "akswitch config init -p <path>"
// creates a valid TOML config file at the specified path.
func TestConfigInit_CreatesFile(t *testing.T) {
	cmd.ResetConfigEnv()
	tmpDir := t.TempDir()
	configPath := tmpDir + "/config.toml"

	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"akswitch", "config", "init", "-p", configPath}

	err := cmd.Execute("")
	if err != nil {
		t.Fatalf("config init failed: %v", err)
	}

	// Verify file was created
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Fatal("config.toml was not created")
	}

	// Verify file is loadable as valid TOML config
	cfg, err := config.LoadToml(configPath)
	if err != nil {
		t.Fatalf("created config.toml is not valid: %v", err)
	}
	// Generated config should have example placeholder providers
	if cfg.TargetBase != "https://api.example-a.com/v1" {
		t.Errorf("TargetBase should be set to example-a target, got %q", cfg.TargetBase)
	}
}

// TestConfigView_ShowsConfig verifies that "akswitch config view" prints
// the current configuration from config.toml.
func TestConfigView_ShowsConfig(t *testing.T) {
	cmd.ResetConfigEnv()
	tmpDir := t.TempDir()
	config.ConfigDir = tmpDir
	t.Cleanup(func() { config.ConfigDir = "" })

	xdgPath, err := config.XDGConfigPath()
	if err != nil {
		t.Fatalf("XDGConfigPath failed: %v", err)
	}

	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = []string{"akswitch", "config", "init", "-p", xdgPath}
	if err := cmd.Execute(""); err != nil {
		t.Fatalf("config init failed: %v", err)
	}

	// Capture stdout
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	os.Args = []string{"akswitch", "config", "view"}
	err = cmd.Execute("")
	w.Close()
	os.Stdout = oldStdout

	out, _ := io.ReadAll(r)
	if err != nil {
		t.Fatalf("config view failed: %v", err)
	}

	output := string(out)
	if !strings.Contains(output, "Configuration source:") {
		t.Error("output missing 'Configuration source:'")
	}
	if !strings.Contains(output, "api.example-a.com") {
		t.Errorf("output missing expected URL: %s", output)
	}
}
