//go:build integration

package cmd

import (
	"os"
	"testing"

	"akswitch/internal/config"
)

// RunCommand executes akswitch with the given arguments via Execute.
func RunCommand(t testing.TB, args ...string) error {
	t.Helper()
	oldArgs := os.Args
	t.Cleanup(func() { os.Args = oldArgs })
	os.Args = args
	return Execute("")
}

// ResetConfigEnv clears all config-related env vars and resets package-level state.
func ResetConfigEnv() {
	config.ResetConfigEnv()
}
