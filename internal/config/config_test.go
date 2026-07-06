//go:build unit

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"akswitch/internal/utils"
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

	// Sanitized copy: first 4 + "..." + last 4 chars (per utils.MaskKey)
	if s.Keys[0] != "nvap...sGW1" {
		t.Errorf("sanitized Keys[0] = %q, want %q", s.Keys[0], "nvap...sGW1")
	}
	if s.Keys[1] != "****" {
		t.Errorf("short key masked to %q, want %q", s.Keys[1], "****")
	}
	if s.Keys[2] != "nvap...Zu3Q" {
		t.Errorf("sanitized Keys[2] = %q, want %q", s.Keys[2], "nvap...Zu3Q")
	}

	// Sanitized must not share underlying array with original
	s.Keys[0] = "tampered"
	if cfg.Keys[0] == "tampered" {
		t.Error("Sanitized() returned a view into original, not a copy")
	}
}

func TestSanitized_UsesUtilsMaskKey(t *testing.T) {
	key := "sk-abcdefghijklmn"
	cfg := &Config{Keys: []string{key}}
	s := cfg.Sanitized()
	expected := utils.MaskKey(key)
	if s.Keys[0] != expected {
		t.Errorf("Sanitized Keys[0] = %q, want %q (must match utils.MaskKey)", s.Keys[0], expected)
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
	cfg := DefaultConfig()
	cfg.TargetBase = "https://example.com"
	cfg.GenaiBase = "https://ai.example.com"
	cfg.Keys = []string{"nvapi-key1"}

	if cfg.EncryptionKey != nil {
		t.Error("EncryptionKey should be nil by default")
	}
}

func TestConfig_EncryptionKey_Valid(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TargetBase = "https://example.com"
	cfg.GenaiBase = "https://ai.example.com"
	cfg.Keys = []string{"nvapi-key1"}
	cfg.EncryptionKey = []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f}

	if len(cfg.EncryptionKey) != 32 {
		t.Fatalf("EncryptionKey length = %d, want 32", len(cfg.EncryptionKey))
	}
	if cfg.EncryptionKey[0] != 0x00 || cfg.EncryptionKey[31] != 0x1f {
		t.Error("EncryptionKey decoded incorrectly")
	}
}

func TestConfig_EncryptionKey_TooShort(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TargetBase = "https://example.com"
	cfg.GenaiBase = "https://ai.example.com"
	cfg.Keys = []string{"nvapi-key1"}
	cfg.EncryptionKey = []byte{0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f}

	err := cfg.Validate()
	if err == nil {
		t.Error("Validate() expected error for short key, got nil")
	}
}

func TestConfig_EncryptionKey_TooLong(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TargetBase = "https://example.com"
	cfg.GenaiBase = "https://ai.example.com"
	cfg.Keys = []string{"nvapi-key1"}
	cfg.EncryptionKey = make([]byte, 64)

	err := cfg.Validate()
	if err == nil {
		t.Error("Validate() expected error for long key, got nil")
	}
}

func TestConfig_EncryptionKey_InvalidLength(t *testing.T) {
	cfg := DefaultConfig()
	cfg.TargetBase = "https://example.com"
	cfg.GenaiBase = "https://ai.example.com"
	cfg.Keys = []string{"nvapi-key1"}
	// 1-byte key is not 32 bytes - should fail validation
	cfg.EncryptionKey = []byte{0x01}

	err := cfg.Validate()
	if err == nil {
		t.Error("Validate() expected error for invalid key length (1), got nil")
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

// ============================================================
// TOML 配置测试
// ============================================================

func TestLoadToml_ExistingFile(t *testing.T) {
	tmpDir := t.TempDir()
	tomlPath := filepath.Join(tmpDir, "config.toml")
	content := `port = 9090

[provider.default]
target = "https://api.example.com"
genai = "https://ai.example.com"
cooldown_sec = 45
max_retries = 7
`
	if err := os.WriteFile(tomlPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadToml(tomlPath)
	if err != nil {
		t.Fatalf("LoadToml() unexpected error: %v", err)
	}
	if cfg.TargetBase != "https://api.example.com" {
		t.Errorf("TargetBase = %q, want %q", cfg.TargetBase, "https://api.example.com")
	}
	if cfg.GenaiBase != "https://ai.example.com" {
		t.Errorf("GenaiBase = %q, want %q", cfg.GenaiBase, "https://ai.example.com")
	}
	if cfg.Port != 9090 {
		t.Errorf("Port = %d, want %d", cfg.Port, 9090)
	}
	if cfg.CooldownSec != 45 {
		t.Errorf("CooldownSec = %d, want %d", cfg.CooldownSec, 45)
	}
	if cfg.MaxRetries != 7 {
		t.Errorf("MaxRetries = %d, want %d", cfg.MaxRetries, 7)
	}
}

func TestLoadToml_NotExist(t *testing.T) {
	_, err := LoadToml("/nonexistent/path/config.toml")
	if err == nil {
		t.Error("LoadToml() expected error for non-existent file, got nil")
	}
}

func TestLoadToml_Malformed(t *testing.T) {
	tmpDir := t.TempDir()
	tomlPath := filepath.Join(tmpDir, "bad.toml")
	if err := os.WriteFile(tomlPath, []byte("this is not toml {{"), 0644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadToml(tomlPath)
	if err == nil {
		t.Error("LoadToml() expected error for malformed TOML, got nil")
	}
}

func TestSaveToml_LoadToml_Roundtrip(t *testing.T) {
	orig := DefaultConfig()
	orig.TargetBase = "https://api.example.com"
	orig.GenaiBase = "https://ai.example.com"
	orig.Port = 7070
	orig.CooldownSec = 30
	orig.MaxRetries = 5

	tmpDir := t.TempDir()
	tomlPath := filepath.Join(tmpDir, "roundtrip.toml")
	if err := SaveTomlConfig(configToToml(orig), tomlPath); err != nil {
		t.Fatalf("SaveTomlConfig() error: %v", err)
	}

	loaded, err := LoadToml(tomlPath)
	if err != nil {
		t.Fatalf("LoadToml() error: %v", err)
	}

	if loaded.TargetBase != orig.TargetBase {
		t.Errorf("TargetBase = %q, want %q", loaded.TargetBase, orig.TargetBase)
	}
	if loaded.GenaiBase != orig.GenaiBase {
		t.Errorf("GenaiBase = %q, want %q", loaded.GenaiBase, orig.GenaiBase)
	}
	if loaded.Port != orig.Port {
		t.Errorf("Port = %d, want %d", loaded.Port, orig.Port)
	}
	if loaded.CooldownSec != orig.CooldownSec {
		t.Errorf("CooldownSec = %d, want %d", loaded.CooldownSec, orig.CooldownSec)
	}
	if loaded.MaxRetries != orig.MaxRetries {
		t.Errorf("MaxRetries = %d, want %d", loaded.MaxRetries, orig.MaxRetries)
	}
}

func TestLoadToml_MissingFieldsUseDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	tomlPath := filepath.Join(tmpDir, "minimal.toml")
	content := `[provider.default]
target = "https://api.example.com"
`
	if err := os.WriteFile(tomlPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadToml(tomlPath)
	if err != nil {
		t.Fatalf("LoadToml() unexpected error: %v", err)
	}
	// TargetBase should be set from TOML
	if cfg.TargetBase != "https://api.example.com" {
		t.Errorf("TargetBase = %q, want %q", cfg.TargetBase, "https://api.example.com")
	}
	// GenaiBase should be empty (not set in TOML)
	if cfg.GenaiBase != "" {
		t.Errorf("GenaiBase = %q, want empty", cfg.GenaiBase)
	}
	// Port should use default from DefaultConfig
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want default 8080", cfg.Port)
	}
	// CooldownSec should use default from DefaultConfig
	if cfg.CooldownSec != 15 {
		t.Errorf("CooldownSec = %d, want default 60", cfg.CooldownSec)
	}
	// MaxRetries should use default from DefaultConfig
	if cfg.MaxRetries != 2 {
		t.Errorf("MaxRetries = %d, want default 3", cfg.MaxRetries)
	}
}

// ============================================================
// TomlProviderConfig 扩展字段测试
// ============================================================

func TestTomlProviderConfig_AllFields(t *testing.T) {
	content := `port = 7070

[provider.default]
	target = "https://api.example.com"
	genai = "https://ai.example.com"
	cooldown_sec = 45
	max_retries = 7
	disable_thinking = true
	genai_model = "opus-4.8"
	log_level = "debug"
	admin_token = "myadmintoken"
	keys_file = "/data/keys.json"
	backoff_cap_sec = 300
	backoff_multiplier = 3.5
	cb_reset_sec = 60
	upstream_cb_threshold = 10
	health_check_interval_sec = 15
		log_file = "/var/log/akswitch.log"
		log_max_size = 200
		log_max_age = 30
`
	path := writeTempToml(t, content)
	cfg, err := LoadToml(path)
	if err != nil {
		t.Fatalf("LoadToml() unexpected error: %v", err)
	}

	if cfg.TargetBase != "https://api.example.com" {
		t.Errorf("TargetBase = %q, want %q", cfg.TargetBase, "https://api.example.com")
	}
	if cfg.GenaiBase != "https://ai.example.com" {
		t.Errorf("GenaiBase = %q, want %q", cfg.GenaiBase, "https://ai.example.com")
	}
	if cfg.Port != 7070 {
		t.Errorf("Port = %d, want %d", cfg.Port, 7070)
	}
	if cfg.CooldownSec != 45 {
		t.Errorf("CooldownSec = %d, want %d", cfg.CooldownSec, 45)
	}
	if cfg.MaxRetries != 7 {
		t.Errorf("MaxRetries = %d, want %d", cfg.MaxRetries, 7)
	}
	if !cfg.DisableThinking {
		t.Error("DisableThinking = false, want true")
	}
	if cfg.GenaiModel != "opus-4.8" {
		t.Errorf("GenaiModel = %q, want %q", cfg.GenaiModel, "opus-4.8")
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want %q", cfg.LogLevel, "debug")
	}
	if cfg.AdminToken != "myadmintoken" {
		t.Errorf("AdminToken = %q, want %q", cfg.AdminToken, "myadmintoken")
	}
	if cfg.KeysFile != "/data/keys.json" {
		t.Errorf("KeysFile = %q, want %q", cfg.KeysFile, "/data/keys.json")
	}
	if cfg.BackoffCapSec != 300 {
		t.Errorf("BackoffCapSec = %d, want %d", cfg.BackoffCapSec, 300)
	}
	if cfg.BackoffMultiplier != 3.5 {
		t.Errorf("BackoffMultiplier = %g, want %g", cfg.BackoffMultiplier, 3.5)
	}
	if cfg.CBResetSec != 60 {
		t.Errorf("CBResetSec = %d, want %d", cfg.CBResetSec, 60)
	}
	if cfg.UpstreamCBThreshold != 10 {
		t.Errorf("UpstreamCBThreshold = %d, want %d", cfg.UpstreamCBThreshold, 10)
	}
	if cfg.HealthCheckIntervalSec != 15 {
		t.Errorf("HealthCheckIntervalSec = %d, want %d", cfg.HealthCheckIntervalSec, 15)
	}
	if cfg.LogFile != "/var/log/akswitch.log" {
		t.Errorf("LogFile = %q, want %q", cfg.LogFile, "/var/log/akswitch.log")
	}
	if cfg.LogMaxSize != 200 {
		t.Errorf("LogMaxSize = %d, want %d", cfg.LogMaxSize, 200)
	}
	if cfg.LogMaxAge != 30 {
		t.Errorf("LogMaxAge = %d, want %d", cfg.LogMaxAge, 30)
	}
}

func TestTomlProviderConfig_DefaultValues(t *testing.T) {
	content := `[provider.default]
	target = "https://api.example.com"
	genai = "https://ai.example.com"
`
	path := writeTempToml(t, content)
	cfg, err := LoadToml(path)
	if err != nil {
		t.Fatalf("LoadToml() unexpected error: %v", err)
	}

	// Core fields set from TOML
	if cfg.TargetBase != "https://api.example.com" {
		t.Errorf("TargetBase = %q, want %q", cfg.TargetBase, "https://api.example.com")
	}
	if cfg.GenaiBase != "https://ai.example.com" {
		t.Errorf("GenaiBase = %q, want %q", cfg.GenaiBase, "https://ai.example.com")
	}

	// All optional fields should fall through to DefaultConfig
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want default 8080", cfg.Port)
	}
	if cfg.CooldownSec != 15 {
		t.Errorf("CooldownSec = %d, want default 60", cfg.CooldownSec)
	}
	if cfg.MaxRetries != 2 {
		t.Errorf("MaxRetries = %d, want default 3", cfg.MaxRetries)
	}
	if cfg.DisableThinking {
		t.Error("DisableThinking = true, want default false")
	}
	if cfg.GenaiModel != "" {
		t.Errorf("GenaiModel = %q, want empty", cfg.GenaiModel)
	}
	if cfg.LogLevel != "info" {
		t.Errorf("LogLevel = %q, want default %q", cfg.LogLevel, "info")
	}
	if cfg.AdminToken != "" {
		t.Errorf("AdminToken = %q, want empty", cfg.AdminToken)
	}
	if cfg.KeysFile != "keys.json" {
		t.Errorf("KeysFile = %q, want default %q", cfg.KeysFile, "keys.json")
	}
	if cfg.BackoffCapSec != 120 {
		t.Errorf("BackoffCapSec = %d, want default %d", cfg.BackoffCapSec, 120)
	}
	if cfg.BackoffMultiplier != 2 {
		t.Errorf("BackoffMultiplier = %g, want default %g", cfg.BackoffMultiplier, 2.0)
	}
	if cfg.CBResetSec != 30 {
		t.Errorf("CBResetSec = %d, want default %d", cfg.CBResetSec, 30)
	}
	if cfg.UpstreamCBThreshold != 5 {
		t.Errorf("UpstreamCBThreshold = %d, want default %d", cfg.UpstreamCBThreshold, 5)
	}
	if cfg.HealthCheckIntervalSec != 30 {
		t.Errorf("HealthCheckIntervalSec = %d, want default %d", cfg.HealthCheckIntervalSec, 30)
	}
	if cfg.LogFile != "" {
		t.Errorf("LogFile = %q, want empty (default)", cfg.LogFile)
	}
	if cfg.LogMaxSize != 100 {
		t.Errorf("LogMaxSize = %d, want default 100", cfg.LogMaxSize)
	}
	if cfg.LogMaxAge != 7 {
		t.Errorf("LogMaxAge = %d, want default 7", cfg.LogMaxAge)
	}
}

func TestTomlProviderConfig_Roundtrip(t *testing.T) {
	orig := DefaultConfig()
	orig.TargetBase = "https://api.example.com"
	orig.GenaiBase = "https://ai.example.com"
	orig.Port = 7070
	orig.CooldownSec = 45
	orig.MaxRetries = 7
	orig.DisableThinking = true
	orig.GenaiModel = "sonnet-4.6"
	orig.LogLevel = "warn"
	orig.AdminToken = "secrettoken"
	orig.KeysFile = "/app/keys.json"
	orig.BackoffCapSec = 300
	orig.BackoffMultiplier = 3.5
	orig.CBResetSec = 60
	orig.UpstreamCBThreshold = 10
	orig.HealthCheckIntervalSec = 15
	orig.LogFile = "/var/log/akswitch.log"
	orig.LogMaxSize = 200
	orig.LogMaxAge = 30

	tmpDir := t.TempDir()
	tomlPath := filepath.Join(tmpDir, "roundtrip_ext.toml")
	if err := SaveTomlConfig(configToToml(orig), tomlPath); err != nil {
		t.Fatalf("SaveTomlConfig() error: %v", err)
	}

	loaded, err := LoadToml(tomlPath)
	if err != nil {
		t.Fatalf("LoadToml() error: %v", err)
	}

	if loaded.TargetBase != orig.TargetBase {
		t.Errorf("TargetBase = %q, want %q", loaded.TargetBase, orig.TargetBase)
	}
	if loaded.GenaiBase != orig.GenaiBase {
		t.Errorf("GenaiBase = %q, want %q", loaded.GenaiBase, orig.GenaiBase)
	}
	if loaded.Port != orig.Port {
		t.Errorf("Port = %d, want %d", loaded.Port, orig.Port)
	}
	if loaded.CooldownSec != orig.CooldownSec {
		t.Errorf("CooldownSec = %d, want %d", loaded.CooldownSec, orig.CooldownSec)
	}
	if loaded.MaxRetries != orig.MaxRetries {
		t.Errorf("MaxRetries = %d, want %d", loaded.MaxRetries, orig.MaxRetries)
	}
	if loaded.DisableThinking != orig.DisableThinking {
		t.Errorf("DisableThinking = %v, want %v", loaded.DisableThinking, orig.DisableThinking)
	}
	if loaded.GenaiModel != orig.GenaiModel {
		t.Errorf("GenaiModel = %q, want %q", loaded.GenaiModel, orig.GenaiModel)
	}
	if loaded.LogLevel != orig.LogLevel {
		t.Errorf("LogLevel = %q, want %q", loaded.LogLevel, orig.LogLevel)
	}
	if loaded.AdminToken != orig.AdminToken {
		t.Errorf("AdminToken = %q, want %q", loaded.AdminToken, orig.AdminToken)
	}
	if loaded.KeysFile != orig.KeysFile {
		t.Errorf("KeysFile = %q, want %q", loaded.KeysFile, orig.KeysFile)
	}
	if loaded.BackoffCapSec != orig.BackoffCapSec {
		t.Errorf("BackoffCapSec = %d, want %d", loaded.BackoffCapSec, orig.BackoffCapSec)
	}
	if loaded.BackoffMultiplier != orig.BackoffMultiplier {
		t.Errorf("BackoffMultiplier = %g, want %g", loaded.BackoffMultiplier, orig.BackoffMultiplier)
	}
	if loaded.CBResetSec != orig.CBResetSec {
		t.Errorf("CBResetSec = %d, want %d", loaded.CBResetSec, orig.CBResetSec)
	}
	if loaded.UpstreamCBThreshold != orig.UpstreamCBThreshold {
		t.Errorf("UpstreamCBThreshold = %d, want %d", loaded.UpstreamCBThreshold, orig.UpstreamCBThreshold)
	}
	if loaded.HealthCheckIntervalSec != orig.HealthCheckIntervalSec {
		t.Errorf("HealthCheckIntervalSec = %d, want %d", loaded.HealthCheckIntervalSec, orig.HealthCheckIntervalSec)
	}
}

func TestXDGConfigPath(t *testing.T) {
	path, err := XDGConfigPath()
	if err != nil {
		t.Fatalf("XDGConfigPath() unexpected error: %v", err)
	}
	if path == "" {
		t.Error("XDGConfigPath() returned empty path")
	}
	if !strings.Contains(path, ".akswitch") {
		t.Errorf("XDGConfigPath() = %q, want path containing \".akswitch\"", path)
	}
	// Should NOT contain AppData, Roaming, or .config
	if strings.Contains(path, "AppData") || strings.Contains(path, "Roaming") || strings.Contains(path, ".config") {
		t.Errorf("XDGConfigPath() = %q, should not contain AppData/Roaming/.config", path)
	}
}

func TestLoadToml_WithGenai(t *testing.T) {
	tmpDir := t.TempDir()
	tomlPath := filepath.Join(tmpDir, "genai.toml")
	content := `[provider.default]
target = "https://api.example.com"
genai = "https://genai.example.com"
`
	if err := os.WriteFile(tomlPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadToml(tomlPath)
	if err != nil {
		t.Fatalf("LoadToml() unexpected error: %v", err)
	}
	if cfg.GenaiBase != "https://genai.example.com" {
		t.Errorf("GenaiBase = %q, want %q", cfg.GenaiBase, "https://genai.example.com")
	}
	if cfg.TargetBase != "https://api.example.com" {
		t.Errorf("TargetBase = %q, want %q", cfg.TargetBase, "https://api.example.com")
	}
}

func TestLoadToml_MultiProvider(t *testing.T) {
	tmpDir := t.TempDir()
	tomlPath := filepath.Join(tmpDir, "multi.toml")
	content := `port = 9090

[provider.primary]
target = "https://primary.example.com"
genai = "https://ai.primary.example.com"

[provider.secondary]
target = "https://secondary.example.com"
genai = "https://ai.secondary.example.com"
`
	if err := os.WriteFile(tomlPath, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadToml(tomlPath)
	if err != nil {
		t.Fatalf("LoadToml() unexpected error: %v", err)
	}
	// Should use first provider (primary) as the main config
	if cfg.TargetBase != "https://primary.example.com" {
		t.Errorf("TargetBase = %q, want %q (first provider)", cfg.TargetBase, "https://primary.example.com")
	}
	if cfg.GenaiBase != "https://ai.primary.example.com" {
		t.Errorf("GenaiBase = %q, want %q (first provider)", cfg.GenaiBase, "https://ai.primary.example.com")
	}
	if cfg.Port != 9090 {
		t.Errorf("Port = %d, want %d (first provider)", cfg.Port, 9090)
	}
}

// ============================================================
// LoadAllTomlProviders 测试
// ============================================================

func writeTempToml(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadAllTomlProviders_MultiProvider(t *testing.T) {
	content := `port = 9090

[provider.sensenova]
target = "https://api.sensenova.com"
genai = "https://ai.sensenova.com"

[provider.nvidia]
target = "https://integrate.api.nvidia.com/v1"
genai = "https://ai.api.nvidia.com"
`
	path := writeTempToml(t, content)
	providers, err := LoadAllTomlProviders(path)
	if err != nil {
		t.Fatalf("LoadAllTomlProviders() unexpected error: %v", err)
	}
	if len(providers) != 2 {
		t.Fatalf("providers count = %d, want 2", len(providers))
	}

	sense, ok := providers["sensenova"]
	if !ok {
		t.Fatal("providers missing key 'sensenova'")
	}
	if sense.TargetBase != "https://api.sensenova.com" {
		t.Errorf("sensenova TargetBase = %q, want %q", sense.TargetBase, "https://api.sensenova.com")
	}
	if sense.Port != 9090 {
		t.Errorf("sensenova Port = %d, want %d", sense.Port, 9090)
	}

	nv, ok := providers["nvidia"]
	if !ok {
		t.Fatal("providers missing key 'nvidia'")
	}
	if nv.TargetBase != "https://integrate.api.nvidia.com/v1" {
		t.Errorf("nvidia TargetBase = %q, want %q", nv.TargetBase, "https://integrate.api.nvidia.com/v1")
	}
	if nv.Port != 9090 {
		t.Errorf("nvidia Port = %d, want %d", nv.Port, 9090)
	}
}

func TestLoadAllTomlProviders_EmptyProvider(t *testing.T) {
	content := `[server]
port = 8080
`
	path := writeTempToml(t, content)
	_, err := LoadAllTomlProviders(path)
	if err == nil {
		t.Error("LoadAllTomlProviders() expected error for missing [provider] section, got nil")
	}
}

func TestLoadAllTomlProviders_MissingFile(t *testing.T) {
	_, err := LoadAllTomlProviders("/nonexistent/path/config.toml")
	if err == nil {
		t.Error("LoadAllTomlProviders() expected error for non-existent file, got nil")
	}
}

func TestLoadAllTomlProviders_Defaults(t *testing.T) {
	content := `[provider.test]
target = "https://api.example.com"
genai = "https://ai.example.com"
`
	path := writeTempToml(t, content)
	providers, err := LoadAllTomlProviders(path)
	if err != nil {
		t.Fatalf("LoadAllTomlProviders() unexpected error: %v", err)
	}
	cfg, ok := providers["test"]
	if !ok {
		t.Fatal("providers missing key 'test'")
	}
	if cfg.TargetBase != "https://api.example.com" {
		t.Errorf("TargetBase = %q, want %q", cfg.TargetBase, "https://api.example.com")
	}
	if cfg.GenaiBase != "https://ai.example.com" {
		t.Errorf("GenaiBase = %q, want %q", cfg.GenaiBase, "https://ai.example.com")
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want default 8080", cfg.Port)
	}
	if cfg.CooldownSec != 15 {
		t.Errorf("CooldownSec = %d, want default 60", cfg.CooldownSec)
	}
	if cfg.MaxRetries != 2 {
		t.Errorf("MaxRetries = %d, want default 3", cfg.MaxRetries)
	}
}

// ── FindServerPort ───────────────────────────────

func TestFindServerPort_WithPort(t *testing.T) {
	content := "port = 8080\n\n[provider.test]\ntarget = \"https://api.example.com\"\ngenai = \"https://ai.example.com\"\n"
	path := writeTempToml(t, content)
	port := FindServerPort(path)
	if port != 8080 {
		t.Errorf("FindServerPort() = %d, want 8080", port)
	}
}

func TestFindServerPort_NoPort(t *testing.T) {
	content := "port = 0\n\n[provider.test]\ntarget = \"https://api.example.com\"\ngenai = \"https://ai.example.com\"\n"
	path := writeTempToml(t, content)
	port := FindServerPort(path)
	if port != 8080 {
		t.Errorf("FindServerPort() = %d, want 8080 (default)", port)
	}
}

func TestFindServerPort_MissingFile(t *testing.T) {
	port := FindServerPort("/nonexistent/path/config.toml")
	if port != 0 {
		t.Errorf("FindServerPort() = %d, want 0", port)
	}
}

func TestFindServerPort_FirstProviderPicked(t *testing.T) {
	content := "port = 9999\n\n[provider.first]\ntarget = \"https://first.example.com\"\ngenai = \"https://ai.first.example.com\"\n\n[provider.second]\ntarget = \"https://second.example.com\"\ngenai = \"https://ai.second.example.com\"\n"
	path := writeTempToml(t, content)
	port := FindServerPort(path)
	if port != 9999 {
		t.Errorf("FindServerPort() = %d, want 9999", port)
	}
}
