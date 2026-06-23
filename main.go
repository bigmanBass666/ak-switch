package main

import (
	"bytes"
	"context"
	"crypto/subtle"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

func maskKey(key string) string {
	if len(key) <= 12 {
		return "****"
	}
	return key[:8] + "..." + key[len(key)-4:]
}

func copyHeaders(dst, src http.Header) {
	for k, vals := range src {
		for _, v := range vals {
			dst.Add(k, v)
		}
	}
}

func appendUsageLog(entry LogEntry) {
	usageMu.Lock()
	usageLogs = append(usageLogs, entry)
	if len(usageLogs) > 1000 {
		usageLogs = usageLogs[1:]
	}
	usageMu.Unlock()
}

type LogEntry struct {
	Timestamp       string `json:"timestamp"`
	Key             string `json:"key"`
	KeyIndex        int    `json:"key_index"`
	Method          string `json:"method"`
	URL             string `json:"url"`
	Status          int    `json:"status"`
	RequestBodySize int    `json:"request_body_size"`
}

var (
	usageLogs = []LogEntry{}
	usageMu   sync.Mutex
)

// ── Key Pool ──────────────────────────────────

type KeyPool struct {
	counter        uint64
	keys           []string
	cooldowns      []time.Time
	disabled       []bool
	requestHistory [][]time.Time // timestamps of requests in the last 60s per key
	lastUsed       []time.Time
	mu             sync.Mutex
}

func NewKeyPool(keys []string) *KeyPool {
	return &KeyPool{
		keys:           keys,
		cooldowns:      make([]time.Time, len(keys)),
		disabled:       make([]bool, len(keys)),
		requestHistory: make([][]time.Time, len(keys)),
		lastUsed:       make([]time.Time, len(keys)),
	}
}

func (p *KeyPool) TimeUntilAvailable() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	var soonest time.Duration = -1
	for i, cd := range p.cooldowns {
		if p.disabled[i] {
			continue
		}
		if now.After(cd) {
			return 0
		}
		if wait := cd.Sub(now); soonest < 0 || wait < soonest {
			soonest = wait
		}
	}
	return soonest
}

func (p *KeyPool) Next() (int, string, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := len(p.keys)
	start := int(atomic.AddUint64(&p.counter, 1)-1) % n
	for i := 0; i < n; i++ {
		idx := (start + i) % n
		if !p.disabled[idx] && time.Now().After(p.cooldowns[idx]) {
			return idx, p.keys[idx], true
		}
	}
	return -1, "", false
}

// requestsInLastMinute returns the number of requests made by a key in the last 60 seconds
func (p *KeyPool) requestsInLastMinute(idx int) int {
	cutoff := time.Now().Add(-60 * time.Second)
	count := 0
	for _, t := range p.requestHistory[idx] {
		if t.After(cutoff) {
			count++
		}
	}
	return count
}

// cleanupOldRequests removes request timestamps older than 60 seconds
func (p *KeyPool) cleanupOldRequests(idx int) {
	cutoff := time.Now().Add(-60 * time.Second)
	var filtered []time.Time
	for _, t := range p.requestHistory[idx] {
		if t.After(cutoff) {
			filtered = append(filtered, t)
		}
	}
	p.requestHistory[idx] = filtered
}

func (p *KeyPool) Cooldown(idx int, d time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if until := time.Now().Add(d); p.cooldowns[idx].Before(until) {
		p.cooldowns[idx] = until
	}
	log.Printf("🧊 Key [%d] on cooldown for %s", idx, d)
}

func (p *KeyPool) Disable(idx int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.disabled[idx] = true
}

func (p *KeyPool) ActiveCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for i := range p.keys {
		if !p.disabled[i] {
			n++
		}
	}
	return n
}

func (p *KeyPool) keyStatusLabel(i int, now time.Time) string {
	cd := p.cooldowns[i]
	switch {
	case p.disabled[i]:
		return "disabled"
	case now.After(cd):
		return "ready"
	default:
		return fmt.Sprintf("cooling(%.0fs)", cd.Sub(now).Seconds())
	}
}

func (p *KeyPool) Status() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	parts := make([]string, len(p.keys))
	for i := range p.keys {
		parts[i] = fmt.Sprintf("[%d]:%s", i, p.keyStatusLabel(i, now))
	}
	return strings.Join(parts, " ")
}

// GetKeyDetails returns detailed status for each key in the pool
func (p *KeyPool) GetKeyDetails() []map[string]interface{} {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := time.Now()
	details := make([]map[string]interface{}, len(p.keys))
	for i := range p.keys {
		p.cleanupOldRequests(i)
		keyDetail := map[string]interface{}{
			"index":               i,
			"key":                 maskKey(p.keys[i]),
			"disabled":            p.disabled[i],
			"requests_per_minute": p.requestsInLastMinute(i),
			"last_used":           p.lastUsed[i].Format(time.RFC3339),
			"cooldown_until":      p.cooldowns[i].Format(time.RFC3339),
		}
		keyDetail["status"] = p.keyStatusLabel(i, now)
		details[i] = keyDetail
	}
	return details
}

// IncrementRequestCount records a request timestamp for a key and updates its lastUsed timestamp
func (p *KeyPool) IncrementRequestCount(idx int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.cleanupOldRequests(idx)
	p.requestHistory[idx] = append(p.requestHistory[idx], time.Now())
	p.lastUsed[idx] = time.Now()
}

// AddKey appends a new key to the pool and returns its index
func (p *KeyPool) AddKey(key string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.keys = append(p.keys, key)
	p.cooldowns = append(p.cooldowns, time.Time{})
	p.disabled = append(p.disabled, false)
	p.requestHistory = append(p.requestHistory, []time.Time{})
	p.lastUsed = append(p.lastUsed, time.Time{})
	idx := len(p.keys) - 1
	log.Printf("➕ Key [%d] added to pool", idx)
	return idx
}

// RemoveKey removes a key from the pool by index
func (p *KeyPool) RemoveKey(idx int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if idx < 0 || idx >= len(p.keys) {
		return fmt.Errorf("key index %d out of range (0-%d)", idx, len(p.keys)-1)
	}
	p.keys = append(p.keys[:idx], p.keys[idx+1:]...)
	p.cooldowns = append(p.cooldowns[:idx], p.cooldowns[idx+1:]...)
	p.disabled = append(p.disabled[:idx], p.disabled[idx+1:]...)
	p.requestHistory = append(p.requestHistory[:idx], p.requestHistory[idx+1:]...)
	p.lastUsed = append(p.lastUsed[:idx], p.lastUsed[idx+1:]...)
	log.Printf("➖ Key [%d] removed from pool", idx)
	return nil
}

// ── Config ────────────────────────────────────

type Config struct {
	TargetBase  string
	GenaiBase   string
	Port        string
	MaxRetries  int
	CooldownSec int
	AdminToken  string
}

func parseKeysFromEnv() ([]string, error) {
	raw := os.Getenv("API_KEYS")
	if raw == "" {
		return nil, fmt.Errorf("API_KEYS is required")
	}
	var keys []string
	for _, k := range strings.Split(raw, ",") {
		if k = strings.TrimSpace(k); k != "" {
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no valid API keys found in API_KEYS")
	}
	return keys, nil
}

func buildConfig() (Config, *KeyPool, error) {
	keys, err := parseKeysFromEnv()
	if err != nil {
		return Config{}, nil, err
	}
	cfg := Config{
		TargetBase:  strings.TrimRight(getenv("TARGET_BASE_URL", "https://integrate.api.nvidia.com/v1"), "/"),
		GenaiBase:   strings.TrimRight(getenv("GENAI_BASE_URL", "https://ai.api.nvidia.com"), "/"),
		Port:        getenv("PORT", "3000"),
		MaxRetries:  getenvInt("MAX_RETRIES", 10),
		CooldownSec: 60,
		AdminToken:  getenv("ADMIN_TOKEN", ""),
	}
	return cfg, NewKeyPool(keys), nil
}

func loadConfig() (Config, *KeyPool) {
	cfg, pool, err := buildConfig()
	if err != nil {
		log.Fatalf("❌ %v", err)
	}
	return cfg, pool
}

func reloadConfig() (Config, *KeyPool, error) {
	for _, k := range []string{"API_KEYS", "TARGET_BASE_URL", "GENAI_BASE_URL", "PORT", "COOLDOWN_SEC", "ADMIN_TOKEN"} {
		os.Unsetenv(k)
	}
	loadDotEnv(".env")
	return buildConfig()
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

// ── Server ────────────────────────────────────

type ServerState struct {
	mu     sync.RWMutex
	cfg    Config
	pool   *KeyPool
	mux    *http.ServeMux
	client *http.Client
}

func newServerState(cfg Config, pool *KeyPool) *ServerState {
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
	}
	s.mux.HandleFunc("/health", s.healthHandler)
	s.mux.HandleFunc("/logs", s.logsHandler)
	s.mux.HandleFunc("/dashboard", s.dashboardHandler)
	s.mux.HandleFunc("/clear", s.clearHandler)
	s.mux.HandleFunc("/api/config", s.configHandler)
	s.mux.HandleFunc("/api/keys", s.keysHandler)
	// Block service worker requests to prevent 404s and unnecessary upstream proxying
	s.mux.HandleFunc("/sw.js", s.swHandler)
	s.mux.HandleFunc("/", s.proxyHandler)
	return s
}

func (s *ServerState) swHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
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
		pool.mu.Lock()
		keys := make([]string, len(pool.keys))
		copy(keys, pool.keys)
		pool.mu.Unlock()

		maskedKeys := make([]string, len(keys))
		for i, k := range keys {
			maskedKeys[i] = maskKey(k)
		}
		s.respondJSON(w, http.StatusOK, ConfigPayload{
			TargetBase: cfg.TargetBase,
			GenaiBase:  cfg.GenaiBase,
			Keys:       maskedKeys,
		})
		return
	}

	if r.Method == http.MethodPost {
		if s.cfg.AdminToken != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Admin-Token")), []byte(s.cfg.AdminToken)) != 1 {
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
		currentKeys := s.pool.keys
		s.mu.RUnlock()

		reclaimed := make(map[int]bool)
		for i := range payload.Keys {
			k := strings.TrimSpace(payload.Keys[i])
			if k == "" {
				continue
			}
			// If the key is masked (contains "..." or is "****"), try to restore it from the current pool
			if strings.Contains(k, "...") || k == "****" {
				for j, ck := range currentKeys {
					if !reclaimed[j] && maskKey(ck) == k {
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
			fmt.Sprintf("PORT=%s", cfg.Port),
			fmt.Sprintf("COOLDOWN_SEC=%d", cfg.CooldownSec),
			fmt.Sprintf("MAX_RETRIES=%d", cfg.MaxRetries),
		}

		if err := os.WriteFile(".env", []byte(strings.Join(envLines, "\n")), 0600); err != nil {
			log.Printf("❌ Failed to write .env: %v", err)
			http.Error(w, "failed to save config", http.StatusInternalServerError)
			return
		}

		log.Printf("📝 Configuration updated via API")

		newCfg, newPool, err := reloadConfig()
		if err != nil {
			log.Printf("⚠️ Immediate reload failed: %v", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusAccepted)
			json.NewEncoder(w).Encode(map[string]string{"status": "accepted", "warning": "config saved but reload failed: " + err.Error()})
			return
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
		pool.mu.Lock()
		now := time.Now()
		result := make([]map[string]interface{}, len(pool.keys))
		for i := range pool.keys {
			pool.cleanupOldRequests(i)
			result[i] = map[string]interface{}{
				"index":       i + 1,
				"key":         maskKey(pool.keys[i]),
				"status":      pool.keyStatusLabel(i, now),
				"requests_1m": pool.requestsInLastMinute(i),
			}
		}
		pool.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)

	case http.MethodPost:
		var body struct {
			Key string `json:"key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}
		if body.Key == "" {
			http.Error(w, "key is required", http.StatusBadRequest)
			return
		}
		idx := pool.AddKey(body.Key)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{
			"index": idx,
			"key":   maskKey(body.Key),
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
	pool := s.pool
	s.mu.RUnlock()

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
	s.mu.RUnlock()

	var bodyBytes []byte
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10MB limit
		var err error
		bodyBytes, err = io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil {
			http.Error(w, "failed to read request body", http.StatusBadRequest)
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

	log.Printf("→ %s %s (%d bytes)", r.Method, target, len(bodyBytes))

	for attempt := 0; attempt < cfg.MaxRetries; attempt++ {
		idx, key, ok := pool.Next()
		if !ok {
			wait := pool.TimeUntilAvailable()
			log.Printf("⏳ All keys cooling — waiting %s (attempt %d/%d)", wait.Round(time.Second), attempt+1, cfg.MaxRetries)
			time.Sleep(wait + 500*time.Millisecond)
			continue
		}

		req, err := http.NewRequestWithContext(r.Context(), r.Method, target, bytes.NewReader(bodyBytes))
		if err != nil {
			http.Error(w, "proxy: failed to build upstream request", http.StatusInternalServerError)
			return
		}
		copyHeaders(req.Header, r.Header)
		req.Header.Set("Authorization", "Bearer "+key)

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("⚠️ Key [%d] network error: %v", idx, err)
			pool.Cooldown(idx, time.Duration(cfg.CooldownSec)*time.Second)
			continue
		}

		switch resp.StatusCode {
		case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable:
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			cooldown := time.Duration(cfg.CooldownSec) * time.Second
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if secs, err := strconv.Atoi(ra); err == nil {
					cooldown = time.Duration(secs+2) * time.Second
				}
			}
			log.Printf("🚫 Key [%d] %d — cooldown %s | %s", idx, resp.StatusCode, cooldown, pool.Status())
			log.Printf("   body: %s", body)
			pool.Cooldown(idx, cooldown)
			continue

		case http.StatusUnauthorized, http.StatusForbidden:
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			log.Printf("🔑 Key [%d] %d — disabled. body: %s", idx, resp.StatusCode, body)
			pool.Disable(idx)
			if pool.ActiveCount() == 0 {
				http.Error(w, "alvus: all keys are invalid or revoked", http.StatusServiceUnavailable)
				return
			}
			continue
		}

		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			copyHeaders(w.Header(), resp.Header)
			w.WriteHeader(resp.StatusCode)
			io.Copy(w, resp.Body)
			resp.Body.Close()

			appendUsageLog(LogEntry{Timestamp: time.Now().Format(time.RFC3339), Key: key, KeyIndex: idx + 1, Method: r.Method, URL: target, Status: resp.StatusCode, RequestBodySize: len(bodyBytes)})
			log.Printf("⚠️ %s %s → %d (Terminal Client Error, no retry)", r.Method, target, resp.StatusCode)
			return
		}

		if resp.StatusCode >= 500 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			log.Printf("⚠️ Upstream %d: %s (Retrying...)", resp.StatusCode, body)

			continue
		}

		copyHeaders(w.Header(), resp.Header)
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
		appendUsageLog(LogEntry{Timestamp: time.Now().Format(time.RFC3339), Key: key, KeyIndex: idx + 1, Method: r.Method, URL: target, Status: resp.StatusCode, RequestBodySize: len(bodyBytes)})
		log.Printf("✅ %s %s → %d (key[%d], attempt %d)", r.Method, target, resp.StatusCode, idx, attempt+1)
		return
	}

	http.Error(w, "alvus: exhausted all retries", http.StatusServiceUnavailable)
}

func (s *ServerState) logsHandler(w http.ResponseWriter, r *http.Request) {
	usageMu.Lock()
	masked := make([]LogEntry, len(usageLogs))
	for i, entry := range usageLogs {
		masked[i] = LogEntry{
			Timestamp:       entry.Timestamp,
			Key:             maskKey(entry.Key),
			KeyIndex:        entry.KeyIndex,
			Method:          entry.Method,
			URL:             entry.URL,
			Status:          entry.Status,
			RequestBodySize: entry.RequestBodySize,
		}
	}
	usageMu.Unlock()
	s.respondJSON(w, http.StatusOK, masked)
}

func (s *ServerState) dashboardHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(dashboardHTML))
}

//go:embed dashboard.html
var dashboardHTML string

func (s *ServerState) clearHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	usageMu.Lock()
	usageLogs = []LogEntry{}
	usageMu.Unlock()
	s.respondJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
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
					log.Printf("⚠️ .env file deleted — keeping current config")
				}
				continue
			}
			if !info.ModTime().After(lastMod) {
				continue
			}
			lastMod = info.ModTime()
			time.Sleep(100 * time.Millisecond) // debounce

			log.Printf("🔄 .env changed — reloading...")
			newCfg, newPool, err := reloadConfig()
			if err != nil {
				log.Printf("❌ Reload failed: %v", err)
				continue
			}
			state.mu.Lock()
			state.cfg = newCfg
			state.pool = newPool
			state.mu.Unlock()
			log.Printf("✅ Reloaded — %d keys, target: %s, genai: %s", len(newPool.keys), newCfg.TargetBase, newCfg.GenaiBase)
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
		log.Printf("🛑 Shutting down gracefully...")
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

	loadDotEnv(".env")
	cfg, pool := loadConfig()
	state := newServerState(cfg, pool)

	go watchEnvFile(state, stop)

	addr := host + ":" + cfg.Port

	// Check port availability and bind
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("❌ 端口 %s 已被占用: %v", cfg.Port, err)
	}

	server := &http.Server{Handler: state.mux}

	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("❌ Shutdown error: %v", err)
		}
	}()


	displayHost := host
	if displayHost == "" {
		displayHost = "0.0.0.0"
	}
	tagSuffix := ""
	if *processTag != "" {
		tagSuffix = fmt.Sprintf(" [tag=%s]", *processTag)
	}
	log.Printf("⚡ Alvus%s %s:%s → %s | genai → %s (%d keys)", tagSuffix, displayHost, cfg.Port, cfg.TargetBase, cfg.GenaiBase, len(pool.keys))
	if err := server.Serve(listener); err != http.ErrServerClosed {
		log.Fatalf("❌ Server error: %v", err)
	}
}

// ── .env Loader ───────────────────────────────

func loadDotEnv(filename string) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return
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
		k, v := strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
		if os.Getenv(k) == "" {
			os.Setenv(k, v)
		}
	}
}



