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
)

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

	BackoffCapSec      int     // Key 退避上限(秒)，达到此值自动禁用 (default 120)
	BackoffMultiplier  float64 // 指数退避倍数 (default 2)
	CBResetSec         int     // 上游熔断器 OPEN→HALF_OPEN 超时(秒) (default 30)
	UpstreamCBThreshold int    // 上游熔断器连续失败触发阈值 (default 5)
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
func Load(envPath string) (*Config, error) {
	if envPath != "" {
		if err := loadDotEnv(envPath); err != nil {
			return nil, fmt.Errorf("load .env from %q: %w", envPath, err)
		}
	}

	cfg := DefaultConfig()

	// Port
	if v := os.Getenv("PORT"); v != "" {
		port, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid PORT %q: %w", v, err)
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
			return nil, fmt.Errorf("invalid MAX_RETRIES %q: %w", v, err)
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
			return nil, fmt.Errorf("invalid COOLDOWN_SEC %q: %w", v, err)
		}
		cfg.CooldownSec = cooldown
	}

	// BackoffCapSec
	if v := os.Getenv("BACKOFF_CAP_SEC"); v != "" {
		capSec, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid BACKOFF_CAP_SEC %q: %w", v, err)
		}
		cfg.BackoffCapSec = capSec
	}

	// BackoffMultiplier
	if v := os.Getenv("BACKOFF_MULTIPLIER"); v != "" {
		mult, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return nil, fmt.Errorf("invalid BACKOFF_MULTIPLIER %q: %w", v, err)
		}
		cfg.BackoffMultiplier = mult
	}

	// CBResetSec
	if v := os.Getenv("CB_RESET_SEC"); v != "" {
		resetSec, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid CB_RESET_SEC %q: %w", v, err)
		}
		cfg.CBResetSec = resetSec
	}

	// UpstreamCBThreshold
	if v := os.Getenv("UPSTREAM_CB_THRESHOLD"); v != "" {
		threshold, err := strconv.Atoi(v)
		if err != nil {
			return nil, fmt.Errorf("invalid UPSTREAM_CB_THRESHOLD %q: %w", v, err)
		}
		cfg.UpstreamCBThreshold = threshold
	}

	// Keys: API_KEYS is primary, then fallback to KEY, KEY1-KEY5, KEYA, KEYB
	keys, names := parseKeys()
	if len(keys) == 0 {
		return nil, fmt.Errorf("no API keys found: set API_KEYS, KEY, KEY1-5, or KEYA/KEYB in environment or .env file")
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
		return fmt.Errorf("PORT must be between 1 and 65535, got %d", c.Port)
	}
	if c.TargetBase == "" {
		return fmt.Errorf("TARGET_BASE_URL is required")
	}
	if c.GenaiBase == "" {
		return fmt.Errorf("GENAI_BASE_URL is required")
	}
	if len(c.Keys) == 0 {
		return fmt.Errorf("at least one API key is required (set API_KEYS)")
	}
	if c.BackoffCapSec < 30 {
		return fmt.Errorf("BACKOFF_CAP_SEC must be at least 30, got %d", c.BackoffCapSec)
	}
	if c.BackoffMultiplier < 1 {
		return fmt.Errorf("BACKOFF_MULTIPLIER must be at least 1, got %f", c.BackoffMultiplier)
	}
	if c.CBResetSec < 5 {
		return fmt.Errorf("CB_RESET_SEC must be at least 5, got %d", c.CBResetSec)
	}
	if c.UpstreamCBThreshold < 2 {
		return fmt.Errorf("UPSTREAM_CB_THRESHOLD must be at least 2, got %d", c.UpstreamCBThreshold)
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
func loadDotEnv(filename string) error {
	data, err := os.ReadFile(filename)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // missing .env is not an error here
		}
		return fmt.Errorf("read %q: %w", filename, err)
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
