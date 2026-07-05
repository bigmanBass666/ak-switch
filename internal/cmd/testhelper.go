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
	for _, k := range []string{
		"PORT", "TARGET_BASE_URL", "GENAI_BASE_URL", "ADMIN_TOKEN",
		"DISABLE_THINKING", "GENAI_MODEL", "MAX_RETRIES", "LOG_LEVEL",
		"COOLDOWN_SEC", "API_KEYS", "KEY", "KEY1", "KEY2", "KEY3",
		"KEY4", "KEY5", "KEYA", "KEYB",
		"BACKOFF_CAP_SEC", "BACKOFF_MULTIPLIER", "CB_RESET_SEC",
		"UPSTREAM_CB_THRESHOLD", "KEYS_FILE", "KEYS_ENCRYPTION_KEY",
		"HEALTH_CHECK_INTERVAL_SEC", "HEALTH_CHECK_PATH", "HEALTH_CHECK_TIMEOUT_SEC",
	} {
		os.Unsetenv(k)
	}
	config.DefaultProviderName = ""
}
