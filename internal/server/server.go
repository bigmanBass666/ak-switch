// Package server provides the HTTP server, proxy, and management handlers for Alvus.
package server

import (
	"alvus/internal/circuitbreaker"
	"alvus/internal/config"
	"alvus/internal/keypool"
	"alvus/internal/logstore"
	alvusmetrics "alvus/internal/metrics"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// ProxyEngine holds the HTTP client and circuit breakers for upstream proxy requests.
type ProxyEngine struct {
	client *http.Client
	keyCBs []*circuitbreaker.KeyCircuitBreaker // per-key circuit breakers
	upCB   *circuitbreaker.UpstreamCircuitBreaker
}

// NewProxyEngine creates a ProxyEngine from config and key count.
func NewProxyEngine(cfg *config.Config, numKeys int) *ProxyEngine {
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
	keyCBs := make([]*circuitbreaker.KeyCircuitBreaker, numKeys)
	for i := range keyCBs {
		keyCBs[i] = circuitbreaker.NewKeyCircuitBreaker(base, cap_, backoffMult)
	}

	upCB := circuitbreaker.NewUpstreamCircuitBreaker(
		upstreamThreshold,
		time.Duration(cbResetSec)*time.Second,
	)

	return &ProxyEngine{
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
		keyCBs: keyCBs,
		upCB:   upCB,
	}
}

// ServerState holds per-provider runtime state: configuration, key pool, HTTP client,
// metrics, circuit breakers, and the request multiplexer.
type ServerState struct {
	mu                  sync.RWMutex
	name                string
	cfg                 *config.Config
	pool                *keypool.KeyPool
	mux                 *http.ServeMux
	proxy               *ProxyEngine
	logs                *logstore.LogStore
	startTime           time.Time
	metrics             *alvusmetrics.Metrics
	metricsRegistry     *prometheus.Registry
	lastHealthCheckTime time.Time
	lastHealthCheckOK   bool
	dashboardHTML       string
	keysFile            string // path to keys.json for key persistence
}

// NewServerState creates a fully initialized ServerState for a single provider.
func NewServerState(name string, cfg *config.Config, pool *keypool.KeyPool, dashboardHTML string, keysFile string) *ServerState {
	reg, m := alvusmetrics.NewRegistry()

	// Initialize ProxyEngine with HTTP client and circuit breakers
	proxy := NewProxyEngine(cfg, len(pool.Keys()))

	s := &ServerState{
		name:            name,
		cfg:             cfg,
		pool:            pool,
		mux:             http.NewServeMux(),
		proxy:           proxy,
		logs:            logstore.New(10000),
		startTime:       time.Now(),
		metrics:         m,
		metricsRegistry: reg,
		dashboardHTML:   dashboardHTML,
		keysFile:        keysFile,
	}
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
	slog.SetDefault(slog.New(newHandler(os.Stderr, lvl)))
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

// RouteEntry represents a single provider's routing info.
type RouteEntry struct {
	Config *config.Config
	Pool   *keypool.KeyPool
	Proxy  *ProxyEngine
}
