//go:build unit

package cmd

import (
	"os"
	"path/filepath"
	"testing"

	"akswitch/internal/config"
)

func TestDetectServerPort_Default(t *testing.T) {
	tmpDir := t.TempDir()
	old := config.ConfigDir
	config.ConfigDir = tmpDir
	t.Cleanup(func() { config.ConfigDir = old })

	port := detectServerPort()
	if port != adminPort {
		t.Errorf("detectServerPort() = %d, want %d (adminPort)", port, adminPort)
	}
}

func TestDetectServerPort_FromConfigFile(t *testing.T) {
	tmpDir := t.TempDir()

	tomlPath := filepath.Join(tmpDir, "config.toml")
	content := `port = 9090

[provider]
[provider.test]
target = "http://example.com"
genai = "http://example.com"
`
	if err := os.WriteFile(tomlPath, []byte(content), 0644); err != nil {
		t.Fatalf("failed to write config.toml: %v", err)
	}

	old := config.ConfigDir
	config.ConfigDir = tmpDir
	t.Cleanup(func() { config.ConfigDir = old })

	port := detectServerPort()
	if port != 9090 {
		t.Errorf("detectServerPort() = %d, want 9090", port)
	}
}
