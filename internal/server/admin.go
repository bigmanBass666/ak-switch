package server

import (
	"akswitch/internal/circuitbreaker"
	"akswitch/internal/config"
	"akswitch/internal/keypool"
	"akswitch/internal/utils"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// ── Management Handlers ────────────────────────────────

func (pr *ProviderRouter) swHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
}

func (pr *ProviderRouter) logLevelHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !pr.checkAnyAdminToken(w, r) {
		return
	}
	var body struct {
		Level string `json:"level"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON body"})
		return
	}
	body.Level = strings.TrimSpace(strings.ToLower(body.Level))
	switch body.Level {
	case "debug", "info", "warn", "error":
		ApplyLogLevel(body.Level)
		respondJSON(w, http.StatusOK, map[string]string{"level": body.Level})
	default:
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid log level, use: debug, info, warn, error"})
	}
}

func (pr *ProviderRouter) configHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		// Return config for a specific provider or all providers
		ps, _ := pr.resolveProvider(r)
		if ps == nil {
			// Return all providers
			pr.mu.RLock()
			result := make(map[string]ConfigPayload)
			for name, p := range pr.providers {
				keys := p.Pool.Keys()
				maskedKeys := make([]string, len(keys))
				for i, k := range keys {
					maskedKeys[i] = utils.MaskKey(k)
				}
				result[name] = ConfigPayload{
					TargetBase: p.Config.TargetBase,
					GenaiBase:  p.Config.GenaiBase,
					Keys:       maskedKeys,
				}
			}
			pr.mu.RUnlock()
			respondJSON(w, http.StatusOK, map[string]interface{}{"providers": result})
			return
		}

		keys := ps.Pool.Keys()
		maskedKeys := make([]string, len(keys))
		for i, k := range keys {
			maskedKeys[i] = utils.MaskKey(k)
		}
		respondJSON(w, http.StatusOK, ConfigPayload{
			TargetBase: ps.Config.TargetBase,
			GenaiBase:  ps.Config.GenaiBase,
			Keys:       maskedKeys,
		})
		return
	}

	// POST is removed — no more .env writing
	w.WriteHeader(http.StatusMethodNotAllowed)
}

func (pr *ProviderRouter) keysHandler(w http.ResponseWriter, r *http.Request) {
	ps, errMsg := pr.resolveProvider(r)
	if ps == nil {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": errMsg})
		return
	}

	pool := ps.Pool

	if r.Method == http.MethodPost || r.Method == http.MethodDelete {
		if !pr.checkAdminToken(w, r, ps.Name) {
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
		ps.State.PersistKeys()
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
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid index"})
			return
		}
		if err := pool.RemoveKey(body.Index - 1); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		ps.State.PersistKeys()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "removed"})

	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (pr *ProviderRouter) healthHandler(w http.ResponseWriter, r *http.Request) {
	if !pr.checkAnyAdminToken(w, r) {
		return
	}

	pr.mu.RLock()
	defer pr.mu.RUnlock()

	// Aggregate health info across all providers
	type providerHealth struct {
		Status            string `json:"status"`
		Keys              int    `json:"keys"`
		UpstreamCBState   string `json:"upstream_cb_state"`
		LastHealthCheck   string `json:"last_health_check,omitempty"`
		LastHealthCheckOK *bool  `json:"last_health_check_ok,omitempty"`
	}

	result := make(map[string]*providerHealth)
	overallOK := true

	for name, ps := range pr.providers {
		upCB := ps.Proxy.upCB

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

		lastCheckTime, lastCheckOK := ps.State.LastHealthCheck()
		var lastCheckISO string
		if !lastCheckTime.IsZero() {
			lastCheckISO = lastCheckTime.Format(time.RFC3339)
		}
		var lastCheckResult *bool
		if !lastCheckTime.IsZero() {
			lastCheckResult = &lastCheckOK
		}

		ph := &providerHealth{
			Status:            "ok",
			Keys:              len(ps.Pool.Keys()),
			UpstreamCBState:   cbState,
			LastHealthCheck:   lastCheckISO,
			LastHealthCheckOK: lastCheckResult,
		}
		result[name] = ph

		if cbState != "closed" || (lastCheckResult != nil && !*lastCheckResult) {
			overallOK = false
		}
	}

	status := "ok"
	if !overallOK {
		status = "degraded"
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":    status,
		"providers": len(pr.providers),
		"details":   result,
	})
}

func (pr *ProviderRouter) logsHandler(w http.ResponseWriter, r *http.Request) {
	entries := pr.logs.Snapshot()
	respondJSON(w, http.StatusOK, entries)
}

func (pr *ProviderRouter) dashboardHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html")
	w.Write([]byte(pr.dashboardHTML))
}

func (pr *ProviderRouter) clearHandler(w http.ResponseWriter, r *http.Request) {
	if !pr.checkAnyAdminToken(w, r) {
		return
	}
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	pr.logs.Clear()
	respondJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}

func (pr *ProviderRouter) statsHandler(w http.ResponseWriter, r *http.Request) {
	total := pr.logs.Len()
	entries := pr.logs.Snapshot()

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

	// Aggregate key stats across all providers
	pr.mu.RLock()
	totalActive := 0
	totalCooling := 0
	totalDisabled := 0
	for _, ps := range pr.providers {
		totalActive += ps.Pool.ActiveCount()
		totalCooling += ps.Pool.CoolingCount()
		totalDisabled += ps.Pool.DisabledCount()
	}
	pr.mu.RUnlock()

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"total_requests":      total,
		"successful_requests": successful,
		"failed_requests":     failed,
		"success_rate":        fmt.Sprintf("%.2f", successRate),
		"active_keys":         totalActive,
		"cooling_keys":        totalCooling,
		"disabled_keys":       totalDisabled,
		"uptime_seconds":      time.Since(pr.startTime).Seconds(),
	})
}

func (pr *ProviderRouter) reloadHandler(w http.ResponseWriter, r *http.Request) {
	if !pr.checkAnyAdminToken(w, r) {
		return
	}

	// Reload TOML config from the XDG path
	xdgPath, err := config.XDGConfigPath()
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   "failed to determine config path: " + err.Error(),
		})
		return
	}

	providers, err := config.LoadAllTomlProviders(xdgPath)
	if err != nil {
		slog.Warn("reload failed", "error", err)
		respondJSON(w, http.StatusInternalServerError, map[string]interface{}{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	pr.mu.Lock()
	defer pr.mu.Unlock()

	for name, cfg := range providers {
		// Load keys from configured keys file or standard encrypted store
		keys, keyNames := loadKeysFromConfig(name, cfg)
		if len(keys) > 0 {
			cfg.Keys = keys
			cfg.KeyNames = keyNames
		}

		if existing, ok := pr.providers[name]; ok {
			// Update existing provider
			existing.Config = cfg
			existing.Pool = keypool.NewKeyPool(cfg.Keys, cfg.KeyNames)
			existing.State.cfg = cfg
			existing.State.pool = existing.Pool
			ApplyLogLevel(cfg.LogLevel)
		} else {
			// New provider — add it
			pool := keypool.NewKeyPool(cfg.Keys, cfg.KeyNames)
			state := NewServerState(name, cfg, pool, pr.dashboardHTML, cfg.KeysFile)
			ps := &ProviderState{
				Name:   name,
				Config: cfg,
				Pool:   pool,
				Proxy:  state.proxy,
				State:  state,
			}
			ApplyLogLevel(cfg.LogLevel)
			pr.providers[name] = ps
		}
	}

	slog.Info("config reloaded", "providers", len(pr.providers))
	respondJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// loadKeysFromConfig loads API keys for a provider from the configured keys file
// or the standard encrypted store. Returns nil if no keys can be loaded.
func loadKeysFromConfig(name string, cfg *config.Config) (keys, names []string) {
	keys, names, loaded := keypool.LoadKeysFromStore(name, cfg)
	if !loaded {
		return nil, nil
	}
	return keys, names
}

// ── Key CRUD Handlers ─────────────────────────────────

func (pr *ProviderRouter) disableKeyHandler(w http.ResponseWriter, r *http.Request) {
	ps, errMsg := pr.resolveProvider(r)
	if ps == nil {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": errMsg})
		return
	}
	if !pr.checkAdminToken(w, r, ps.Name) {
		return
	}

	idx, ok := parseKeyIndex(r)
	if !ok || idx >= len(ps.Pool.Keys()) {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "key not found"})
		return
	}

	if err := ps.Pool.Disable(idx); err != nil {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	ps.State.PersistKeys()
	respondJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (pr *ProviderRouter) enableKeyHandler(w http.ResponseWriter, r *http.Request) {
	ps, errMsg := pr.resolveProvider(r)
	if ps == nil {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": errMsg})
		return
	}
	if !pr.checkAdminToken(w, r, ps.Name) {
		return
	}

	idx, ok := parseKeyIndex(r)
	if !ok || idx >= len(ps.Pool.Keys()) {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "key not found"})
		return
	}

	if err := ps.Pool.Enable(idx); err != nil {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	ps.Proxy.keyCBs[idx].Reset()
	ps.State.PersistKeys()
	respondJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (pr *ProviderRouter) cooldownKeyHandler(w http.ResponseWriter, r *http.Request) {
	ps, errMsg := pr.resolveProvider(r)
	if ps == nil {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": errMsg})
		return
	}
	if !pr.checkAdminToken(w, r, ps.Name) {
		return
	}

	idx, ok := parseKeyIndex(r)
	if !ok || idx >= len(ps.Pool.Keys()) {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "key not found"})
		return
	}

	if err := ps.Pool.Cooldown(idx, time.Duration(ps.Config.CooldownSec)*time.Second); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, map[string]bool{"success": true})
}

func (pr *ProviderRouter) deleteKeyHandler(w http.ResponseWriter, r *http.Request) {
	ps, errMsg := pr.resolveProvider(r)
	if ps == nil {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": errMsg})
		return
	}
	if !pr.checkAdminToken(w, r, ps.Name) {
		return
	}

	idx, ok := parseKeyIndex(r)
	if !ok || idx >= len(ps.Pool.Keys()) {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "key not found"})
		return
	}

	ps.Pool.RemoveKey(idx)
	ps.State.PersistKeys()
	respondJSON(w, http.StatusOK, map[string]bool{"success": true})
}