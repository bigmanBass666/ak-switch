package main

import (
	"alvus/internal/circuitbreaker"
	"alvus/internal/config"
	"alvus/internal/keypool"
	"alvus/internal/logstore"
	alvusmetrics "alvus/internal/metrics"
	"alvus/internal/utils"
	"bytes"
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"log/slog"
	"math/rand"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ── Error Handling ─────────────────────────────────

// ErrorCode represents a machine-readable error category for proxy responses.
type ErrorCode string

const (
	ErrorBadRequest       ErrorCode = "BAD_REQUEST"
	ErrorUpstreamError    ErrorCode = "UPSTREAM_ERROR"
	ErrorAllKeysInvalid   ErrorCode = "ALL_KEYS_INVALID"
	ErrorExhaustedRetries ErrorCode = "EXHAUSTED_RETRIES"
)

func loadConfig() (*config.Config, *keypool.KeyPool) {
	cfg, err := config.Load(".env")
	if err != nil {
		slog.Error("config load failed", "error", err)
		log.Fatalf("config load failed: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		slog.Error("config validation failed", "error", err)
		log.Fatalf("config validation failed: %v", err)
	}
	slog.Info("config loaded", "keys", len(cfg.Keys), "target", cfg.TargetBase, "genai", cfg.GenaiBase)
	return cfg, keypool.NewKeyPool(cfg.Keys, cfg.KeyNames)
}

func reloadConfig() (*config.Config, *keypool.KeyPool, error) {
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

// ── Server ────────────────────────────────────

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
}

func newServerState(cfg *config.Config, pool *keypool.KeyPool) *ServerState {
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

func (s *ServerState) swHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

// writeProxyError writes a JSON error response with the given status code and error code.
func writeProxyError(w http.ResponseWriter, status int, code ErrorCode, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]interface{}{
			"code":    string(code),
			"message": message,
		},
	})
}

type ConfigPayload struct {
	TargetBase string   `json:"targetBase"`
	GenaiBase  string   `json:"genaiBase"`
	Keys       []string `json:"keys"`
}

func (s *ServerState) configHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := s.cfg
	pool := s.pool
	s.mu.RUnlock()

	if r.Method == http.MethodGet {
		keys := pool.Keys()

		maskedKeys := make([]string, len(keys))
		for i, k := range keys {
			maskedKeys[i] = utils.MaskKey(k)
		}
		s.respondJSON(w, http.StatusOK, ConfigPayload{
			TargetBase: cfg.TargetBase,
			GenaiBase:  cfg.GenaiBase,
			Keys:       maskedKeys,
		})
		return
	}

	if r.Method == http.MethodPost {
		if cfg.AdminToken != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Admin-Token")), []byte(cfg.AdminToken)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var payload ConfigPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		payload.TargetBase = strings.TrimSpace(payload.TargetBase)
		payload.GenaiBase = strings.TrimSpace(payload.GenaiBase)

		s.mu.RLock()
		pool := s.pool
		s.mu.RUnlock()

		currentKeys := pool.Keys()

		reclaimed := make(map[int]bool)
		for i := range payload.Keys {
			k := strings.TrimSpace(payload.Keys[i])
			if k == "" {
				time.Sleep(10 * time.Millisecond)
				time.Sleep(10 * time.Millisecond)
			continue
			}
			// If the key is masked (contains "..." or is "****"), try to restore it from the current pool
			if strings.Contains(k, "...") || k == "****" {
				for j, ck := range currentKeys {
					if !reclaimed[j] && utils.MaskKey(ck) == k {
						k = ck
						reclaimed[j] = true
						break
					}
				}
			}
			payload.Keys[i] = k
		}
		payload.Keys = filterEmpty(payload.Keys)

		if payload.TargetBase == "" {
			http.Error(w, "targetBase is required", http.StatusBadRequest)
			return
		}
		if payload.GenaiBase == "" {
			http.Error(w, "genaiBase is required", http.StatusBadRequest)
			return
		}
		if len(payload.Keys) == 0 {
			http.Error(w, "at least one API key is required", http.StatusBadRequest)
			return
		}

		envLines := []string{
			fmt.Sprintf("TARGET_BASE_URL=%s", payload.TargetBase),
			fmt.Sprintf("GENAI_BASE_URL=%s", payload.GenaiBase),
			fmt.Sprintf("API_KEYS=%s", strings.Join(payload.Keys, ",")),
			fmt.Sprintf("PORT=%d", cfg.Port),
			fmt.Sprintf("COOLDOWN_SEC=%d", cfg.CooldownSec),
			fmt.Sprintf("MAX_RETRIES=%d", cfg.MaxRetries),
		}

		if err := os.WriteFile(".env", []byte(strings.Join(envLines, "\n")), 0600); err != nil {
			slog.Error("failed to write env", "error", err)
			http.Error(w, "failed to save config", http.StatusInternalServerError)
			return
		}

		slog.Info("config updated via api")

		s.mu.RLock()
		oldCfg := s.cfg
		s.mu.RUnlock()

		newCfg, newPool, err := reloadConfig()
		if err != nil {
			slog.Warn("reload failed", "error", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{"status": "accepted", "warning": "config saved but reload failed: " + err.Error()})
			return
		}

		changes := oldCfg.Diff(newCfg)
		for _, c := range changes {
			slog.Info("config changed via api", "field", c.Field, "old", c.OldValue, "new", c.NewValue)
		}

		s.mu.Lock()
		s.cfg = newCfg
		s.pool = newPool
		s.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "reloaded"})
		return
	}

	w.WriteHeader(http.StatusMethodNotAllowed)
}

func (s *ServerState) keysHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	pool := s.pool
	cfg := s.cfg
	s.mu.RUnlock()

	// Admin token check for POST and DELETE
	if (r.Method == http.MethodPost || r.Method == http.MethodDelete) && cfg.AdminToken != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Admin-Token")), []byte(cfg.AdminToken)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	switch r.Method {
	case http.MethodGet:
		keys := pool.Keys()
		now := time.Now()
		result := make([]map[string]interface{}, len(keys))
		for i := range keys {
			pool.CleanupOldRequests(i)
			result[i] = map[string]interface{}{
				"index":       i + 1,
				"key":         utils.MaskKey(keys[i]),
				"status":      pool.KeyStatusLabel(i, now),
				"requests_1m": pool.RequestsInLastMinute(i),
				"name":        pool.Name(i),
			}
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)

	case http.MethodPost:
		var body struct {
			Key string `json:"key"`
				KeyName string `json:"name"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if body.Key == "" {
			http.Error(w, "key is required", http.StatusBadRequest)
			return
		}
		idx := pool.AddKey(body.Key, body.KeyName)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"index": idx,
			"key":   utils.MaskKey(body.Key),
				"name":  body.KeyName,
		})

	case http.MethodDelete:
		var body struct {
			Index int `json:"index"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if err := pool.RemoveKey(body.Index); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "removed"})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func filterEmpty(ss []string) []string {
	filtered := make([]string, 0, len(ss))
	for _, s := range ss {
		if s != "" {
			filtered = append(filtered, s)
		}
	}
	return filtered
}

func (s *ServerState) respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func (s *ServerState) healthHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := s.cfg
	pool := s.pool
	s.mu.RUnlock()

	if cfg.AdminToken != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Admin-Token")), []byte(cfg.AdminToken)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	details := pool.GetKeyDetails()
	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"status":  "ok",
		"keys":    len(details),
		"details": details,
	})
}

func (s *ServerState) proxyHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := s.cfg
	pool := s.pool
	client := s.client
	keyCBs := s.keyCBs
	upCB := s.upCB
	s.mu.RUnlock()

	start := time.Now()
	recordMetrics := func(statusClass, keyIndex string) {
		s.metrics.RequestsTotal.WithLabelValues(r.Method, statusClass, keyIndex).Inc()
		s.metrics.RequestDuration.WithLabelValues(r.Method, statusClass).Observe(time.Since(start).Seconds())
	}

	var bodyBytes []byte
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10MB limit
		var err error
		bodyBytes, err = io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil {
			writeProxyError(w, http.StatusBadRequest, ErrorBadRequest, "request body too large or unreadable")
			recordMetrics("4xx", "")
			return
		}
	}

	// Route /genai/ paths to GenaiBase, everything else to TargetBase
	var target string
	if strings.Contains(r.URL.Path, "/genai/") {
		target = cfg.GenaiBase + r.URL.Path
		if r.URL.RawQuery != "" {
			target += "?" + r.URL.RawQuery
		}
	} else {
		path := r.URL.Path
		if strings.HasSuffix(cfg.TargetBase, "/v1") && strings.HasPrefix(path, "/v1") {
			path = path[3:]
		}
		if r.URL.RawQuery != "" {
			path += "?" + r.URL.RawQuery
		}
		target = cfg.TargetBase + path
	}

	slog.Info("proxy request", "method", r.Method, "url", target, "bytes", len(bodyBytes))

	for attempt := 0; attempt < cfg.MaxRetries; attempt++ {
		// 1. Check upstream circuit breaker (fail fast)
		if !upCB.Allow() {
			slog.Warn("upstream circuit breaker open, backing off", "attempt", attempt+1, "max", cfg.MaxRetries)
			time.Sleep(time.Second)
			continue
		}

		// 2. Get available key from pool
		idx, key, ok := pool.Next()
		if !ok {
			wait := pool.TimeUntilAvailable()
			jitter := time.Duration(rand.Intn(500)) * time.Millisecond
			slog.Warn("all keys cooling", "wait", (wait + jitter).Round(time.Second), "attempt", attempt+1, "max", cfg.MaxRetries)
			time.Sleep(wait + jitter)
			continue
		}

		// 3. Check key-level circuit breaker
		if !keyCBs[idx].Allow() {
			remaining := keyCBs[idx].CooldownRemaining()
			if remaining < 0 {
				// Key is permanently disabled but pool returned it.
				// Check if ALL keys are permanently disabled.
				allPerma := true
				for _, cb := range keyCBs {
					if cb.State() != circuitbreaker.StatePermanent {
						allPerma = false
						break
					}
				}
				if allPerma {
					writeProxyError(w, http.StatusServiceUnavailable, ErrorAllKeysInvalid, "all keys quota exhausted")
					recordMetrics("5xx", "")
					return
				}
				// Skip to next key
				continue
			}
			if remaining > 0 {
				time.Sleep(remaining)
			} else {
				time.Sleep(10 * time.Millisecond)
			}
			continue
		}

		req, err := http.NewRequestWithContext(r.Context(), r.Method, target, bytes.NewReader(bodyBytes))
		if err != nil {
			s.metrics.UpstreamErrors.WithLabelValues("network").Inc()
			writeProxyError(w, http.StatusInternalServerError, ErrorUpstreamError, "failed to build upstream request")
			recordMetrics("5xx", "")
			return
		}
		utils.CopyHeaders(req.Header, r.Header)
		req.Header.Set("Authorization", "Bearer "+key)

		resp, err := client.Do(req)
		if err != nil {
			slog.Warn("key network error", "key_index", idx, "key_name", pool.Name(idx), "error", err)
			s.metrics.UpstreamErrors.WithLabelValues("network").Inc()
			upCB.RecordFailure()
			continue
		}

		switch resp.StatusCode {
		case http.StatusTooManyRequests:
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			keyCBs[idx].RecordFailure()
			cooldown := time.Duration(cfg.CooldownSec) * time.Second
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if secs, err := strconv.Atoi(ra); err == nil {
					cooldown = time.Duration(secs+2) * time.Second
				}
			}
			pool.Cooldown(idx, cooldown)
			slog.Warn("key rate limited", "key_index", idx, "key_name", pool.Name(idx), "status", resp.StatusCode, "cb_state", fmt.Sprintf("%d", keyCBs[idx].State()), "cb_attempt", keyCBs[idx].Attempt(), "body", string(body))
			s.metrics.UpstreamErrors.WithLabelValues("rate_limited").Inc()
			if keyCBs[idx].State() == circuitbreaker.StatePermanent {
				slog.Warn("key quota exhausted, disabling permanently", "key_index", idx, "key_name", pool.Name(idx))
				pool.Disable(idx)
				if pool.ActiveCount() == 0 {
					writeProxyError(w, http.StatusServiceUnavailable, ErrorAllKeysInvalid, "all keys quota exhausted")
					recordMetrics("5xx", "")
					return
				}
			}
			continue

		case http.StatusBadGateway, http.StatusServiceUnavailable:
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			slog.Warn("upstream server error", "key_index", idx, "key_name", pool.Name(idx), "status", resp.StatusCode, "body", string(body))
			s.metrics.UpstreamErrors.WithLabelValues("server_error").Inc()
			upCB.RecordFailure() // upstream error, not key fault
			continue

		case http.StatusUnauthorized, http.StatusForbidden:
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			slog.Warn("key disabled", "key_index", idx, "key_name", pool.Name(idx), "status", resp.StatusCode, "body", string(body))
			s.metrics.UpstreamErrors.WithLabelValues("auth_rejected").Inc()
			pool.Disable(idx)
			keyCBs[idx].RecordPerma("auth_rejected")
			if pool.ActiveCount() == 0 {
				writeProxyError(w, http.StatusServiceUnavailable, ErrorAllKeysInvalid, "all keys are invalid or revoked")
				recordMetrics("5xx", "")
				return
			}
			continue
		}

		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			utils.CopyHeaders(w.Header(), resp.Header)
			w.WriteHeader(resp.StatusCode)
			io.Copy(w, resp.Body)
			resp.Body.Close()

			s.logs.Append(utils.LogEntry{Timestamp: time.Now().Format(time.RFC3339), Key: key, KeyIndex: idx + 1, KeyName: pool.Name(idx), Method: r.Method, URL: target, Status: resp.StatusCode, RequestBodySize: len(bodyBytes)})
			slog.Warn("terminal client error", "method", r.Method, "url", target, "status", resp.StatusCode)
			recordMetrics("4xx", fmt.Sprintf("%d", idx))
			return
		}

		if resp.StatusCode >= 500 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			slog.Warn("upstream error, retrying", "status", resp.StatusCode, "body", string(body))
			s.metrics.UpstreamErrors.WithLabelValues("server_error").Inc()
			upCB.RecordFailure()
			continue
		}

		// Success (2xx/3xx)
		keyCBs[idx].RecordSuccess()
		upCB.RecordSuccess()

		utils.CopyHeaders(w.Header(), resp.Header)
		w.WriteHeader(resp.StatusCode)

		if f, ok := w.(http.Flusher); ok {
			buf := make([]byte, 4096)
			for {
				n, rerr := resp.Body.Read(buf)
				if n > 0 {
					if _, werr := w.Write(buf[:n]); werr != nil {
						break
					}
					f.Flush()
				}
				if rerr != nil {
					break
				}
			}
		} else {
			io.Copy(w, resp.Body)
		}
		resp.Body.Close()

		pool.IncrementRequestCount(idx)
		s.logs.Append(utils.LogEntry{Timestamp: time.Now().Format(time.RFC3339), Key: key, KeyIndex: idx + 1, KeyName: pool.Name(idx), Method: r.Method, URL: target, Status: resp.StatusCode, RequestBodySize: len(bodyBytes)})
		slog.Info("proxy success", "method", r.Method, "url", target, "status", resp.StatusCode, "key_index", idx, "key_name", pool.Name(idx), "attempt", attempt+1)
		recordMetrics(alvusmetrics.StatusLabel(resp.StatusCode), fmt.Sprintf("%d", idx))
		return
	}

	writeProxyError(w, http.StatusServiceUnavailable, ErrorExhaustedRetries, "exhausted all retries")
	recordMetrics("5xx", "")
}

func (s *ServerState) logsHandler(w http.ResponseWriter, r *http.Request) {
	entries := s.logs.Snapshot()
	s.respondJSON(w, http.StatusOK, entries)
}

func (s *ServerState) dashboardHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(dashboardHTML))
}

//go:embed dashboard.html
var dashboardHTML string

func (s *ServerState) clearHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()

	if cfg.AdminToken != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Admin-Token")), []byte(cfg.AdminToken)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	s.logs.Clear()
	s.respondJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}

// ── Management API Handlers ─────────────────────

func (s *ServerState) adminAuth(cfg *config.Config, w http.ResponseWriter, r *http.Request) bool {
	if cfg.AdminToken != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Admin-Token")), []byte(cfg.AdminToken)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

func (s *ServerState) parseKeyIndex(r *http.Request) (int, bool) {
	raw := r.PathValue("index")
	idx, err := strconv.Atoi(raw)
	if err != nil || idx < 1 {
		return 0, false
	}
	return idx - 1, true // convert to 0-based
}

func (s *ServerState) disableKeyHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := s.cfg
	pool := s.pool
	s.mu.RUnlock()

	if !s.adminAuth(cfg, w, r) {
		return
	}

	idx, ok := s.parseKeyIndex(r)
	if !ok || idx >= len(pool.Keys()) {
		s.respondJSON(w, http.StatusNotFound, map[string]string{"error": "key not found"})
		return
	}

	if err := pool.Disable(idx); err != nil {s.respondJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()});return};s.respondJSON(w, http.StatusOK, map[string]bool{"success": true})
	s.respondJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *ServerState) cooldownKeyHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := s.cfg
	pool := s.pool
	s.mu.RUnlock()

	if !s.adminAuth(cfg, w, r) {
		return
	}

	idx, ok := s.parseKeyIndex(r)
	if !ok || idx >= len(pool.Keys()) {
		s.respondJSON(w, http.StatusNotFound, map[string]string{"error": "key not found"})
		return
	}

	if err := pool.Cooldown(idx, time.Duration(cfg.CooldownSec)*time.Second); err != nil {
		s.respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	s.respondJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *ServerState) deleteKeyHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := s.cfg
	pool := s.pool
	s.mu.RUnlock()

	if !s.adminAuth(cfg, w, r) {
		return
	}

	idx, ok := s.parseKeyIndex(r)
	if !ok || idx >= len(pool.Keys()) {
		s.respondJSON(w, http.StatusNotFound, map[string]string{"error": "key not found"})
		return
	}

	pool.RemoveKey(idx)
	s.respondJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (s *ServerState) statsHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	pool := s.pool
	s.mu.RUnlock()

	total := s.logs.Len()
	entries := s.logs.Snapshot()

	successful := 0
	failed := 0
	for _, e := range entries {
		if e.Status < 400 {
			successful++
		} else {
			failed++
		}
	}

	var successRate float64
	if total > 0 {
		successRate = float64(successful) / float64(total) * 100
	}

	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"total_requests":     total,
		"successful_requests": successful,
		"failed_requests":    failed,
		"success_rate":       fmt.Sprintf("%.2f", successRate),
		"active_keys":        pool.ActiveCount(),
		"cooling_keys":       pool.CoolingCount(),
		"disabled_keys":      pool.DisabledCount(),
		"uptime_seconds":     time.Since(s.startTime).Seconds(),
	})
}

func (s *ServerState) reloadHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()

	if !s.adminAuth(cfg, w, r) {
		return
	}

	s.mu.RLock()
	oldCfg := s.cfg
	s.mu.RUnlock()

	newCfg, newPool, err := reloadConfig()
	if err != nil {
		slog.Warn("reload failed", "error", err)
		s.respondJSON(w, http.StatusOK, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	changes := oldCfg.Diff(newCfg)
	for _, c := range changes {
		slog.Info("config changed via api reload", "field", c.Field, "old", c.OldValue, "new", c.NewValue)
	}

	s.mu.Lock()
	s.cfg = newCfg
	s.pool = newPool
	s.mu.Unlock()

	s.respondJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// ── .env Watcher ──────────────────────────────

func watchEnvFile(state *ServerState, stop <-chan struct{}) {
	var lastMod time.Time
	if info, err := os.Stat(".env"); err == nil {
		lastMod = info.ModTime()
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			info, err := os.Stat(".env")
			if err != nil {
				if os.IsNotExist(err) {
					slog.Info("env deleted, keeping current config")
				}
				time.Sleep(10 * time.Millisecond)
				continue
			}
			if !info.ModTime().After(lastMod) {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			lastMod = info.ModTime()
			time.Sleep(100 * time.Millisecond) // debounce

			slog.Info("env changed, reloading")

			state.mu.RLock()
			oldCfg := state.cfg
			state.mu.RUnlock()

			newCfg, newPool, err := reloadConfig()
			if err != nil {
				slog.Error("env reload failed; keeping previous config", "error", err)
				time.Sleep(10 * time.Millisecond)
				continue
			}

			// Log configuration changes (sensitive fields masked)
			changes := oldCfg.Diff(newCfg)
			if len(changes) > 0 {
				for _, c := range changes {
					slog.Info("config changed", "field", c.Field, "old", c.OldValue, "new", c.NewValue)
				}
			}

			state.mu.Lock()
			state.cfg = newCfg
			state.pool = newPool
			state.mu.Unlock()

			slog.Info("config reloaded", "keys", len(newPool.Keys()), "target", newCfg.TargetBase, "genai", newCfg.GenaiBase)
		}
	}
}

// refreshKeyPoolMetrics periodically updates the keypool gauge metrics.
func refreshKeyPoolMetrics(state *ServerState, stop <-chan struct{}) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			state.mu.RLock()
			pool := state.pool
			state.mu.RUnlock()
			state.metrics.RefreshKeyPoolGauge(pool)
		}
	}
}

// ── Main ──────────────────────────────────────

func main() {
	isLocal := flag.Bool("local", false, "Bind to 127.0.0.1 (local access only)")
	isNetwork := flag.Bool("network-only", false, "Bind to 0.0.0.0 (accessible via LAN)")
	managePath := flag.String("manage", "", "Path to manage.json for multi-instance mode")
	processTag := flag.String("tag", "", "Process identity tag (empty = production)")
	flag.Parse()

	// Shared stop channel for graceful shutdown
	stop := make(chan struct{})
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("shutting down")
		close(stop)
	}()

	// ── Manage Mode ────────────────────────────
	if *managePath != "" {
		runManager(*managePath, *processTag, stop)
		return
	}

	// ── Single Instance Mode (original) ────────
	host := "" // Default (binds to all interfaces)
	if *isLocal {
		host = "127.0.0.1"
	} else if *isNetwork {
		host = "0.0.0.0"
	}

	cfg, pool := loadConfig()
	state := newServerState(cfg, pool)

	// Initial key pool metric refresh
	state.metrics.RefreshKeyPoolGauge(state.pool)

	go watchEnvFile(state, stop)
	go refreshKeyPoolMetrics(state, stop)

	addr := fmt.Sprintf("%s:%d", host, cfg.Port)

	// Check port availability and bind
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		slog.Error("port in use", "port", cfg.Port, "error", err)
		log.Fatalf("port %d is already in use: %v", cfg.Port, err)
	}

	server := &http.Server{Handler: state.mux}

	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			slog.Error("shutdown error", "error", err)
		}
	}()

	displayHost := host
	if displayHost == "" {
		displayHost = "0.0.0.0"
	}
	if *processTag != "" {
		slog.Info("starting", "tag", *processTag, "port", cfg.Port, "keys", len(pool.Keys()), "target", cfg.TargetBase, "genai", cfg.GenaiBase)
	} else {
		slog.Info("starting", "port", cfg.Port, "keys", len(pool.Keys()), "target", cfg.TargetBase, "genai", cfg.GenaiBase)
	}
	if err := server.Serve(listener); err != http.ErrServerClosed {
		slog.Error("server error", "error", err)
		log.Fatalf("❌ Server error: %v", err)
	}
}
