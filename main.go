package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
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

// ── Key Pool ──────────────────────────────────

type KeyPool struct {
	counter      uint64
	keys         []string
	cooldowns    []time.Time
	disabled     []bool
	requestHistory [][]time.Time // timestamps of requests in the last 60s per key
	lastUsed     []time.Time
	mu           sync.Mutex
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
			"key":                 p.keys[i],
			"disabled":            p.disabled[i],
			"requests_per_minute": p.requestsInLastMinute(i),
			"last_used":           p.lastUsed[i].Format(time.RFC3339),
			"cooldown_until":      p.cooldowns[i].Format(time.RFC3339),
		}

		// Determine label
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

// ── Config ────────────────────────────────────

type Config struct {
	TargetBase  string
	Port        string
	MaxRetries  int
	CooldownSec int
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
		Port:        getenv("PORT", "3000"),
		MaxRetries:  10,
		CooldownSec: 60,
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
	for _, k := range []string{"API_KEYS", "TARGET_BASE_URL", "PORT", "COOLDOWN_SEC"} {
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

// ── Server ────────────────────────────────────

type ServerState struct {
	mu   sync.RWMutex
	cfg  Config
	pool *KeyPool
	mux  *http.ServeMux
}

func newServerState(cfg Config, pool *KeyPool) *ServerState {
	s := &ServerState{cfg: cfg, pool: pool, mux: http.NewServeMux()}
	s.mux.HandleFunc("/health", s.healthHandler)
	s.mux.HandleFunc("/", s.proxyHandler)
	return s
}

func (s *ServerState) healthHandler(w http.ResponseWriter, r *http.Request) {
    s.mu.RLock()
    pool := s.pool
    s.mu.RUnlock()
    w.Header().Set("Content-Type", "application/json")
    
    details := pool.GetKeyDetails()
    jsonDetails, err := json.Marshal(details)
    if err != nil {
        http.Error(w, "failed to marshal key details", http.StatusInternalServerError)
        return
    }
    
    fmt.Fprintf(w, `{"status":"ok","keys":%d,"details":%s}`, len(pool.keys), jsonDetails)
}

func (s *ServerState) proxyHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := s.cfg
	pool := s.pool
	s.mu.RUnlock()

	client := &http.Client{
		Timeout: 120 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	var bodyBytes []byte
	if r.Body != nil {
		var err error
		bodyBytes, err = io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil {
			http.Error(w, "failed to read request body", http.StatusBadRequest)
			return
		}
	}

	path := r.URL.Path
	if strings.HasSuffix(cfg.TargetBase, "/v1") && strings.HasPrefix(path, "/v1") {
		path = path[3:]
	}
	if r.URL.RawQuery != "" {
		path += "?" + r.URL.RawQuery
	}
	target := cfg.TargetBase + path

	log.Printf("→ %s %s (%d bytes)", r.Method, target, len(bodyBytes))

	for attempt := 0; attempt < cfg.MaxRetries; attempt++ {
		idx, key, ok := pool.Next()
		if !ok {
			wait := pool.TimeUntilAvailable()
			log.Printf("⏳ All keys cooling — waiting %s (attempt %d/%d)", wait.Round(time.Second), attempt+1, cfg.MaxRetries)
			time.Sleep(wait + 500*time.Millisecond)
			continue
		}

		req, err := http.NewRequest(r.Method, target, bytes.NewReader(bodyBytes))
		if err != nil {
			http.Error(w, "proxy: failed to build upstream request", http.StatusInternalServerError)
			return
		}
		for k, vals := range r.Header {
			for _, v := range vals {
				req.Header.Add(k, v)
			}
		}
		req.Header.Set("Authorization", "Bearer "+key)

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("⚠️ Key [%d] network error: %v", idx, err)
			pool.Cooldown(idx, time.Duration(cfg.CooldownSec)*time.Second)
			continue
		}

		switch resp.StatusCode {
		case http.StatusTooManyRequests:
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			cooldown := time.Duration(cfg.CooldownSec) * time.Second
			if ra := resp.Header.Get("Retry-After"); ra != "" {
				if secs, err := strconv.Atoi(ra); err == nil {
					cooldown = time.Duration(secs+2) * time.Second
				}
			}
			log.Printf("🚫 Key [%d] 429 — cooldown %s | %s", idx, cooldown, pool.Status())
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

		if resp.StatusCode >= 400 {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			log.Printf("⚠️ Upstream %d: %s", resp.StatusCode, body)
			resp.Body = io.NopCloser(bytes.NewReader(body))
		}

		for k, vals := range resp.Header {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)

		if f, ok := w.(http.Flusher); ok {
			buf := make([]byte, 4096)
			for {
				n, rerr := resp.Body.Read(buf)
				if n > 0 {
					w.Write(buf[:n])
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
		log.Printf("✅ %s %s → %d (key[%d], attempt %d)", r.Method, target, resp.StatusCode, idx, attempt+1)
		return
	}

	http.Error(w, "alvus: exhausted all retries", http.StatusServiceUnavailable)
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
			log.Printf("✅ Reloaded — %d keys, target: %s", len(newPool.keys), newCfg.TargetBase)
		}
	}
}

// ── Main ──────────────────────────────────────

func main() {
	loadDotEnv(".env")
	cfg, pool := loadConfig()
	state := newServerState(cfg, pool)

	stop := make(chan struct{})
	go watchEnvFile(state, stop)

	// Graceful shutdown on SIGINT/SIGTERM
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	server := &http.Server{Addr: ":" + cfg.Port, Handler: state.mux}

	go func() {
		<-sigCh
		log.Printf("🛑 Shutting down gracefully...")
		close(stop)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			log.Printf("❌ Shutdown error: %v", err)
		}
	}()

	log.Printf("⚡ Alvus :%s → %s (%d keys)", cfg.Port, cfg.TargetBase, len(pool.keys))
	if err := server.ListenAndServe(); err != http.ErrServerClosed {
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