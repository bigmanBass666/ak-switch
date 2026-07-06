package server

import (
	"akswitch/internal/circuitbreaker"
	"akswitch/internal/config"
	"akswitch/internal/keypool"
	akswitchmetrics "akswitch/internal/metrics"
	"akswitch/internal/utils"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ConfigPayload is the JSON structure for config API responses.
type ConfigPayload struct {
	TargetBase string   `json:"targetBase"`
	GenaiBase  string   `json:"genaiBase"`
	Keys       []string `json:"keys"`
}

// lookupProvider returns the ProviderState for a given provider name.
func (pr *ProviderRouter) lookupProvider(name string) *ProviderState {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	return pr.providers[name]
}

// firstProvider returns the first (alphabetically) provider, or nil if none exist.
func (pr *ProviderRouter) firstProvider() *ProviderState {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	for _, ps := range pr.providers {
		return ps
	}
	return nil
}

// resolveProvider gets the provider specified by the "provider" query parameter.
// If not set, returns the first provider. Returns an error string if no provider found.
func (pr *ProviderRouter) resolveProvider(r *http.Request) (*ProviderState, string) {
	pName := r.URL.Query().Get("provider")
	if pName == "" {
		ps := pr.firstProvider()
		if ps == nil {
			return nil, "no providers configured"
		}
		return ps, ""
	}
	ps := pr.lookupProvider(pName)
	if ps == nil {
		return nil, fmt.Sprintf("provider %q not found", pName)
	}
	return ps, ""
}

// checkAdminToken validates the X-Admin-Token header against a specific provider's admin token.
func (pr *ProviderRouter) checkAdminToken(w http.ResponseWriter, r *http.Request, providerName string) bool {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	ps, ok := pr.providers[providerName]
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	token := r.Header.Get("X-Admin-Token")
	if ps.Config.AdminToken == "" {
		// No admin token configured for this provider — access allowed
		return true
	}
	if ps.Config.AdminToken == token {
		return true
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	return false
}

// checkAnyAdminToken validates the X-Admin-Token header against any configured admin token.
// If at least one provider has an AdminToken configured, a valid token must be provided.
// If no providers have AdminToken configured, access is allowed without token.
func (pr *ProviderRouter) checkAnyAdminToken(w http.ResponseWriter, r *http.Request) bool {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	token := r.Header.Get("X-Admin-Token")
	hasAnyToken := false
	for _, ps := range pr.providers {
		if ps.Config.AdminToken != "" {
			hasAnyToken = true
			if ps.Config.AdminToken == token {
				return true
			}
		}
	}
	if !hasAnyToken {
		return true
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	return false
}

// ── Proxy Handler ──────────────────────────────────────

func (pr *ProviderRouter) proxyHandler(w http.ResponseWriter, r *http.Request) {
	// Extract provider name from path: /{provider}/...
	providerName, restPath := pr.extractProvider(r.URL.Path)
	if providerName == "" {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "no provider specified in path"})
		return
	}

	pr.mu.RLock()
	ps, ok := pr.providers[providerName]
	pr.mu.RUnlock()
	if !ok {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": "provider not found: " + providerName})
		return
	}

	// Rewrite the URL path (strip provider prefix)
	r.URL.Path = restPath

	// Delegate to the proxy logic with this provider's state
	pr.executeProxy(w, r, ps)
}

// executeProxy contains the core proxy request logic.
func (pr *ProviderRouter) executeProxy(w http.ResponseWriter, r *http.Request, ps *ProviderState) {
	cfg := ps.Config
	pool := ps.Pool
	client := ps.Proxy.client
	keyCBs := ps.Proxy.keyCBs
	upCB := ps.Proxy.upCB

	start := time.Now()
	var lastKey string
	var lastIdx int

	var bodyBytes []byte
	if r.Body != nil {
		r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10MB limit
		var err error
		bodyBytes, err = io.ReadAll(r.Body)
		r.Body.Close()
		if err != nil {
			writeProxyError(w, http.StatusBadRequest, ErrorBadRequest, "request body too large or unreadable")
			pr.recordProxyMetrics(r.Method, "4xx", "", start)
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

	slog.Info("proxy request", "provider", ps.Name, "method", r.Method, "url", target, "body_size", len(bodyBytes))

	if auth := r.Header.Get("Authorization"); auth != "" {
		maskedAuth := auth
		if len(auth) > 12 {
			maskedAuth = auth[:7] + "..." + auth[len(auth)-4:]
		} else {
			maskedAuth = "****"
		}
		bodyPreview := ""
		if len(bodyBytes) > 0 {
			preview := string(bodyBytes)
			if len(preview) > 1024 {
				preview = preview[:1024]
			}
			bodyPreview = MaskSensitiveData(preview, 1024)
		}
		slog.Debug("proxy request debug", "provider", ps.Name, "method", r.Method, "path", r.URL.Path, "auth", maskedAuth, "body_size", len(bodyBytes), "body_preview", bodyPreview)
	}

	for attempt := 0; attempt < cfg.MaxRetries; attempt++ {
		if !upCB.Allow() {
			slog.Warn("upstream circuit breaker open, backing off", "provider", ps.Name, "retry", attempt, "max", cfg.MaxRetries)
			time.Sleep(time.Second)
			continue
		}

		idx, key, ok := pool.Next()
		if !ok {
			wait := pool.TimeUntilAvailable()
			if wait < 0 {
				writeProxyError(w, http.StatusServiceUnavailable, ErrorAllKeysInvalid, fmt.Sprintf("%s 所有 API Key 已熔断，请稍后重试", ps.Name))
				pr.recordProxyMetrics(r.Method, "5xx", "", start)
				return
			}
			jitter := time.Duration(rand.Intn(500)) * time.Millisecond
			slog.Warn("all keys cooling", "provider", ps.Name, "wait", (wait+jitter).Round(time.Second), "retry", attempt, "max", cfg.MaxRetries)
			time.Sleep(wait + jitter)
			continue
		}
		lastKey = key
		lastIdx = idx

		if !keyCBs[idx].Allow() {
			remaining := keyCBs[idx].CooldownRemaining()
			if remaining < 0 {
				allPerma := true
				for _, cb := range keyCBs {
					if cb.State() != circuitbreaker.StatePermanent {
						allPerma = false
						break
					}
				}
				if allPerma {
					writeProxyError(w, http.StatusServiceUnavailable, ErrorAllKeysInvalid, fmt.Sprintf("%s 所有 API Key 已熔断，请稍后重试", ps.Name))
					pr.recordProxyMetrics(r.Method, "5xx", "", start)
					return
				}
				continue
			}
			if remaining > 0 {
				pool.Cooldown(idx, remaining)
			}
			continue
		}

		req, err := http.NewRequestWithContext(r.Context(), r.Method, target, bytes.NewReader(bodyBytes))
		if err != nil {
			pr.metrics.UpstreamErrors.WithLabelValues("network").Inc()
			writeProxyError(w, http.StatusInternalServerError, ErrorUpstreamError, "failed to build upstream request")
			pr.recordProxyMetrics(r.Method, "5xx", "", start)
			return
		}
		utils.CopyHeaders(req.Header, r.Header)
		req.Header.Set("Authorization", "Bearer "+key)

		resp, err := client.Do(req)
		if err != nil {
			switch categorizeError(0, err) {
			case CatClientAbort:
				slog.Debug("client aborted request", "provider", ps.Name, "key_index", idx, "key_name", pool.Name(idx), "error", err)
				return
			default:
				slog.Warn("key network error", "provider", ps.Name, "key_index", idx, "key_name", pool.Name(idx), "error", err)
				pr.metrics.UpstreamErrors.WithLabelValues("network").Inc()
				upCB.RecordFailure()
				continue
			}
		}

		// ── Response status dispatch ──
		switch {
		case resp.StatusCode == http.StatusTooManyRequests:
			if pr.handleRateLimited(w, ps, idx, resp, cfg, start, r.Method, target, bodyBytes) {
				return
			}
			continue

		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			if pr.handleAuthRejected(w, ps, idx, resp, start, r.Method, target, bodyBytes) {
				return
			}
			continue

		case resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusServiceUnavailable:
			pr.handleServerError(ps, idx, resp, attempt)
			continue

		case resp.StatusCode >= 400 && resp.StatusCode < 500 || categorizeError(resp.StatusCode, nil) == CatNonRetryable:
			pr.handleNonRetryable(w, ps, idx, resp, start, r.Method, target, bodyBytes, attempt, key)
			return

		case resp.StatusCode >= 500:
			pr.handleServerError(ps, idx, resp, attempt)
			continue

		default:
			// 2xx/3xx — success
			pr.handleSuccess(w, ps, idx, resp, start, r.Method, target, bodyBytes, attempt, key)
			return
		}
	}

	// Retry exhausted
	writeProxyError(w, http.StatusServiceUnavailable, ErrorExhaustedRetries, fmt.Sprintf("%s 重试已耗尽，所有 Key 无响应", ps.Name))
	pr.logs.Append(utils.LogEntry{
		Timestamp:       time.Now().Format(time.RFC3339),
		Key:             lastKey,
		KeyIndex:        lastIdx + 1,
		KeyName:         pool.Name(lastIdx),
		Method:          r.Method,
		URL:             target,
		Status:          http.StatusServiceUnavailable,
		RequestBodySize: len(bodyBytes),
		DurationMs:      time.Since(start).Milliseconds(),
		Retries:         cfg.MaxRetries,
		Provider:        ps.Name,
	})
	slog.Debug("proxy response debug", "status", 503, "duration_ms", time.Since(start).Seconds()*1000, "retries", cfg.MaxRetries)
	pr.recordProxyMetrics(r.Method, "5xx", "", start)
}

// ── Response Status Handlers ───────────────────────────

// handleRateLimited processes a 429 Too Many Requests response.
// It records the failure, applies cooldown (respecting Retry-After headers),
// and returns true if all keys are exhausted (caller should abort).
// When returning true, the error response has already been written to w.
func (pr *ProviderRouter) handleRateLimited(w http.ResponseWriter, ps *ProviderState, idx int, resp *http.Response, cfg *config.Config, start time.Time, method, target string, bodyBytes []byte) bool {
	pool := ps.Pool
	keyCBs := ps.Proxy.keyCBs

	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	cbCooldown := keyCBs[idx].RecordFailure()
	cooldown := cbCooldown
	if ra := resp.Header.Get("Retry-After"); ra != "" {
		if secs, err := strconv.Atoi(ra); err == nil {
			raDuration := time.Duration(secs+2) * time.Second
			if raDuration > cooldown {
				cooldown = raDuration
			}
		}
	}
	pool.Cooldown(idx, cooldown)
	slog.Warn("key rate limited", "provider", ps.Name, "key_index", idx, "key_name", pool.Name(idx), "status", resp.StatusCode, "cb_state", fmt.Sprintf("%d", keyCBs[idx].State()), "cb_retry", keyCBs[idx].Attempt(), "body_preview", string(body))
	pr.metrics.UpstreamErrors.WithLabelValues("rate_limited").Inc()

	if keyCBs[idx].State() == circuitbreaker.StatePermanent {
		slog.Warn("key quota exhausted, disabling permanently", "provider", ps.Name, "key_index", idx, "key_name", pool.Name(idx))
		pool.Disable(idx)
		if pool.ActiveCount() == 0 {
			writeProxyError(w, http.StatusServiceUnavailable, ErrorAllKeysInvalid, fmt.Sprintf("%s 所有 API Key 已熔断，请稍后重试", ps.Name))
			pr.recordProxyMetrics(method, "5xx", "", start)
			return true
		}
	}
	return false
}

// handleAuthRejected processes a 401 Unauthorized or 403 Forbidden response.
// It disables the key permanently and returns true if all keys are exhausted.
// When returning true, the error response has already been written to w.
func (pr *ProviderRouter) handleAuthRejected(w http.ResponseWriter, ps *ProviderState, idx int, resp *http.Response, start time.Time, method, target string, bodyBytes []byte) bool {
	pool := ps.Pool
	keyCBs := ps.Proxy.keyCBs

	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	pr.metrics.UpstreamErrors.WithLabelValues("auth_rejected").Inc()
	if keyCBs[idx].RecordAuthFailure() {
		pool.Disable(idx)
		keyCBs[idx].RecordPerma("auth_rejected")
		slog.Warn("key permanently disabled", "provider", ps.Name, "key_index", idx, "key_name", pool.Name(idx), "status", resp.StatusCode, "body_preview", string(body))
	} else {
		slog.Warn("key auth failure", "provider", ps.Name, "key_index", idx, "key_name", pool.Name(idx), "status", resp.StatusCode, "fail_count", keyCBs[idx].AuthFailCount())
	}
	if pool.ActiveCount() == 0 {
		writeProxyError(w, http.StatusServiceUnavailable, ErrorAllKeysInvalid, fmt.Sprintf("%s 所有 Key 已失效或吊销", ps.Name))
		pr.recordProxyMetrics(method, "5xx", "", start)
		return true
	}
	return false
}

// handleServerError processes a 502 Bad Gateway or 503 Service Unavailable (or other 5xx) response.
// It logs the error, records metrics, and records an upstream circuit breaker failure.
func (pr *ProviderRouter) handleServerError(ps *ProviderState, idx int, resp *http.Response, attempt int) {
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	slog.Warn("upstream server error", "provider", ps.Name, "key_index", idx, "key_name", ps.Pool.Name(idx), "status", resp.StatusCode, "body_preview", string(body))
	pr.metrics.UpstreamErrors.WithLabelValues("server_error").Inc()
	ps.Proxy.upCB.RecordFailure()
}

// handleNonRetryable copies a non-retryable 4xx response through to the client
// without further retry attempts.
func (pr *ProviderRouter) handleNonRetryable(w http.ResponseWriter, ps *ProviderState, idx int, resp *http.Response, start time.Time, method, target string, bodyBytes []byte, attempt int, key string) {
	utils.CopyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
	resp.Body.Close()

	pr.logs.Append(buildLogEntry(ps, key, idx, method, target, resp.StatusCode, len(bodyBytes), start, attempt))
	slog.Warn("non-retryable client error", "provider", ps.Name, "method", method, "url", target, "status", resp.StatusCode)
	slog.Debug("proxy response debug", "status", resp.StatusCode, "duration_ms", time.Since(start).Seconds()*1000, "retries", attempt+1)
	pr.recordProxyMetrics(method, "4xx", fmt.Sprintf("%d", idx), start)
}

// handleSuccess processes a successful 2xx/3xx response, including streaming
// for SSE and chunked responses.
func (pr *ProviderRouter) handleSuccess(w http.ResponseWriter, ps *ProviderState, idx int, resp *http.Response, start time.Time, method, target string, bodyBytes []byte, attempt int, key string) {
	pool := ps.Pool
	keyCBs := ps.Proxy.keyCBs
	upCB := ps.Proxy.upCB

	keyCBs[idx].RecordSuccess()
	upCB.RecordSuccess()

	utils.CopyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)

	streamResponse(w, resp)

	pool.IncrementRequestCount(idx)
	pr.logs.Append(buildLogEntry(ps, key, idx, method, target, resp.StatusCode, len(bodyBytes), start, attempt))
	slog.Info("proxy success", "provider", ps.Name, "method", method, "url", target, "status", resp.StatusCode, "key_index", idx, "key_name", pool.Name(idx), "retry", attempt)
	slog.Debug("proxy response debug", "status", resp.StatusCode, "duration_ms", time.Since(start).Seconds()*1000, "retries", attempt+1)
	pr.recordProxyMetrics(method, akswitchmetrics.StatusLabel(resp.StatusCode), fmt.Sprintf("%d", idx), start)
}

// ── Proxy Helpers ──────────────────────────────────────

// buildLogEntry creates a structured LogEntry for proxy request logging.
func buildLogEntry(ps *ProviderState, key string, idx int, method, target string, status int, bodySize int, start time.Time, attempt int) utils.LogEntry {
	return utils.LogEntry{
		Timestamp:       time.Now().Format(time.RFC3339),
		Key:             key,
		KeyIndex:        idx + 1,
		KeyName:         ps.Pool.Name(idx),
		Method:          method,
		URL:             target,
		Status:          status,
		RequestBodySize: bodySize,
		DurationMs:      time.Since(start).Milliseconds(),
		Retries:         attempt,
		Provider:        ps.Name,
	}
}

// recordProxyMetrics records request total count and duration metrics.
func (pr *ProviderRouter) recordProxyMetrics(method, statusClass, keyIndex string, start time.Time) {
	pr.metrics.RequestsTotal.WithLabelValues(method, statusClass, keyIndex).Inc()
	pr.metrics.RequestDuration.WithLabelValues(method, statusClass).Observe(time.Since(start).Seconds())
}

// streamResponse copies the response body to the client writer, flushing after
// each chunk for SSE compatibility. It always closes resp.Body.
func streamResponse(w http.ResponseWriter, resp *http.Response) {
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
}

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
