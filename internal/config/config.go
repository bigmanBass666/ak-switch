// Package config provides centralized configuration management for AK Switch.
//
// It reads from TOML configuration files, validates required fields,
// and supports runtime diffing for hot-reload scenarios.
package config

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"akswitch/internal/utils"
	"github.com/pelletier/go-toml/v2"
)

// Config holds all application configuration.
// Use LoadAllTomlProviders() to create Config slices from TOML, then Validate()
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

	LogFile    string // 日志文件路径（空 = 不启用文件日志）
	LogMaxSize int    // 日志文件轮转大小（MB，默认 100）
	LogMaxAge  int    // 日志文件保留天数（默认 7）
}


// ConfigError carries a category tag for error classification.
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
		LogMaxSize:          100,
		LogMaxAge:           7,
	}
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
		return &ConfigError{Category: "config", Message: "配置错误: 至少需要一个 API Key（请通过 akswitch key add 添加）"}
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
		{"LOG_FILE", func(c, o *Config) bool { return c.LogFile == o.LogFile }, func(c *Config) string { return c.LogFile }},
		{"LOG_MAX_SIZE", func(c, o *Config) bool { return c.LogMaxSize == o.LogMaxSize }, func(c *Config) string { return strconv.Itoa(c.LogMaxSize) }},
		{"LOG_MAX_AGE", func(c, o *Config) bool { return c.LogMaxAge == o.LogMaxAge }, func(c *Config) string { return strconv.Itoa(c.LogMaxAge) }},
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


func encKeyState(key []byte) string {
	if len(key) == 0 {
		return "unset"
	}
	return "set (32 bytes)"
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



// ---- TOML 配置支持 ----

// TomlProviderConfig 对应 TOML 中单个 [provider.*] 的配置。
type TomlProviderConfig struct {
	Target                 string  `toml:"target"`
	Genai                  string  `toml:"genai,omitempty"`
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
		LogFile    string `toml:"log_file,omitempty"`
		LogMaxSize int    `toml:"log_max_size,omitempty"`
		LogMaxAge  int    `toml:"log_max_age,omitempty"`
}

// TomlConfig 对应整个 config.toml 文件结构。
type TomlConfig struct {
	Port       int                            `toml:"port,omitempty"`
	LogFile    string                         `toml:"log_file,omitempty"`
	LogMaxSize int                            `toml:"log_max_size,omitempty"`
	LogMaxAge  int                            `toml:"log_max_age,omitempty"`
	Provider   map[string]TomlProviderConfig `toml:"provider"`
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
	port := tc.Port
	if port == 0 {
		port = DefaultConfig().Port
	}
	return tomlToConfig(names[0], &p, port), nil
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
// Windows: %APPDATA%/akswitch/config.toml
// Linux/macOS: $XDG_CONFIG_HOME/akswitch/config.toml → ~/.config/akswitch/config.toml
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
	return filepath.Join(configDir, "akswitch", "config.toml"), nil
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
	port := tc.Port
	if port == 0 {
		port = DefaultConfig().Port
	}
	for name, p := range tc.Provider {
		cfg := tomlToConfig(name, &p, port)
		// Top-level log fields override per-provider log fields
		if tc.LogFile != "" {
			cfg.LogFile = tc.LogFile
		}
		if tc.LogMaxSize > 0 {
			cfg.LogMaxSize = tc.LogMaxSize
		}
		if tc.LogMaxAge > 0 {
			cfg.LogMaxAge = tc.LogMaxAge
		}
		result[name] = cfg
	}
	return result, nil
}

// tomlToConfig 将单 provider 的 TOML 配置转换为 *Config，未指定的字段使用默认值。
func tomlToConfig(name string, tc *TomlProviderConfig, port int) *Config {
	cfg := DefaultConfig()
	cfg.TargetBase = tc.Target
	cfg.GenaiBase = tc.Genai
	if port > 0 {
		cfg.Port = port
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
	if tc.LogFile != "" {
		cfg.LogFile = tc.LogFile
	}
	if tc.LogMaxSize > 0 {
		cfg.LogMaxSize = tc.LogMaxSize
	}
	if tc.LogMaxAge > 0 {
		cfg.LogMaxAge = tc.LogMaxAge
	}
	return cfg
}

// configToToml 将 *Config 转换为 *TomlConfig（用于写入 TOML 文件）。
func configToToml(cfg *Config) *TomlConfig {
	return &TomlConfig{
		Port:       cfg.Port,
		LogFile:    cfg.LogFile,
		LogMaxSize: cfg.LogMaxSize,
		LogMaxAge:  cfg.LogMaxAge,
		Provider: map[string]TomlProviderConfig{
			"default": {
				Target:                 cfg.TargetBase,
				Genai:                  cfg.GenaiBase,
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

// FindServerPort finds the first non-zero port from TOML providers.
// Returns 0 if no port is configured or if the TOML file cannot be loaded.
func FindServerPort(xdgPath string) int {
	providers, err := LoadAllTomlProviders(xdgPath)
	if err != nil {
		return 0
	}
	for _, cfg := range providers {
		if cfg.Port > 0 {
			return cfg.Port
		}
	}
	return 0
}