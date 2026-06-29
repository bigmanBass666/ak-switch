// Package server provides the HTTP server, proxy, and management handlers for Alvus.
package server

import (
	"alvus/internal/circuitbreaker"
	"alvus/internal/config"
	"alvus/internal/keypool"
	"alvus/internal/logstore"
	alvusmetrics "alvus/internal/metrics"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ServerState holds all server-wide state: configuration, key pool, HTTP client,
// metrics, circuit breakers, and the request multiplexer.
type ServerState struct {
	mu              sync.RWMutex
	cfg             *config.Config
	pool            *keypool.KeyPool
	mux             *http.ServeMux
	client          *http.Client
	logs            *logstore.LogStore
	startTime       time.Time
	metrics         *alvusmetrics.Metrics
	metricsRegistry *prometheus.Registry
	keyCBs          []*circuitbreaker.KeyCircuitBreaker // per-key circuit breakers
	upCB                *circuitbreaker.UpstreamCircuitBreaker
	lastHealthCheckTime time.Time
	lastHealthCheckOK   bool
	dashboardHTML       string
	keysFile        string // path to keys.json for key persistence
}

// NewServerState creates a fully initialized ServerState, registering all HTTP routes.
func NewServerState(cfg *config.Config, pool *keypool.KeyPool, dashboardHTML string, keysFile string) *ServerState {
	reg, m := alvusmetrics.NewRegistry()

	// Initialize KeyCircuitBreakers (one per key)
	// Apply CB fallback defaults for inline-constructed configs
	backoffCapSec := cfg.BackoffCapSec
	if backoffCapSec <= 0 {
		backoffCapSec = 120
	}
	backoffMult := cfg.BackoffMultiplier
	if backoffMult <= 0 {
		backoffMult = 2
	}
	upstreamThreshold := cfg.UpstreamCBThreshold
	if upstreamThreshold <= 0 {
		upstreamThreshold = 5
	}
	cbResetSec := cfg.CBResetSec
	if cbResetSec <= 0 {
		cbResetSec = 30
	}
	base := time.Duration(cfg.CooldownSec) * time.Second
	cap_ := time.Duration(backoffCapSec) * time.Second
	keyCBs := make([]*circuitbreaker.KeyCircuitBreaker, len(pool.Keys()))
	for i := range keyCBs {
		keyCBs[i] = circuitbreaker.NewKeyCircuitBreaker(base, cap_, backoffMult)
	}

	upCB := circuitbreaker.NewUpstreamCircuitBreaker(
		upstreamThreshold,
		time.Duration(cbResetSec)*time.Second,
	)

	s := &ServerState{
		cfg: cfg, pool: pool, mux: http.NewServeMux(),
		client: &http.Client{
			Timeout: 120 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        100,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		logs:            logstore.New(1000),
		startTime:       time.Now(),
		metrics:         m,
		metricsRegistry: reg,
		keyCBs:          keyCBs,
		upCB:            upCB,
		dashboardHTML:   dashboardHTML,
		keysFile:        keysFile,
	}
	s.mux.HandleFunc("/health", s.healthHandler)
	s.mux.HandleFunc("/logs", s.logsHandler)
	s.mux.HandleFunc("/dashboard", s.dashboardHandler)
	s.mux.HandleFunc("/clear", s.clearHandler)
	s.mux.HandleFunc("/api/config", s.configHandler)
	s.mux.HandleFunc("/api/keys", s.keysHandler)
	s.mux.HandleFunc("POST /api/keys/{index}/disable", s.disableKeyHandler)
	s.mux.HandleFunc("PUT /api/keys/{index}/cooldown", s.cooldownKeyHandler)
	s.mux.HandleFunc("DELETE /api/keys/{index}", s.deleteKeyHandler)
	s.mux.HandleFunc("GET /api/stats", s.statsHandler)
	s.mux.HandleFunc("POST /api/reload", s.reloadHandler)
	s.mux.HandleFunc("/api/log-level", s.logLevelHandler)
	s.mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	// Block service worker requests to prevent 404s and unnecessary upstream proxying
	s.mux.HandleFunc("/sw.js", s.swHandler)
	s.mux.HandleFunc("/", s.proxyHandler)
	return s
}

// Handler returns the HTTP handler (mux) for use by http.Server or httptest.
func (s *ServerState) Handler() http.Handler {
	return s.mux
}

// LastHealthCheck returns the timestamp and result of the most recent active health check.
func (s *ServerState) LastHealthCheck() (time.Time, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.lastHealthCheckTime, s.lastHealthCheckOK
}

// SetLastHealthCheck records the result of an active health check probe.
func (s *ServerState) SetLastHealthCheck(ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastHealthCheckTime = time.Now()
	s.lastHealthCheckOK = ok
}

// Metrics returns the prometheus metrics collector.
func (s *ServerState) Metrics() *alvusmetrics.Metrics {
	return s.metrics
}

// PersistKeys saves the current key pool state to the keys file.
// Called after key mutations through the management API.
func (s *ServerState) PersistKeys() {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()
	if cfg.KeysFile == "" {
		return
	}
	keys := s.pool.Keys()
	entries := make([]keypool.KeyEntry, len(keys))
	for i := range keys {
		entries[i] = keypool.KeyEntry{
			Key:      keys[i],
			Name:     s.pool.Name(i),
			Disabled: s.pool.IsDisabled(i),
		}
	}
	if err := keypool.SaveFullStore(cfg.KeysFile, &keypool.KeyStore{Keys: entries}); err != nil {
		slog.Error("failed to persist keys", "path", cfg.KeysFile, "error", err)
	}
}

// ApplyLogLevel sets the global slog handler's minimum level based on a string.
// Supported values: "debug", "info", "warn", "error".
// Unknown or empty values default to slog.LevelInfo.
func ApplyLogLevel(level string) {
	var lvl slog.Level
	switch strings.ToLower(level) {
	case "debug":
		lvl = slog.LevelDebug
	case "info":
		lvl = slog.LevelInfo
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl})))
}

// MaskSensitiveData scrubs potential API key patterns from a string for safe debug logging.
// It masks any word-like token that starts with "sk-" by replacing it with "***".
// It also truncates the result to maxLen bytes.
func MaskSensitiveData(data string, maxLen int) string {
	if len(data) > maxLen {
		data = data[:maxLen]
	}
	// Mask sk- prefixed tokens (API key patterns)
	result := data
	lower := strings.ToLower(data)
	idx := strings.Index(lower, "sk-")
	for idx >= 0 {
		// Find end of token (word boundary)
		end := idx + 3 // start of actual key after "sk-"
		for end < len(result) && (isAlphaNum(result[end]) || result[end] == '-' || result[end] == '_') {
			end++
		}
		if end > idx+3 {
			result = result[:idx] + "***" + result[end:]
			lower = strings.ToLower(result)
		}
		idx = strings.Index(lower, "sk-")
	}
	return result
}

func isAlphaNum(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
}

// LoadConfig loads configuration from .env and validates it.
func LoadConfig() (*config.Config, *keypool.KeyPool) {
	cfg, err := config.Load(".env")
	if err != nil {
		slog.Error(err.Error())
		var cfgErr *config.ConfigError
		if errors.As(err, &cfgErr) && cfgErr.Category == "system" {
			os.Exit(config.ExitCodeSystem)
		} else {
			os.Exit(config.ExitCodeConfig)
		}
	}
	if err := cfg.Validate(); err != nil {
		slog.Error(err.Error())
		os.Exit(config.ExitCodeConfig)
	}
	// Merge keys from file if KeysFile is configured
	keys := cfg.Keys
	names := cfg.KeyNames
	if cfg.KeysFile != "" {
		fileKeys, fileNames, err := keypool.LoadKeysFromFile(cfg.KeysFile)
		if err != nil {
			slog.Warn("load keys file failed, using env keys", "path", cfg.KeysFile, "error", err)
		} else if fileKeys != nil {
			// File exists - use its keys as the source of truth
			cfg.Keys = fileKeys
			cfg.KeyNames = fileNames
			keys = fileKeys
			names = fileNames
		} else {
			// File not found - create it from env keys
			if err := keypool.SaveKeysToFile(cfg.KeysFile, keys, names); err != nil {
				slog.Warn("create keys file failed", "path", cfg.KeysFile, "error", err)
			}
		}
	}
	slog.Info("config loaded", "keys", len(cfg.Keys), "target", cfg.TargetBase, "genai", cfg.GenaiBase)

	// Set encryption key for key pool persistence
	keypool.SetEncryptionKey(cfg.EncryptionKey)

	return cfg, keypool.NewKeyPool(keys, names)
}

// ReloadConfig reloads configuration from .env after clearing environment variables.
func ReloadConfig() (*config.Config, *keypool.KeyPool, error) {
	for _, k := range []string{
		"API_KEYS", "KEY", "KEY1", "KEY2", "KEY3", "KEY4", "KEY5", "KEYA", "KEYB",
		"TARGET_BASE_URL", "GENAI_BASE_URL", "PORT", "COOLDOWN_SEC", "ADMIN_TOKEN",
		"MAX_RETRIES", "DISABLE_THINKING", "GENAI_MODEL", "LOG_LEVEL",
		"BACKOFF_CAP_SEC", "BACKOFF_MULTIPLIER", "CB_RESET_SEC", "UPSTREAM_CB_THRESHOLD",
			"KEYS_FILE", "KEYS_ENCRYPTION_KEY",
		"HEALTH_CHECK_INTERVAL_SEC", "HEALTH_CHECK_PATH", "HEALTH_CHECK_TIMEOUT_SEC",
	} {
		os.Unsetenv(k)
	}
	cfg, err := config.Load(".env")
	if err != nil {
		return nil, nil, err
	}
	if err := cfg.Validate(); err != nil {
		return nil, nil, fmt.Errorf("reloaded config invalid: %w", err)
	}
	// Merge keys from file if KeysFile is configured
	keys := cfg.Keys
	names := cfg.KeyNames
	if cfg.KeysFile != "" {
		fileKeys, fileNames, err := keypool.LoadKeysFromFile(cfg.KeysFile)
		if err != nil {
			slog.Warn("load keys file failed", "path", cfg.KeysFile, "error", err)
		} else if fileKeys != nil {
			keys = fileKeys
			names = fileNames
		}
		// NOTE: during reload, don't auto-create the file
		// (it should already exist if server has been running)
	}
	cfg.Keys = keys
	cfg.KeyNames = names

	// Sync encryption key for key pool persistence
	keypool.SetEncryptionKey(cfg.EncryptionKey)

	return cfg, keypool.NewKeyPool(keys, names), nil
}
