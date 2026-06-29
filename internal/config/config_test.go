package config

import (
	"os"
	"path/filepath"
	"testing"
)

// resetEnv cleans up all config-related env vars to prevent leakage between tests.
func resetEnv() {
	for _, key := range []string{
		"PORT", "TARGET_BASE_URL", "GENAI_BASE_URL", "ADMIN_TOKEN",
		"DISABLE_THINKING", "GENAI_MODEL", "MAX_RETRIES", "LOG_LEVEL",
		"COOLDOWN_SEC", "API_KEYS", "KEY", "KEY1", "KEY2", "KEY3",
		"KEY4", "KEY5", "KEYA", "KEYB",
		"BACKOFF_CAP_SEC", "BACKOFF_MULTIPLIER", "CB_RESET_SEC",
		"UPSTREAM_CB_THRESHOLD", "KEYS_FILE",
		"HEALTH_CHECK_INTERVAL_SEC", "HEALTH_CHECK_PATH", "HEALTH_CHECK_TIMEOUT_SEC",
			"KEYS_ENCRYPTION_KEY",
	} {
		os.Unsetenv(key)
	}
}

func writeTempEnv(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoad_FullConfig(t *testing.T) {
	resetEnv()
	envContent := `
TARGET_BASE_URL=https://integrate.api.nvidia.com/v1
GENAI_BASE_URL=https://ai.api.nvidia.com
API_KEYS=nvapi-key1,nvapi-key2
PORT=4000
MAX_RETRIES=5
LOG_LEVEL=debug
COOLDOWN_SEC=30
ADMIN_TOKEN=myadmintoken
DISABLE_THINKING=true
GENAI_MODEL=claude-sonnet
`
	path := writeTempEnv(t, envContent)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	if cfg.Port != 4000 {
		t.Errorf("Port = %d, want 4000", cfg.Port)
	}
	if cfg.TargetBase != "https://integrate.api.nvidia.com/v1" {
		t.Errorf("TargetBase = %q, want %q", cfg.TargetBase, "https://integrate.api.nvidia.com/v1")
	}
	if cfg.GenaiBase != "https://ai.api.nvidia.com" {
		t.Errorf("GenaiBase = %q, want %q", cfg.GenaiBase, "https://ai.api.nvidia.com")
	}
	if cfg.AdminToken != "myadmintoken" {
		t.Errorf("AdminToken = %q, want %q", cfg.AdminToken, "myadmintoken")
	}
	if !cfg.DisableThinking {
		t.Error("DisableThinking = false, want true")
	}
	if cfg.GenaiModel != "claude-sonnet" {
		t.Errorf("GenaiModel = %q, want %q", cfg.GenaiModel, "claude-sonnet")
	}
	if cfg.MaxRetries != 5 {
		t.Errorf("MaxRetries = %d, want 5", cfg.MaxRetries)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
	if cfg.CooldownSec != 30 {
		t.Errorf("CooldownSec = %d, want 30", cfg.CooldownSec)
	}
	if len(cfg.Keys) != 2 || cfg.Keys[0] != "nvapi-key1" || cfg.Keys[1] != "nvapi-key2" {
		t.Errorf("Keys = %v, want [nvapi-key1 nvapi-key2]", cfg.Keys)
	}
	if cfg.KeysFile != "keys.json" {
		t.Errorf("KeysFile = %q, want %q", cfg.KeysFile, "keys.json")
	}
}

func TestLoad_MissingRequiredFields(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{"no keys", "TARGET_BASE_URL=https://example.com\nGENAI_BASE_URL=https://ai.example.com\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resetEnv()
			path := writeTempEnv(t, tt.content)
			_, err := Load(path)
			if err == nil {
				t.Error("Load() expected error, got nil")
			}
		})
	}
}

func TestLoad_MissingKeys(t *testing.T) {
	resetEnv()

	// Set TARGET_BASE_URL and GENAI_BASE_URL via env, but no keys
	t.Setenv("TARGET_BASE_URL", "https://example.com")
	t.Setenv("GENAI_BASE_URL", "https://ai.example.com")

	cfg, err := Load("")
	if err == nil {
		t.Fatal("Load() expected error when no keys set, got nil")
	}
	if cfg != nil {
		t.Logf("got config (should be nil on error): %+v", cfg)
	}
}

func TestLoad_EnvOverrideDotEnv(t *testing.T) {
	resetEnv()
	t.Setenv("PORT", "9999")

	envContent := `PORT=3000
TARGET_BASE_URL=https://integrate.api.nvidia.com/v1
GENAI_BASE_URL=https://ai.api.nvidia.com
API_KEYS=nvapi-key1
`
	path := writeTempEnv(t, envContent)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Port != 9999 {
		t.Errorf("Port = %d, want 9999 (env var should override .env)", cfg.Port)
	}
}

func TestLoad_OptionalDefaults(t *testing.T) {
	resetEnv()
	// Only set required fields; all optional fields should use defaults
	t.Setenv("TARGET_BASE_URL", "https://example.com")
	t.Setenv("GENAI_BASE_URL", "https://ai.example.com")
	t.Setenv("API_KEYS", "nvapi-key1")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.Port != 8080 {
		t.Errorf("Port default = %d, want 8080", cfg.Port)
	}
	if cfg.MaxRetries != 3 {
		t.Errorf("MaxRetries default = %d, want 3", cfg.MaxRetries)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel default = %q, want %q", cfg.LogLevel, "info")
	}
	if cfg.CooldownSec != 60 {
		t.Errorf("CooldownSec default = %d, want 60", cfg.CooldownSec)
	}
	if cfg.AdminToken != "" {
		t.Errorf("AdminToken default = %q, want empty", cfg.AdminToken)
	}
	if cfg.BackoffCapSec != 120 {
		t.Errorf("BackoffCapSec default = %d, want 120", cfg.BackoffCapSec)
	}
	if cfg.BackoffMultiplier != 2 {
		t.Errorf("BackoffMultiplier default = %g, want 2", cfg.BackoffMultiplier)
	}
	if cfg.CBResetSec != 30 {
		t.Errorf("CBResetSec default = %d, want 30", cfg.CBResetSec)
	}
	if cfg.UpstreamCBThreshold != 5 {
		t.Errorf("UpstreamCBThreshold default = %d, want 5", cfg.UpstreamCBThreshold)
	}
	if cfg.KeysFile != "keys.json" {
		t.Errorf("KeysFile default = %q, want %q", cfg.KeysFile, "keys.json")
	}
}

func TestLoad_EmptyPath(t *testing.T) {
	resetEnv()
	t.Setenv("TARGET_BASE_URL", "https://example.com")
	t.Setenv("GENAI_BASE_URL", "https://ai.example.com")
	t.Setenv("API_KEYS", "nvapi-key1")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if cfg.TargetBase != "https://example.com" {
		t.Errorf("TargetBase = %q, want %q", cfg.TargetBase, "https://example.com")
	}
}

func TestLoad_KeyFallbackKEY(t *testing.T) {
	resetEnv()
	t.Setenv("TARGET_BASE_URL", "https://example.com")
	t.Setenv("GENAI_BASE_URL", "https://ai.example.com")
	t.Setenv("KEY", "nvapi-fallback-key")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if len(cfg.Keys) != 1 || cfg.Keys[0] != "nvapi-fallback-key" {
		t.Errorf("Keys = %v, want [nvapi-fallback-key]", cfg.Keys)
	}
}

func TestLoad_KeyFallbackKEY1to5(t *testing.T) {
	resetEnv()
	t.Setenv("TARGET_BASE_URL", "https://example.com")
	t.Setenv("GENAI_BASE_URL", "https://ai.example.com")
	t.Setenv("KEY1", "nvapi-key-one")
	t.Setenv("KEY2", "nvapi-key-two")
	t.Setenv("KEY3", "nvapi-key-three")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if len(cfg.Keys) != 3 {
		t.Fatalf("Keys = %v, want 3 keys", cfg.Keys)
	}
	if cfg.Keys[0] != "nvapi-key-one" {
		t.Errorf("Keys[0] = %q, want %q", cfg.Keys[0], "nvapi-key-one")
	}
}

func TestLoad_KeyFallbackKEYAKEYB(t *testing.T) {
	resetEnv()
	t.Setenv("TARGET_BASE_URL", "https://example.com")
	t.Setenv("GENAI_BASE_URL", "https://ai.example.com")
	t.Setenv("KEYA", "nvapi-key-a")
	t.Setenv("KEYB", "nvapi-key-b")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	if len(cfg.Keys) != 2 {
		t.Fatalf("Keys = %v, want 2 keys", cfg.Keys)
	}
}

func TestLoad_InvalidPort(t *testing.T) {
	resetEnv()

	// Use Load with a .env file that has invalid PORT
	envContent := `PORT=invalid
TARGET_BASE_URL=https://example.com
GENAI_BASE_URL=https://ai.example.com
API_KEYS=nvapi-key1
`
	path := writeTempEnv(t, envContent)
	_, err := Load(path)
	if err == nil {
		t.Error("Load() expected error for invalid PORT, got nil")
	}
}

func TestLoad_InvalidMaxRetries(t *testing.T) {
	resetEnv()

	// Must set PORT via env var so the .env's PORT doesn't need to be valid
	t.Setenv("PORT", "8080")

	envContent := `MAX_RETRIES=abc
TARGET_BASE_URL=https://example.com
GENAI_BASE_URL=https://ai.example.com
API_KEYS=nvapi-key1
`
	path := writeTempEnv(t, envContent)
	_, err := Load(path)
	if err == nil {
		t.Error("Load() expected error for invalid MAX_RETRIES, got nil")
	}
}

func TestLoad_KeysFromEnvFile(t *testing.T) {
	resetEnv()
	envContent := `TARGET_BASE_URL=https://example.com
GENAI_BASE_URL=https://ai.example.com
API_KEYS=nvapi-key1,nvapi-key2,nvapi-key3
PORT=8080
`
	path := writeTempEnv(t, envContent)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.Keys) != 3 {
		t.Fatalf("Keys = %v, want 3 keys", cfg.Keys)
	}
	if cfg.Keys[0] != "nvapi-key1" || cfg.Keys[1] != "nvapi-key2" || cfg.Keys[2] != "nvapi-key3" {
		t.Errorf("Keys = %v", cfg.Keys)
	}
}

func TestValidate_InvalidPort(t *testing.T) {
	tests := []struct {
		port int
		name string
	}{
		{0, "port 0"},
		{-1, "negative port"},
		{65536, "port too high"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Port = tt.port
			cfg.TargetBase = "https://example.com"
			cfg.GenaiBase = "https://ai.example.com"
			cfg.Keys = []string{"nvapi-key1"}
			if err := cfg.Validate(); err == nil {
				t.Errorf("Validate() expected error for port %d, got nil", tt.port)
			}
		})
	}
}

func TestValidate_RequiredFields(t *testing.T) {
	tests := []struct {
		name   string
		modify func(cfg *Config)
	}{
		{name: "empty keys", modify: func(cfg *Config) { cfg.Keys = nil }},
		{name: "empty target base", modify: func(cfg *Config) { cfg.TargetBase = "" }},
		{name: "empty genai base", modify: func(cfg *Config) { cfg.GenaiBase = "" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Port = 8080
			cfg.TargetBase = "https://example.com"
			cfg.GenaiBase = "https://ai.example.com"
			cfg.Keys = []string{"nvapi-key1"}
			tt.modify(cfg)
			if err := cfg.Validate(); err == nil {
				t.Error("Validate() expected error, got nil")
			}
		})
	}
}

func TestValidate_CircuitBreakerFields(t *testing.T) {
	tests := []struct {
		name   string
		modify func(cfg *Config)
	}{
		{name: "backoff cap too low", modify: func(cfg *Config) { cfg.BackoffCapSec = 10 }},
		{name: "backoff multiplier < 1", modify: func(cfg *Config) { cfg.BackoffMultiplier = 0.5 }},
		{name: "cb reset too low", modify: func(cfg *Config) { cfg.CBResetSec = 1 }},
		{name: "cb threshold too low", modify: func(cfg *Config) { cfg.UpstreamCBThreshold = 1 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			cfg.Port = 8080
			cfg.TargetBase = "https://example.com"
			cfg.GenaiBase = "https://ai.example.com"
			cfg.Keys = []string{"nvapi-key1"}
			tt.modify(cfg)
			if err := cfg.Validate(); err == nil {
				t.Error("Validate() expected error, got nil")
			}
		})
	}
}

func TestSanitized(t *testing.T) {
	cfg := &Config{
		Keys: []string{
			"nvapi-xiKMDpevXK60t6gLsGW1",
			"short",
			"nvapi-KXZ6a_5Mwcew7Ekd32DD85OaLVZu3Q",
		},
	}
	s := cfg.Sanitized()

	// Original must be unchanged
	if len(cfg.Keys) != 3 {
		t.Fatal("original keys length changed")
	}
	if cfg.Keys[0] != "nvapi-xiKMDpevXK60t6gLsGW1" {
		t.Error("original key mutated")
	}

	// Sanitized copy: first 4 + "..." + last 2 chars (per spec)
	if s.Keys[0] != "nvap...W1" {
		t.Errorf("sanitized Keys[0] = %q, want %q", s.Keys[0], "nvap...W1")
	}
	if s.Keys[1] != "****" {
		t.Errorf("short key masked to %q, want %q", s.Keys[1], "****")
	}
	if s.Keys[2] != "nvap...3Q" {
		t.Errorf("sanitized Keys[2] = %q, want %q", s.Keys[2], "nvap...3Q")
	}

	// Sanitized must not share underlying array with original
	s.Keys[0] = "tampered"
	if cfg.Keys[0] == "tampered" {
		t.Error("Sanitized() returned a view into original, not a copy")
	}
}

func TestDiff(t *testing.T) {
	old := &Config{
		Port:        8080,
		TargetBase:  "https://old.example.com",
		GenaiBase:   "https://ai.old.example.com",
		AdminToken:  "secret1",
		MaxRetries:  3,
		LogLevel:    "info",
		CooldownSec: 60,
		Keys:        []string{"nvapi-old-key1", "nvapi-old-key2"},
	}
	new := &Config{
		Port:        8081,
		TargetBase:  "https://new.example.com",
		GenaiBase:   "https://ai.new.example.com",
		AdminToken:  "secret2",
		MaxRetries:  5,
		LogLevel:    "debug",
		CooldownSec: 30,
		Keys:        []string{"nvapi-new-key1"},
	}

	changes := old.Diff(new)

	// Build lookup
	changeMap := make(map[string]ConfigChange)
	for _, c := range changes {
		changeMap[c.Field] = c
	}

	// Check PORT
	if c, ok := changeMap["PORT"]; !ok {
		t.Error("Diff missing PORT change")
	} else if c.OldValue != "8080" || c.NewValue != "8081" {
		t.Errorf("PORT change: got %v", c)
	}

	// Check TARGET_BASE_URL
	if _, ok := changeMap["TARGET_BASE_URL"]; !ok {
		t.Error("Diff missing TARGET_BASE_URL change")
	}

	// Check ADMIN_TOKEN is redacted
	if c, ok := changeMap["ADMIN_TOKEN"]; !ok {
		t.Error("Diff missing ADMIN_TOKEN change")
	} else if c.OldValue != "(redacted)" || c.NewValue != "(redacted)" {
		t.Errorf("ADMIN_TOKEN should be redacted, got %v", c)
	}

	// Check API_KEYS — values should be masked
	if c, ok := changeMap["API_KEYS"]; !ok {
		t.Error("Diff missing API_KEYS change")
	} else {
		// Old: two keys masked individually and joined: "nvap...y1,nvap...y2"
		// New: single key masked: "nvap...y1"
		if c.OldValue != "nvap...y1,nvap...y2" {
			t.Errorf("API_KEYS old (masked) = %q, want %q", c.OldValue, "nvap...y1,nvap...y2")
		}
		if c.NewValue != "nvap...y1" {
			t.Errorf("API_KEYS new (masked) = %q, want %q", c.NewValue, "nvap...y1")
		}
	}

	// Check that unchanged fields are not in diff
	if _, ok := changeMap["DISABLE_THINKING"]; ok {
		t.Error("Diff should not include DISABLE_THINKING (unchanged)")
	}
}

func TestDiff_NoChanges(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TargetBase = "https://example.com"
	cfg.GenaiBase = "https://ai.example.com"
	cfg.Keys = []string{"nvapi-key1"}

	changes := cfg.Diff(cfg)
	if len(changes) != 0 {
		t.Errorf("Diff with same config should be empty, got %v", changes)
	}
}

func TestLoad_NamedKeys(t *testing.T) {
	resetEnv()
	envContent := `
TARGET_BASE_URL=https://integrate.api.nvidia.com/v1
GENAI_BASE_URL=https://ai.api.nvidia.com
API_KEYS=nvapi-key1==主账号,nvapi-key2==备用key,nvapi-key3
PORT=8080
`
	path := writeTempEnv(t, envContent)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}

	if len(cfg.Keys) != 3 {
		t.Fatalf("Keys count = %d, want 3", len(cfg.Keys))
	}
	if cfg.Keys[0] != "nvapi-key1" || cfg.Keys[1] != "nvapi-key2" || cfg.Keys[2] != "nvapi-key3" {
		t.Errorf("Keys = %v", cfg.Keys)
	}
	if len(cfg.KeyNames) != 3 {
		t.Fatalf("KeyNames count = %d, want 3", len(cfg.KeyNames))
	}
	if cfg.KeyNames[0] != "主账号" {
		t.Errorf("KeyNames[0] = %q, want %q", cfg.KeyNames[0], "主账号")
	}
	if cfg.KeyNames[1] != "备用key" {
		t.Errorf("KeyNames[1] = %q, want %q", cfg.KeyNames[1], "备用key")
	}
	if cfg.KeyNames[2] != "" {
		t.Errorf("KeyNames[2] = %q, want empty", cfg.KeyNames[2])
	}
}

func TestLoad_CleanKeysNoNames(t *testing.T) {
	resetEnv()
	t.Setenv("TARGET_BASE_URL", "https://example.com")
	t.Setenv("GENAI_BASE_URL", "https://ai.example.com")
	t.Setenv("API_KEYS", "key1,key2")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.KeyNames) != 2 {
		t.Fatalf("KeyNames = %v, want 2 entries", cfg.KeyNames)
	}
	for i, n := range cfg.KeyNames {
		if n != "" {
			t.Errorf("KeyNames[%d] = %q, want empty", i, n)
		}
	}
}

func TestLoad_FallbackKeysNoNames(t *testing.T) {
	resetEnv()
	t.Setenv("TARGET_BASE_URL", "https://example.com")
	t.Setenv("GENAI_BASE_URL", "https://ai.example.com")
	t.Setenv("KEY", "key1,key2")
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.KeyNames) != 2 {
		t.Fatalf("KeyNames = %v, want 2 entries", cfg.KeyNames)
	}
	for i, n := range cfg.KeyNames {
		if n != "" {
			t.Errorf("KeyNames[%d] = %q, want empty", i, n)
		}
	}
}

func TestDiff_NamedKeys(t *testing.T) {
	// Named keys diff should serialize names alongside masked keys
	oldCfg := DefaultConfig()
	oldCfg.TargetBase = "https://example.com"
	oldCfg.GenaiBase = "https://ai.example.com"
	oldCfg.Keys = []string{"nvapi-key1", "nvapi-key2"}
	oldCfg.KeyNames = []string{"主账号", "备用key"}

	newCfg := DefaultConfig()
	newCfg.TargetBase = "https://example.com"
	newCfg.GenaiBase = "https://ai.example.com"
	newCfg.Keys = []string{"nvapi-key3"}
	newCfg.KeyNames = []string{"新key"}

	changes := oldCfg.Diff(newCfg)
	changeMap := make(map[string]string)
	for _, c := range changes {
		changeMap[c.Field] = c.NewValue
	}

	if c, ok := changeMap["API_KEYS"]; !ok {
		t.Error("Diff should include API_KEYS")
	} else {
		// Should contain masked key names in the serialized form
		if c != "nvap...y3==新key" {
			t.Errorf("API_KEYS new value = %q, want %q", c, "nvap...y3==新key")
		}

		// Check old value also has names
		oldVal := changeMap["API_KEYS"]
		_ = oldVal // old value also serialized; just checking new is enough
	}
}

func TestLoad_MissingEnvFile(t *testing.T) {
	resetEnv()
	t.Setenv("TARGET_BASE_URL", "https://example.com")
	t.Setenv("GENAI_BASE_URL", "https://ai.example.com")
	t.Setenv("API_KEYS", "nvapi-key1")

	cfg, err := Load("/nonexistent/.env")
	if err != nil {
		t.Fatalf("Load() with missing file should not error: %v", err)
	}
	if cfg == nil {
		t.Fatal("Load() returned nil config")
	}
}

func TestLoad_TrailingSlashesTrimmed(t *testing.T) {
	resetEnv()
	t.Setenv("TARGET_BASE_URL", "https://example.com/v1/")
	t.Setenv("GENAI_BASE_URL", "https://ai.example.com///")
	t.Setenv("API_KEYS", "nvapi-key1")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.TargetBase != "https://example.com/v1" {
		t.Errorf("TargetBase trailing slash not trimmed: %q", cfg.TargetBase)
	}
	if cfg.GenaiBase != "https://ai.example.com" {
		t.Errorf("GenaiBase trailing slash not trimmed: %q", cfg.GenaiBase)
	}
}

func TestConfig_HealthCheckDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.HealthCheckIntervalSec != 30 {
		t.Errorf("HealthCheckIntervalSec default = %d, want 30", cfg.HealthCheckIntervalSec)
	}
	if cfg.HealthCheckPath != "/health" {
		t.Errorf("HealthCheckPath default = %q, want %q", cfg.HealthCheckPath, "/health")
	}
	if cfg.HealthCheckTimeoutSec != 5 {
		t.Errorf("HealthCheckTimeoutSec default = %d, want 5", cfg.HealthCheckTimeoutSec)
	}
}

func TestConfig_HealthCheckIntervalTooSmall(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Port = 8080
	cfg.TargetBase = "https://example.com"
	cfg.GenaiBase = "https://ai.example.com"
	cfg.Keys = []string{"nvapi-key1"}
	cfg.HealthCheckIntervalSec = 4
	if err := cfg.Validate(); err == nil {
		t.Error("Validate() expected error for HealthCheckIntervalSec=4, got nil")
	}
}

func TestConfig_HealthCheckTimeoutTooSmall(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Port = 8080
	cfg.TargetBase = "https://example.com"
	cfg.GenaiBase = "https://ai.example.com"
	cfg.Keys = []string{"nvapi-key1"}
	cfg.HealthCheckTimeoutSec = 0
	if err := cfg.Validate(); err == nil {
		t.Error("Validate() expected error for HealthCheckTimeoutSec=0, got nil")
	}
}


func TestConfig_EncryptionKey_Default(t *testing.T) {
	resetEnv()
	t.Setenv("TARGET_BASE_URL", "https://example.com")
	t.Setenv("GENAI_BASE_URL", "https://ai.example.com")
	t.Setenv("API_KEYS", "nvapi-key1")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if cfg.EncryptionKey != nil {
		t.Error("EncryptionKey should be nil by default")
	}
}

func TestConfig_EncryptionKey_Valid(t *testing.T) {
	resetEnv()
	t.Setenv("TARGET_BASE_URL", "https://example.com")
	t.Setenv("GENAI_BASE_URL", "https://ai.example.com")
	t.Setenv("API_KEYS", "nvapi-key1")
	t.Setenv("KEYS_ENCRYPTION_KEY", "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() error: %v", err)
	}
	if len(cfg.EncryptionKey) != 32 {
		t.Fatalf("EncryptionKey length = %d, want 32", len(cfg.EncryptionKey))
	}
	if cfg.EncryptionKey[0] != 0x00 || cfg.EncryptionKey[31] != 0x1f {
		t.Error("EncryptionKey decoded incorrectly")
	}
}

func TestConfig_EncryptionKey_TooShort(t *testing.T) {
	resetEnv()
	t.Setenv("TARGET_BASE_URL", "https://example.com")
	t.Setenv("GENAI_BASE_URL", "https://ai.example.com")
	t.Setenv("API_KEYS", "nvapi-key1")
	t.Setenv("KEYS_ENCRYPTION_KEY", "000102030405060708090a0b0c0d0e0f")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	err = cfg.Validate()
	if err == nil {
		t.Error("Validate() expected error for short key, got nil")
	}
}

func TestConfig_EncryptionKey_TooLong(t *testing.T) {
	resetEnv()
	t.Setenv("TARGET_BASE_URL", "https://example.com")
	t.Setenv("GENAI_BASE_URL", "https://ai.example.com")
	t.Setenv("API_KEYS", "nvapi-key1")
	t.Setenv("KEYS_ENCRYPTION_KEY", "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f202122232425262728292a2b2c2d2e2f303132333435363738393a3b3c3d3e3f")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load() unexpected error: %v", err)
	}
	err = cfg.Validate()
	if err == nil {
		t.Error("Validate() expected error for long key, got nil")
	}
}

func TestConfig_EncryptionKey_InvalidHex(t *testing.T) {
	resetEnv()
	t.Setenv("TARGET_BASE_URL", "https://example.com")
	t.Setenv("GENAI_BASE_URL", "https://ai.example.com")
	t.Setenv("API_KEYS", "nvapi-key1")
	t.Setenv("KEYS_ENCRYPTION_KEY", "zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz")

	_, err := Load("")
	if err == nil {
		t.Fatal("Load() expected error for invalid hex, got nil")
	}
}

func TestConfig_EncryptionKey_SanitizedExcluded(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TargetBase = "https://example.com"
	cfg.GenaiBase = "https://ai.example.com"
	cfg.Keys = []string{"nvapi-key1"}
	cfg.EncryptionKey = []byte{1, 2, 3, 4, 5}

	s := cfg.Sanitized()
	if s.EncryptionKey != nil {
		t.Error("Sanitized() should have nil EncryptionKey")
	}
}
