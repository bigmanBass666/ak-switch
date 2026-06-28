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
	upCB            *circuitbreaker.UpstreamCircuitBreaker
	dashboardHTML   string
}

// NewServerState creates a fully initialized ServerState, registering all HTTP routes.
func NewServerState(cfg *config.Config, pool *keypool.KeyPool, dashboardHTML string) *ServerState {
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

// Metrics returns the prometheus metrics collector.
func (s *ServerState) Metrics() *alvusmetrics.Metrics {
	return s.metrics
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
	slog.Info("config loaded", "keys", len(cfg.Keys), "target", cfg.TargetBase, "genai", cfg.GenaiBase)
	return cfg, keypool.NewKeyPool(cfg.Keys, cfg.KeyNames)
}

// ReloadConfig reloads configuration from .env after clearing environment variables.
func ReloadConfig() (*config.Config, *keypool.KeyPool, error) {
	for _, k := range []string{
		"API_KEYS", "KEY", "KEY1", "KEY2", "KEY3", "KEY4", "KEY5", "KEYA", "KEYB",
		"TARGET_BASE_URL", "GENAI_BASE_URL", "PORT", "COOLDOWN_SEC", "ADMIN_TOKEN",
		"MAX_RETRIES", "DISABLE_THINKING", "GENAI_MODEL", "LOG_LEVEL",
		"BACKOFF_CAP_SEC", "BACKOFF_MULTIPLIER", "CB_RESET_SEC", "UPSTREAM_CB_THRESHOLD",
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
	return cfg, keypool.NewKeyPool(cfg.Keys, cfg.KeyNames), nil
}
