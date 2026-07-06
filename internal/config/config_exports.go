package config

import "os"

// ResetConfigEnv clears all config-related env vars and resets package-level state.
// Used by tests in this package and by cmd integration tests to prevent env var leakage.
func ResetConfigEnv() {
	for _, k := range []string{
		"PORT", "TARGET_BASE_URL", "GENAI_BASE_URL", "ADMIN_TOKEN",
		"DISABLE_THINKING", "GENAI_MODEL", "MAX_RETRIES", "LOG_LEVEL",
		"COOLDOWN_SEC", "API_KEYS", "KEY", "KEY1", "KEY2", "KEY3",
		"KEY4", "KEY5", "KEYA", "KEYB",
		"BACKOFF_CAP_SEC", "BACKOFF_MULTIPLIER", "CB_RESET_SEC",
		"UPSTREAM_CB_THRESHOLD", "KEYS_FILE", "KEYS_ENCRYPTION_KEY",
		"HEALTH_CHECK_INTERVAL_SEC", "HEALTH_CHECK_PATH", "HEALTH_CHECK_TIMEOUT_SEC",
		"HTTP_TIMEOUT_SEC",
	} {
		os.Unsetenv(k)
	}
	DefaultProviderName = ""
}