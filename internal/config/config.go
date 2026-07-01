// Package config provides centralized configuration management for Alvus.
//
// It reads from .env files and environment variables, validates required
// fields, and supports runtime diffing for hot-reload scenarios.
package config

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"alvus/internal/utils"
	"github.com/pelletier/go-toml/v2"
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
// API keys in Keys are masked via utils.MaskKey — first 4 chars + "..." + last 4 chars.
// KeyNames are not sensitive and are copied as-is.
func (c *Config) Sanitized() *Config {
	s := *c // shallow copy
	s.Keys = make([]string, len(c.Keys))
	for i, k := range c.Keys {
		s.Keys[i] = utils.MaskKey(k)
	}
	s.KeyNames = make([]string, len(c.KeyNames))
	copy(s.KeyNames, c.KeyNames)
	s.EncryptionKey = nil // excluded from sanitized output
	return &s
}

// fieldDef defines a single configuration field for diff comparison.
type fieldDef struct {
	name   string                       // env var name for diff output
	equal  func(c, o *Config) bool      // equality check
	valStr func(c *Config) string       // serializes field value for diff
}

// configDiffFields is the registry of all diff-comparable config fields.
// Add new fields here instead of adding if-blocks to Diff().
var configDiffFields = []fieldDef{
	{"PORT", func(c, o *Config) bool { return c.Port == o.Port }, func(c *Config) string { return strconv.Itoa(c.Port) }},
	{"TARGET_BASE_URL", func(c, o *Config) bool { return c.TargetBase == o.TargetBase }, func(c *Config) string { return c.TargetBase }},
	{"GENAI_BASE_URL", func(c, o *Config) bool { return c.GenaiBase == o.GenaiBase }, func(c *Config) string { return c.GenaiBase }},
	{"DISABLE_THINKING", func(c, o *Config) bool { return c.DisableThinking == o.DisableThinking }, func(c *Config) string { return strconv.FormatBool(c.DisableThinking) }},
	{"GENAI_MODEL", func(c, o *Config) bool { return c.GenaiModel == o.GenaiModel }, func(c *Config) string { return c.GenaiModel }},
	{"MAX_RETRIES", func(c, o *Config) bool { return c.MaxRetries == o.MaxRetries }, func(c *Config) string { return strconv.Itoa(c.MaxRetries) }},
	{"LOG_LEVEL", func(c, o *Config) bool { return c.LogLevel == o.LogLevel }, func(c *Config) string { return c.LogLevel }},
	{"COOLDOWN_SEC", func(c, o *Config) bool { return c.CooldownSec == o.CooldownSec }, func(c *Config) string { return strconv.Itoa(c.CooldownSec) }},
	{"BACKOFF_CAP_SEC", func(c, o *Config) bool { return c.BackoffCapSec == o.BackoffCapSec }, func(c *Config) string { return strconv.Itoa(c.BackoffCapSec) }},
	{"BACKOFF_MULTIPLIER", func(c, o *Config) bool { return c.BackoffMultiplier == o.BackoffMultiplier }, func(c *Config) string { return strconv.FormatFloat(c.BackoffMultiplier, 'g', -1, 64) }},
	{"CB_RESET_SEC", func(c, o *Config) bool { return c.CBResetSec == o.CBResetSec }, func(c *Config) string { return strconv.Itoa(c.CBResetSec) }},
	{"UPSTREAM_CB_THRESHOLD", func(c, o *Config) bool { return c.UpstreamCBThreshold == o.UpstreamCBThreshold }, func(c *Config) string { return strconv.Itoa(c.UpstreamCBThreshold) }},
	{"HEALTH_CHECK_INTERVAL_SEC", func(c, o *Config) bool { return c.HealthCheckIntervalSec == o.HealthCheckIntervalSec }, func(c *Config) string { return strconv.Itoa(c.HealthCheckIntervalSec) }},
	{"HEALTH_CHECK_PATH", func(c, o *Config) bool { return c.HealthCheckPath == o.HealthCheckPath }, func(c *Config) string { return c.HealthCheckPath }},
	{"HEALTH_CHECK_TIMEOUT_SEC", func(c, o *Config) bool { return c.HealthCheckTimeoutSec == o.HealthCheckTimeoutSec }, func(c *Config) string { return strconv.Itoa(c.HealthCheckTimeoutSec) }},
}

// Diff returns a list of ConfigChange entries describing what differs
// between c and other. Sensitive fields (Keys) are masked in the output.
// Key names are serialized alongside keys (key==name format) in the diff.
func (c *Config) Diff(other *Config) []ConfigChange {
	var changes []ConfigChange

	// Iterate over the field registry
	for _, f := range configDiffFields {
		if !f.equal(c, other) {
			changes = append(changes, ConfigChange{
				Field:    f.name,
				OldValue: f.valStr(c),
				NewValue: f.valStr(other),
			})
		}
	}

	// Special fields that need custom handling

	// AdminToken — redact values
	if c.AdminToken != other.AdminToken {
		changes = append(changes, ConfigChange{
			Field:    "ADMIN_TOKEN",
			OldValue: "(redacted)",
			NewValue: "(redacted)",
		})
	}

	// EncryptionKey — only expose set/unset state
	if string(c.EncryptionKey) != string(other.EncryptionKey) {
		changes = append(changes, ConfigChange{
			Field:    "KEYS_ENCRYPTION_KEY",
			OldValue: encKeyState(c.EncryptionKey),
			NewValue: encKeyState(other.EncryptionKey),
		})
	}

	// Keys — compare as masked strings (with names)
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
		result[i] = joinKeyName(utils.MaskKey(k), n)
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

// ---- TOML 配置支持 ----

// TomlProviderConfig 对应 TOML 中单个 [provider.*] 的配置。
type TomlProviderConfig struct {
	Target                 string  `toml:"target"`
	Genai                  string  `toml:"genai,omitempty"`
	Port                   int     `toml:"port,omitempty"`
	CooldownSec            int     `toml:"cooldown_sec,omitempty"`
	MaxRetries             int     `toml:"max_retries,omitempty"`
	DisableThinking        bool    `toml:"disable_thinking,omitempty"`
	GenaiModel             string  `toml:"genai_model,omitempty"`
	LogLevel               string  `toml:"log_level,omitempty"`
	AdminToken             string  `toml:"admin_token,omitempty"`
	KeysFile               string  `toml:"keys_file,omitempty"`
	BackoffCapSec          int     `toml:"backoff_cap_sec,omitempty"`
	BackoffMultiplier      float64 `toml:"backoff_multiplier,omitempty"`
	CBResetSec             int     `toml:"cb_reset_sec,omitempty"`
	UpstreamCBThreshold    int     `toml:"upstream_cb_threshold,omitempty"`
	HealthCheckIntervalSec int     `toml:"health_check_interval_sec,omitempty"`
}

// TomlConfig 对应整个 config.toml 文件结构。
type TomlConfig struct {
	Provider map[string]TomlProviderConfig `toml:"provider"`
}

// LoadToml 读取 TOML 配置文件并转换为 Config。
// 文件必须存在且格式合法；格式错误或缺少 [provider] 段返回 error。
func LoadToml(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &ConfigError{
			Category: "system",
			Message:  fmt.Sprintf("系统错误: 读取 TOML 配置文件 %q 失败: %v", path, err),
		}
	}
	var tc TomlConfig
	if err := toml.Unmarshal(data, &tc); err != nil {
		return nil, &ConfigError{
			Category: "config",
			Message:  fmt.Sprintf("配置错误: TOML 解析失败: %v", err),
		}
	}
	if len(tc.Provider) == 0 {
		return nil, &ConfigError{
			Category: "config",
			Message:  "配置错误: TOML 配置缺少 [provider] 段",
		}
	}
	// 取第一个 provider 作为主配置（按名称排序确保确定性）
	names := make([]string, 0, len(tc.Provider))
	for n := range tc.Provider {
		names = append(names, n)
	}
	sort.Strings(names)
	p := tc.Provider[names[0]]
	return tomlToConfig(names[0], &p), nil
}

// SaveToml 将 Config 写入 TOML 文件。覆盖已存在的文件。
func SaveToml(cfg *Config, path string) error {
	tc := configToToml(cfg)
	var buf bytes.Buffer
	encoder := toml.NewEncoder(&buf)
	if err := encoder.Encode(tc); err != nil {
		return fmt.Errorf("TOML 编码失败: %w", err)
	}
	return os.WriteFile(path, buf.Bytes(), 0644)
}

// LoadTomlConfig 读取 TOML 配置文件并返回完整的 TomlConfig（包含所有 provider）。
// 文件不存在时返回原始错误（可通过 os.IsNotExist 检查）。
func LoadTomlConfig(path string) (*TomlConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var tc TomlConfig
	if err := toml.Unmarshal(data, &tc); err != nil {
		return nil, err
	}
	if tc.Provider == nil {
		tc.Provider = make(map[string]TomlProviderConfig)
	}
	return &tc, nil
}

// SaveTomlConfig 将完整 TomlConfig 写入 TOML 文件。覆盖已存在的文件。
func SaveTomlConfig(tc *TomlConfig, path string) error {
	var buf bytes.Buffer
	encoder := toml.NewEncoder(&buf)
	if err := encoder.Encode(tc); err != nil {
		return fmt.Errorf("TOML 编码失败: %w", err)
	}
	return os.WriteFile(path, buf.Bytes(), 0644)
}

// XDGConfigPath 返回平台相关的 XDG 配置路径。
// Windows: %APPDATA%/alvus/config.toml
// Linux/macOS: $XDG_CONFIG_HOME/alvus/config.toml → ~/.config/alvus/config.toml
// $XDG_CONFIG_HOME 环境变量优先于平台默认值（在所有平台上均生效）。
func XDGConfigPath() (string, error) {
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		var err error
		configDir, err = os.UserConfigDir()
		if err != nil {
			return "", fmt.Errorf("获取用户配置目录失败: %w", err)
		}
	}
	return filepath.Join(configDir, "alvus", "config.toml"), nil
}

// DetectConfigSource 按优先级自动检测配置源。
// 优先级: specifiedPath > XDG config.toml > .env
// 返回检测到的路径、是否为 TOML 源、错误信息。
func DetectConfigSource(specifiedPath string) (source string, fromToml bool, err error) {
	if specifiedPath != "" {
		return specifiedPath, strings.HasSuffix(specifiedPath, ".toml"), nil
	}
	// 检查 XDG 配置路径是否存在
	xdgPath, xdgErr := XDGConfigPath()
	if xdgErr == nil {
		if _, statErr := os.Stat(xdgPath); statErr == nil {
			return xdgPath, true, nil
		}
	}
	// 回退到 .env
	return ".env", false, nil
}

// LoadAllTomlProviders 读取 TOML 配置文件中的所有 [provider.*] 段，
// 每个段转换为独立的 *Config 实例，返回 provider 名到 Config 的映射。
// 文件必须存在且格式合法；格式错误或缺少 [provider] 段返回 error。
func LoadAllTomlProviders(path string) (map[string]*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, &ConfigError{
			Category: "system",
			Message:  fmt.Sprintf("系统错误: 读取 TOML 配置文件 %q 失败: %v", path, err),
		}
	}
	var tc TomlConfig
	if err := toml.Unmarshal(data, &tc); err != nil {
		return nil, &ConfigError{
			Category: "config",
			Message:  fmt.Sprintf("配置错误: TOML 解析失败: %v", err),
		}
	}
	if len(tc.Provider) == 0 {
		return nil, &ConfigError{
			Category: "config",
			Message:  "配置错误: TOML 配置缺少 [provider] 段",
		}
	}
	result := make(map[string]*Config, len(tc.Provider))
	for name, p := range tc.Provider {
		cfg := tomlToConfig(name, &p)
		result[name] = cfg
	}
	return result, nil
}

// tomlToConfig 将单 provider 的 TOML 配置转换为 *Config，未指定的字段使用默认值。
func tomlToConfig(name string, tc *TomlProviderConfig) *Config {
	cfg := DefaultConfig()
	cfg.TargetBase = tc.Target
	cfg.GenaiBase = tc.Genai
	if tc.Port > 0 {
		cfg.Port = tc.Port
	}
	if tc.CooldownSec > 0 {
		cfg.CooldownSec = tc.CooldownSec
	}
	if tc.MaxRetries > 0 {
		cfg.MaxRetries = tc.MaxRetries
	}
	if tc.DisableThinking {
		cfg.DisableThinking = true
	}
	if tc.GenaiModel != "" {
		cfg.GenaiModel = tc.GenaiModel
	}
	if tc.LogLevel != "" {
		cfg.LogLevel = tc.LogLevel
	}
	if tc.AdminToken != "" {
		cfg.AdminToken = tc.AdminToken
	}
	if tc.KeysFile != "" {
		cfg.KeysFile = tc.KeysFile
	}
	if tc.BackoffCapSec > 0 {
		cfg.BackoffCapSec = tc.BackoffCapSec
	}
	if tc.BackoffMultiplier > 0 {
		cfg.BackoffMultiplier = tc.BackoffMultiplier
	}
	if tc.CBResetSec > 0 {
		cfg.CBResetSec = tc.CBResetSec
	}
	if tc.UpstreamCBThreshold > 0 {
		cfg.UpstreamCBThreshold = tc.UpstreamCBThreshold
	}
	if tc.HealthCheckIntervalSec > 0 {
		cfg.HealthCheckIntervalSec = tc.HealthCheckIntervalSec
	}
	return cfg
}

// configToToml 将 *Config 转换为 *TomlConfig（用于写入 TOML 文件）。
func configToToml(cfg *Config) *TomlConfig {
	return &TomlConfig{
		Provider: map[string]TomlProviderConfig{
			"default": {
				Target:                 cfg.TargetBase,
				Genai:                  cfg.GenaiBase,
				Port:                   cfg.Port,
				CooldownSec:            cfg.CooldownSec,
				MaxRetries:             cfg.MaxRetries,
				DisableThinking:        cfg.DisableThinking,
				GenaiModel:             cfg.GenaiModel,
				LogLevel:               cfg.LogLevel,
				AdminToken:             cfg.AdminToken,
				KeysFile:               cfg.KeysFile,
				BackoffCapSec:          cfg.BackoffCapSec,
				BackoffMultiplier:      cfg.BackoffMultiplier,
				CBResetSec:             cfg.CBResetSec,
				UpstreamCBThreshold:    cfg.UpstreamCBThreshold,
				HealthCheckIntervalSec: cfg.HealthCheckIntervalSec,
			},
		},
	}
}
