// Package config provides centralized configuration management for Alvus.
//
// It reads from .env files and environment variables, validates required
// fields, and supports runtime diffing for hot-reload scenarios.
package config

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"encoding/hex"
)

// Exit codes for controlled process termination.
const (
	ExitCodeOK      int = 0 // 正常
	ExitCodeRuntime int = 1 // 运行时错误（端口占用等）
	ExitCodeConfig  int = 2 // 配置错误（缺少字段、格式错误）
	ExitCodeSystem  int = 3 // 系统资源错误（无法读取文件等）
)

// ConfigError carries a category tag so callers can choose the right exit code.
type ConfigError struct {
	Category string // "config" or "system"
	Message  string
}

func (e *ConfigError) Error() string { return e.Message }

// ConfigChange represents a single field that changed between two Config values.
type ConfigChange struct {
	Field    string
	OldValue string
	NewValue string
}

// Config holds all application configuration.
// Use Load() to create a Config from environment sources, then Validate()
// to ensure required fields are present.
type Config struct {
	Port            int      // HTTP listen port (default 8080)
	TargetBase      string   // Upstream target base URL (required)
	GenaiBase       string   // Generative AI base URL (required)
	AdminToken      string   // Optional admin authentication token
	DisableThinking bool     // Disable thinking mode
	GenaiModel      string   // Generative AI model name
	MaxRetries      int      // Max retry attempts for upstream (default 3)
	LogLevel        string   // Log level (default "info")
	CooldownSec     int      // Cooldown seconds after rate-limit (default 60)
	Keys            []string // API keys (at least one required)
	KeyNames        []string // Corresponding key names (empty string if unnamed), same length as Keys
	KeysFile        string   // JSON file path for key persistence (default "keys.json")
	EncryptionKey   []byte   `json:"-"` // AES-256 key for keys.json encryption (32 bytes, hex-encoded via KEYS_ENCRYPTION_KEY)

	BackoffCapSec       int     // Key 退避上限(秒)，达到此值自动禁用 (default 120)
	BackoffMultiplier   float64 // 指数退避倍数 (default 2)
	CBResetSec          int     // 上游熔断器 OPEN→HALF_OPEN 超时(秒) (default 30)
	UpstreamCBThreshold int     // 上游熔断器连续失败触发阈值 (default 5)

	HealthCheckIntervalSec int    // 健康检查间隔(秒)，默认 30，最小 5
	HealthCheckPath       string // 健康检查路径，默认 "/health"
	HealthCheckTimeoutSec int    // 健康检查超时(秒)，默认 5，最小 1
}

// DefaultConfig returns a Config with all optional fields set to their defaults.
func DefaultConfig() *Config {
	return &Config{
		Port:                8080,
		MaxRetries:          3,
		LogLevel:            "info",
		CooldownSec:         60,
		BackoffCapSec:       120,
		BackoffMultiplier:   2,
		CBResetSec:          30,
		UpstreamCBThreshold: 5,
		HealthCheckIntervalSec: 30,
		HealthCheckPath:       "/health",
		HealthCheckTimeoutSec:  5,
		KeysFile:            "keys.json",
	}
}

// Load reads configuration from the given .env file (if non-empty) and from
// environment variables. Environment variables take precedence over .env file values.
//
// Supported environment variables:
//   - PORT (int, default 8080)
//   - TARGET_BASE_URL (string, required)
//   - GENAI_BASE_URL (string, required)
//   - ADMIN_TOKEN (string, optional)
//   - DISABLE_THINKING (bool: "true"/"1"/"yes")
//   - GENAI_MODEL (string, optional)
//   - MAX_RETRIES (int, default 3)
//   - LOG_LEVEL (string, default "info")
//   - COOLDOWN_SEC (int, default 60)
//   - API_KEYS (comma-separated, required — at least one; use key==name to name a key)
//   - KEY (fallback single/comma-separated)
//   - KEY1, KEY2, ..., KEY5 (fallback individual keys)
//   - KEYA, KEYB (fallback individual keys)
//   - BACKOFF_CAP_SEC (int, default 120) — Key 退避上限(秒)
//   - BACKOFF_MULTIPLIER (float, default 2) — 指数退避倍数
//   - CB_RESET_SEC (int, default 30) — 上游熔断器 OPEN→HALF_OPEN 超时(秒)
//   - UPSTREAM_CB_THRESHOLD (int, default 5) — 上游熔断器连续失败触发阈值
//   - HEALTH_CHECK_INTERVAL_SEC (int, default 30) — 健康检查间隔(秒)
//   - HEALTH_CHECK_PATH (string, default "/health") — 健康检查路径
//   - HEALTH_CHECK_TIMEOUT_SEC (int, default 5) — 健康检查超时(秒)
//   - KEYS_FILE (string, default "keys.json") — JSON 文件路径，用于持久化存储 API Key 状态
func Load(envPath string) (*Config, error) {
	if envPath != "" {
		if err := loadDotEnv(envPath); err != nil {
			return nil, &ConfigError{
				Category: "system",
				Message:  fmt.Sprintf("系统错误: 读取配置文件 %q 失败: %v", envPath, err),
			}
		}
	}

	cfg := DefaultConfig()

	// Port
	if v := os.Getenv("PORT"); v != "" {
		port, err := strconv.Atoi(v)
		if err != nil {
			return nil, &ConfigError{Category: "config", Message: fmt.Sprintf("配置错误: PORT=\"%s\" 不是有效端口号，有效范围 1-65535", v)}
		}
		cfg.Port = port
	}

	// TargetBase
	if v := os.Getenv("TARGET_BASE_URL"); v != "" {
		cfg.TargetBase = strings.TrimRight(v, "/")
	}

	// GenaiBase
	if v := os.Getenv("GENAI_BASE_URL"); v != "" {
		cfg.GenaiBase = strings.TrimRight(v, "/")
	}

	// AdminToken
	if v := os.Getenv("ADMIN_TOKEN"); v != "" {
		cfg.AdminToken = v
	}

	// DisableThinking
	if v := os.Getenv("DISABLE_THINKING"); v != "" {
		v = strings.ToLower(strings.TrimSpace(v))
		cfg.DisableThinking = v == "true" || v == "1" || v == "yes"
	}

	// GenaiModel
	if v := os.Getenv("GENAI_MODEL"); v != "" {
		cfg.GenaiModel = v
	}

	// MaxRetries
	if v := os.Getenv("MAX_RETRIES"); v != "" {
		retries, err := strconv.Atoi(v)
		if err != nil {
			return nil, &ConfigError{Category: "config", Message: fmt.Sprintf("配置错误: MAX_RETRIES=\"%s\" 不是有效整数，有效范围 1-100", v)}
		}
		cfg.MaxRetries = retries
	}

	// LogLevel
	if v := os.Getenv("LOG_LEVEL"); v != "" {
		cfg.LogLevel = v
	}

	// CooldownSec
	if v := os.Getenv("COOLDOWN_SEC"); v != "" {
		cooldown, err := strconv.Atoi(v)
		if err != nil {
			return nil, &ConfigError{Category: "config", Message: fmt.Sprintf("配置错误: COOLDOWN_SEC=\"%s\" 不是有效整数，有效范围 1-86400", v)}
		}
		cfg.CooldownSec = cooldown
	}

	// BackoffCapSec
	if v := os.Getenv("BACKOFF_CAP_SEC"); v != "" {
		capSec, err := strconv.Atoi(v)
		if err != nil {
			return nil, &ConfigError{Category: "config", Message: fmt.Sprintf("配置错误: BACKOFF_CAP_SEC=\"%s\" 不是有效整数，有效范围 30-3600", v)}
		}
		cfg.BackoffCapSec = capSec
	}

	// BackoffMultiplier
	if v := os.Getenv("BACKOFF_MULTIPLIER"); v != "" {
		mult, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, &ConfigError{Category: "config", Message: fmt.Sprintf("配置错误: BACKOFF_MULTIPLIER=\"%s\" 不是有效数值，有效范围 >= 1.0", v)}
		}
		cfg.BackoffMultiplier = mult
	}

	// CBResetSec
	if v := os.Getenv("CB_RESET_SEC"); v != "" {
		resetSec, err := strconv.Atoi(v)
		if err != nil {
			return nil, &ConfigError{Category: "config", Message: fmt.Sprintf("配置错误: CB_RESET_SEC=\"%s\" 不是有效整数，有效范围 5-3600", v)}
		}
		cfg.CBResetSec = resetSec
	}

	// UpstreamCBThreshold
	if v := os.Getenv("UPSTREAM_CB_THRESHOLD"); v != "" {
		threshold, err := strconv.Atoi(v)
		if err != nil {
			return nil, &ConfigError{Category: "config", Message: fmt.Sprintf("配置错误: UPSTREAM_CB_THRESHOLD=\"%s\" 不是有效整数，有效范围 2-100", v)}
		}
		cfg.UpstreamCBThreshold = threshold
	}

	// HealthCheckIntervalSec
	if v := os.Getenv("HEALTH_CHECK_INTERVAL_SEC"); v != "" {
		interval, err := strconv.Atoi(v)
		if err != nil {
			return nil, &ConfigError{Category: "config", Message: fmt.Sprintf("配置错误: HEALTH_CHECK_INTERVAL_SEC=\"%s\" 不是有效整数，有效范围 5-3600", v)}
		}
		cfg.HealthCheckIntervalSec = interval
	}

	// HealthCheckPath
	if v := os.Getenv("HEALTH_CHECK_PATH"); v != "" {
		cfg.HealthCheckPath = v
	}

	// HealthCheckTimeoutSec
	if v := os.Getenv("HEALTH_CHECK_TIMEOUT_SEC"); v != "" {
		timeout, err := strconv.Atoi(v)
		if err != nil {
			return nil, &ConfigError{Category: "config", Message: fmt.Sprintf("配置错误: HEALTH_CHECK_TIMEOUT_SEC=\"%s\" 不是有效整数，有效范围 1-60", v)}
		}
		cfg.HealthCheckTimeoutSec = timeout
	}

	// KeysFile
	if v := os.Getenv("KEYS_FILE"); v != "" {
		cfg.KeysFile = v
	}

	// EncryptionKey
	if v := os.Getenv("KEYS_ENCRYPTION_KEY"); v != "" {
		dec, err := hex.DecodeString(v)
		if err != nil {
			return nil, &ConfigError{Category: "config", Message: fmt.Sprintf("配置错误: KEYS_ENCRYPTION_KEY 不是有效十六进制字符串: %v", err)}
		}
		cfg.EncryptionKey = dec
	}

	// Keys: API_KEYS is primary, then fallback to KEY, KEY1-KEY5, KEYA, KEYB
	keys, names := parseKeys()
	if len(keys) == 0 {
		return nil, &ConfigError{Category: "config", Message: "配置错误: 未设置 API_KEYS，请通过环境变量或 .env 文件设置至少一个 API Key"}
	}
	cfg.Keys = keys
	cfg.KeyNames = names

	return cfg, nil
}

// parseKeys reads API keys from environment variables.
// Primary: API_KEYS (comma-separated, supports key==name format)
// Fallback: KEY (single or comma-separated), KEY1-KEY5, KEYA, KEYB
func parseKeys() ([]string, []string) {
	// Primary: API_KEYS
	if raw := os.Getenv("API_KEYS"); raw != "" {
		return splitKeys(raw)
	}

	// Fallback: KEY
	if raw := os.Getenv("KEY"); raw != "" {
		keys, _ := splitKeys(raw)
		return keys, make([]string, len(keys))
	}

	// Fallback: KEY1-KEY5, KEYA, KEYB
	var keys []string
	for i := 1; i <= 5; i++ {
		if k := os.Getenv("KEY" + strconv.Itoa(i)); k != "" {
			keys = append(keys, k)
		}
	}
	if k := os.Getenv("KEYA"); k != "" {
		keys = append(keys, k)
	}
	if k := os.Getenv("KEYB"); k != "" {
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return nil, nil
	}
	return keys, make([]string, len(keys))
}

// splitKeys parses a comma-separated list, where each element can be
// either a bare key ("key") or a named key ("key==name").
func splitKeys(raw string) ([]string, []string) {
	var keys []string
	var names []string
	for _, part := range strings.Split(raw, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		// Check for key==name format
		if idx := strings.Index(part, "=="); idx > 0 {
			keys = append(keys, part[:idx])
			names = append(names, part[idx+2:])
		} else {
			keys = append(keys, part)
			names = append(names, "")
		}
	}
	return keys, names
}

// Validate checks that all required fields are present and valid.
// Returns a descriptive error for the first problem found.
func (c *Config) Validate() error {
	if c.Port < 1 || c.Port > 65535 {
		return &ConfigError{Category: "config", Message: fmt.Sprintf("配置错误: PORT=%d 不在有效范围(1-65535)内", c.Port)}
	}
	if c.TargetBase == "" {
		return &ConfigError{Category: "config", Message: "配置错误: TARGET_BASE_URL 为必填字段，请设置上游 API 基础地址"}
	}
	if c.GenaiBase == "" {
		return &ConfigError{Category: "config", Message: "配置错误: GENAI_BASE_URL 为必填字段，请设置 GenAI API 基础地址"}
	}
	if len(c.Keys) == 0 {
		return &ConfigError{Category: "config", Message: "配置错误: 至少需要一个 API Key（通过 API_KEYS 设置）"}
	}
	if c.BackoffCapSec < 30 {
		return &ConfigError{Category: "config", Message: fmt.Sprintf("配置错误: BACKOFF_CAP_SEC=%d 不能小于 30 秒", c.BackoffCapSec)}
	}
	if c.BackoffMultiplier < 1 {
		return &ConfigError{Category: "config", Message: fmt.Sprintf("配置错误: BACKOFF_MULTIPLIER=%.1f 不能小于 1.0", c.BackoffMultiplier)}
	}
	if c.CBResetSec < 5 {
		return &ConfigError{Category: "config", Message: fmt.Sprintf("配置错误: CB_RESET_SEC=%d 不能小于 5 秒", c.CBResetSec)}
	}
	if c.UpstreamCBThreshold < 2 {
		return &ConfigError{Category: "config", Message: fmt.Sprintf("配置错误: UPSTREAM_CB_THRESHOLD=%d 不能小于 2", c.UpstreamCBThreshold)}
	}
	if c.HealthCheckIntervalSec < 5 {
		return &ConfigError{Category: "config", Message: fmt.Sprintf("配置错误: HEALTH_CHECK_INTERVAL_SEC=%d 不能小于 5", c.HealthCheckIntervalSec)}
	}
	if c.HealthCheckTimeoutSec < 1 {
		return &ConfigError{Category: "config", Message: fmt.Sprintf("配置错误: HEALTH_CHECK_TIMEOUT_SEC=%d 不能小于 1", c.HealthCheckTimeoutSec)}
	}
	if len(c.EncryptionKey) > 0 && len(c.EncryptionKey) != 32 {
		return &ConfigError{Category: "config", Message: "配置错误: KEYS_ENCRYPTION_KEY 必须正好是 32 字节（64 个十六进制字符）"}
	}
	return nil
}

// Sanitized returns a copy of the Config with sensitive fields masked.
// API keys in Keys are masked to first 4 chars + "..." + last 2 chars.
// KeyNames are not sensitive and are copied as-is.
func (c *Config) Sanitized() *Config {
	s := *c // shallow copy
	s.Keys = make([]string, len(c.Keys))
	for i, k := range c.Keys {
		s.Keys[i] = maskKey(k)
	}
	s.KeyNames = make([]string, len(c.KeyNames))
	copy(s.KeyNames, c.KeyNames)
	s.EncryptionKey = nil // excluded from sanitized output
	return &s
}

func maskKey(key string) string {
	if len(key) <= 6 {
		return "****"
	}
	return key[:4] + "..." + key[len(key)-2:]
}

// Diff returns a list of ConfigChange entries describing what differs
// between c and other. Sensitive fields (Keys) are masked in the output.
// Key names are serialized alongside keys (key==name format) in the diff.
func (c *Config) Diff(other *Config) []ConfigChange {
	var changes []ConfigChange

	if c.Port != other.Port {
		changes = append(changes, ConfigChange{
			Field:    "PORT",
			OldValue: strconv.Itoa(c.Port),
			NewValue: strconv.Itoa(other.Port),
		})
	}
	if c.TargetBase != other.TargetBase {
		changes = append(changes, ConfigChange{
			Field:    "TARGET_BASE_URL",
			OldValue: c.TargetBase,
			NewValue: other.TargetBase,
		})
	}
	if c.GenaiBase != other.GenaiBase {
		changes = append(changes, ConfigChange{
			Field:    "GENAI_BASE_URL",
			OldValue: c.GenaiBase,
			NewValue: other.GenaiBase,
		})
	}
	if c.AdminToken != other.AdminToken {
		changes = append(changes, ConfigChange{
			Field:    "ADMIN_TOKEN",
			OldValue: "(redacted)",
			NewValue: "(redacted)",
		})
	}
	if c.DisableThinking != other.DisableThinking {
		changes = append(changes, ConfigChange{
			Field:    "DISABLE_THINKING",
			OldValue: fmt.Sprintf("%t", c.DisableThinking),
			NewValue: fmt.Sprintf("%t", other.DisableThinking),
		})
	}
	if c.GenaiModel != other.GenaiModel {
		changes = append(changes, ConfigChange{
			Field:    "GENAI_MODEL",
			OldValue: c.GenaiModel,
			NewValue: other.GenaiModel,
		})
	}
	if c.MaxRetries != other.MaxRetries {
		changes = append(changes, ConfigChange{
			Field:    "MAX_RETRIES",
			OldValue: strconv.Itoa(c.MaxRetries),
			NewValue: strconv.Itoa(other.MaxRetries),
		})
	}
	if c.LogLevel != other.LogLevel {
		changes = append(changes, ConfigChange{
			Field:    "LOG_LEVEL",
			OldValue: c.LogLevel,
			NewValue: other.LogLevel,
		})
	}
	if c.CooldownSec != other.CooldownSec {
		changes = append(changes, ConfigChange{
			Field:    "COOLDOWN_SEC",
			OldValue: strconv.Itoa(c.CooldownSec),
			NewValue: strconv.Itoa(other.CooldownSec),
		})
	}
	if c.BackoffCapSec != other.BackoffCapSec {
		changes = append(changes, ConfigChange{
			Field:    "BACKOFF_CAP_SEC",
			OldValue: strconv.Itoa(c.BackoffCapSec),
			NewValue: strconv.Itoa(other.BackoffCapSec),
		})
	}
	if c.BackoffMultiplier != other.BackoffMultiplier {
		changes = append(changes, ConfigChange{
			Field:    "BACKOFF_MULTIPLIER",
			OldValue: strconv.FormatFloat(c.BackoffMultiplier, 'g', -1, 64),
			NewValue: strconv.FormatFloat(other.BackoffMultiplier, 'g', -1, 64),
		})
	}
	if c.CBResetSec != other.CBResetSec {
		changes = append(changes, ConfigChange{
			Field:    "CB_RESET_SEC",
			OldValue: strconv.Itoa(c.CBResetSec),
			NewValue: strconv.Itoa(other.CBResetSec),
		})
	}
	if c.UpstreamCBThreshold != other.UpstreamCBThreshold {
		changes = append(changes, ConfigChange{
			Field:    "UPSTREAM_CB_THRESHOLD",
			OldValue: strconv.Itoa(c.UpstreamCBThreshold),
			NewValue: strconv.Itoa(other.UpstreamCBThreshold),
		})
	}
	if c.HealthCheckIntervalSec != other.HealthCheckIntervalSec {
		changes = append(changes, ConfigChange{
			Field:    "HEALTH_CHECK_INTERVAL_SEC",
			OldValue: strconv.Itoa(c.HealthCheckIntervalSec),
			NewValue: strconv.Itoa(other.HealthCheckIntervalSec),
		})
	}
	if c.HealthCheckPath != other.HealthCheckPath {
		changes = append(changes, ConfigChange{
			Field:    "HEALTH_CHECK_PATH",
			OldValue: c.HealthCheckPath,
			NewValue: other.HealthCheckPath,
		})
	}
	if c.HealthCheckTimeoutSec != other.HealthCheckTimeoutSec {
		changes = append(changes, ConfigChange{
			Field:    "HEALTH_CHECK_TIMEOUT_SEC",
			OldValue: strconv.Itoa(c.HealthCheckTimeoutSec),
			NewValue: strconv.Itoa(other.HealthCheckTimeoutSec),
		})
	}
	// EncryptionKey: only expose set/unset state
	if string(c.EncryptionKey) != string(other.EncryptionKey) {
		changes = append(changes, ConfigChange{
			Field:    "KEYS_ENCRYPTION_KEY",
			OldValue: encKeyState(c.EncryptionKey),
			NewValue: encKeyState(other.EncryptionKey),
		})
	}
	// Keys: compare as masked strings (with names)
	if !stringSliceEqual(c.Keys, other.Keys) {
		oldKeys := maskedSliceWithNames(c.Keys, c.KeyNames)
		newKeys := maskedSliceWithNames(other.Keys, other.KeyNames)
		changes = append(changes, ConfigChange{
			Field:    "API_KEYS",
			OldValue: strings.Join(oldKeys, ","),
			NewValue: strings.Join(newKeys, ","),
		})
	}

	// Sort for deterministic output
	sort.Slice(changes, func(i, j int) bool {
		return changes[i].Field < changes[j].Field
	})
	return changes
}

func joinKeyName(key, name string) string {
	if name == "" {
		return key
	}
	return key + "==" + name
}

func maskedSliceWithNames(keys []string, names []string) []string {
	result := make([]string, len(keys))
	for i, k := range keys {
		n := ""
		if i < len(names) {
			n = names[i]
		}
		result[i] = joinKeyName(maskKey(k), n)
	}
	return result
}

func stringSliceEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// loadDotEnv reads a .env file and sets environment variables.
// Existing environment variables are NOT overwritten (env has higher priority).
func encKeyState(key []byte) string {
	if len(key) == 0 {
		return "unset"
	}
	return "set (32 bytes)"
}

func loadDotEnv(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // missing .env is not an error here
		}
		return fmt.Errorf("读取文件失败: %w", err)
	}

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		k := strings.TrimSpace(parts[0])
		v := strings.TrimSpace(parts[1])
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
	return nil
}
