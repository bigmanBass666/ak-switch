package server

import (
	"alvus/internal/circuitbreaker"
	"alvus/internal/utils"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

// ConfigPayload is the JSON structure for config API requests/responses.
type ConfigPayload struct {
	TargetBase string   `json:"targetBase"`
	GenaiBase  string   `json:"genaiBase"`
	Keys       []string `json:"keys"`
}

func (s *ServerState) swHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (s *ServerState) logLevelHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()
	if !s.adminAuth(cfg, w, r) {
		return
	}
	var body struct {
		Level string `json:"level"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		s.respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	body.Level = strings.TrimSpace(strings.ToLower(body.Level))
	switch body.Level {
	case "debug", "info", "warn", "error":
		ApplyLogLevel(body.Level)
		s.respondJSON(w, http.StatusOK, map[string]string{"level": body.Level})
	default:
		s.respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid log level, use: debug, info, warn, error"})
	}
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
		if !s.adminAuth(cfg, w, r) {
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

		newCfg, newPool, err := ReloadConfig()
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
	if r.Method == http.MethodPost || r.Method == http.MethodDelete {
		if !s.adminAuth(cfg, w, r) {
			return
		}
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
			Key     string `json:"key"`
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
		s.PersistKeys()
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
		if body.Index < 1 || body.Index > len(pool.Keys()) {
			s.respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid index"})
			return
		}
		if err := pool.RemoveKey(body.Index - 1); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.PersistKeys()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "removed"})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *ServerState) healthHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := s.cfg
	pool := s.pool
	upCB := s.upCB
	s.mu.RUnlock()

	if !s.adminAuth(cfg, w, r) {
		return
	}

	// Upstream CB state string
	var cbState string
	switch upCB.State() {
	case circuitbreaker.UpstreamClosed:
		cbState = "closed"
	case circuitbreaker.UpstreamOpen:
		cbState = "open"
	case circuitbreaker.UpstreamHalfOpen:
		cbState = "half_open"
	default:
		cbState = "unknown"
	}

	// Last health check info
	lastCheckTime, lastCheckOK := s.LastHealthCheck()
	var lastCheckISO string
	if !lastCheckTime.IsZero() {
		lastCheckISO = lastCheckTime.Format(time.RFC3339)
	}
	var lastCheckResult *bool
	if !lastCheckTime.IsZero() {
		lastCheckResult = &lastCheckOK
	}

	details := pool.GetKeyDetails()
	s.respondJSON(w, http.StatusOK, map[string]interface{}{
		"status":             "ok",
		"keys":               len(details),
		"upstream_cb_state":  cbState,
		"last_health_check":  lastCheckISO,
		"last_health_check_ok": lastCheckResult,
		"details":            details,
	})
}

func (s *ServerState) logsHandler(w http.ResponseWriter, r *http.Request) {
	entries := s.logs.Snapshot()
	s.respondJSON(w, http.StatusOK, entries)
}

func (s *ServerState) dashboardHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(s.dashboardHTML))
}

func (s *ServerState) clearHandler(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	cfg := s.cfg
	s.mu.RUnlock()

	if !s.adminAuth(cfg, w, r) {
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	s.logs.Clear()
	s.respondJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
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
		"total_requests":      total,
		"successful_requests": successful,
		"failed_requests":     failed,
		"success_rate":        fmt.Sprintf("%.2f", successRate),
		"active_keys":         pool.ActiveCount(),
		"cooling_keys":        pool.CoolingCount(),
		"disabled_keys":       pool.DisabledCount(),
		"uptime_seconds":      time.Since(s.startTime).Seconds(),
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

	newCfg, newPool, err := ReloadConfig()
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

	if err := pool.Disable(idx); err != nil {
		s.respondJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	s.PersistKeys()
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
	s.PersistKeys()
	s.respondJSON(w, http.StatusOK, map[string]bool{"success": true})
}