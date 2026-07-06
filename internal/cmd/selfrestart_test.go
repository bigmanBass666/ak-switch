//go:build integration

package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestExecRestart_SpawnsHelper verifies that ExecRestart spawns the configured
// binary with "start" as its argument and cleans up the restart ticker.
func TestExecRestart_SpawnsHelper(t *testing.T) {
	// ── Compile the restart helper binary ────────────────
	helper := filepath.Join(t.TempDir(), "restart-helper.exe")
	out, err := exec.Command("go", "build", "-o", helper, "./testdata/restart-helper/").CombinedOutput()
	if err != nil {
		t.Fatalf("failed to build restart-helper: %v\n%s", err, out)
	}

	// ── Set up marker directory (read by helper) ─────────
	markerDir := t.TempDir()
	t.Setenv("RESTART_HELPER_MARKER_DIR", markerDir)

	// ── Arrange package-level vars ───────────────────────
	restartExePath = helper
	restartTicker = time.NewTicker(5 * time.Second)

	t.Cleanup(func() {
		if restartTicker != nil {
			restartTicker.Stop()
		}
		restartExePath = ""
		restartTicker = nil
		restartSigCh = nil
	})

	// ── Act ──────────────────────────────────────────────
	ExecRestart()

	// ── Wait for helper to write its marker file ─────────
	marker := filepath.Join(markerDir, "started.marker")
	var data []byte
	for i := 0; i < 20; i++ {
		data, err = os.ReadFile(marker)
		if err == nil && len(data) > 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil || len(data) == 0 {
		t.Fatalf("helper did not create marker file within 1s: %v", err)
	}

	// ── Assert helper was called with "start" arg ────────
	markerContent := string(data)
	if !strings.Contains(markerContent, "start") {
		t.Errorf("helper args should contain 'start', got: %s", markerContent)
	}

	// ── Assert ticker was stopped and nil'd ──────────────
	if restartTicker != nil {
		t.Error("restartTicker should be nil after ExecRestart")
	}

	t.Logf("helper marker: %s", markerContent)
}

// TestExecRestart_EmptyPath verifies that ExecRestart is a no-op when
// restartExePath is empty (no crash, no panic).
func TestExecRestart_EmptyPath(t *testing.T) {
	restartExePath = ""
	restartTicker = nil

	ExecRestart()

	if restartTicker != nil {
		t.Error("restartTicker should remain nil")
	}
}
