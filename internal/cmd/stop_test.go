//go:build unit

package cmd

import (
	"os"
	"testing"
)

func TestProcessRunning_CurrentProcess(t *testing.T) {
	if !processRunning(os.Getpid()) {
		t.Errorf("processRunning should return true for current process (PID %d)", os.Getpid())
	}
}

func TestProcessRunning_NonExistentPID(t *testing.T) {
	// Use a PID that is extremely unlikely to exist on any system
	if processRunning(999999999) {
		t.Errorf("processRunning should return false for non-existent PID")
	}
}

func TestProcessRunning_PIDZero(t *testing.T) {
	// PID 0 is a boundary condition: should not panic.
	// Cross-platform behavior varies:
	//   - Windows: PID 0 is "System Idle Process", tasklist finds it -> true
	//   - Unix:    PID 0 is the current process group, Signal(0) succeeds -> true
	// So we accept either result; the key requirement is no panic.
	got := processRunning(0)
	t.Logf("processRunning(0) = %v (platform-dependent, expected no panic)", got)
}