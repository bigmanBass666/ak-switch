package server

import (
	"akswitch/internal/circuitbreaker"
	"akswitch/internal/config"
	akswitchmetrics "akswitch/internal/metrics"
	"akswitch/internal/utils"
	"bytes"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"
)

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

	bodyBytes, err := readRequestBody(w, r)
	if err != nil {
		pr.recordProxyMetrics(r.Method, "4xx", "", start)
		return
	}

	// Build target URL
	target := buildTargetURL(cfg, r.URL.Path, r.URL.RawQuery)

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
				pr.writeAllKeysExhausted(w, ps, r.Method, start)
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
					pr.writeAllKeysExhausted(w, ps, r.Method, start)
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
			pr.logs.Append(buildLogEntry(ps, key, idx, r.Method, target, resp.StatusCode, len(bodyBytes), start, attempt))
			if pr.handleRateLimited(w, ps, idx, resp, cfg, start, r.Method, target, bodyBytes) {
				return
			}
			continue

		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			pr.logs.Append(buildLogEntry(ps, key, idx, r.Method, target, resp.StatusCode, len(bodyBytes), start, attempt))
			if pr.handleAuthRejected(w, ps, idx, resp, start, r.Method, target, bodyBytes) {
				return
			}
			continue

		case resp.StatusCode == http.StatusBadGateway || resp.StatusCode == http.StatusServiceUnavailable:
			pr.logs.Append(buildLogEntry(ps, key, idx, r.Method, target, resp.StatusCode, len(bodyBytes), start, attempt))
			pr.handleServerError(ps, idx, resp, attempt)
			continue

		case resp.StatusCode >= 400 && resp.StatusCode < 500 || categorizeError(resp.StatusCode, nil) == CatNonRetryable:
			pr.handleNonRetryable(w, ps, idx, resp, start, r.Method, target, bodyBytes, attempt, key)
			return

		case resp.StatusCode >= 500:
			pr.logs.Append(buildLogEntry(ps, key, idx, r.Method, target, resp.StatusCode, len(bodyBytes), start, attempt))
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
	defer resp.Body.Close()
	pool := ps.Pool
	keyCBs := ps.Proxy.keyCBs

	body, _ := io.ReadAll(resp.Body)
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
	slog.Warn("key rate limited", "provider", ps.Name, "key_index", idx, "key_name", pool.Name(idx), "status", resp.StatusCode, "cb_state", fmt.Sprintf("%d", keyCBs[idx].State()), "cb_retry", keyCBs[idx].Attempt(), "body_preview", MaskSensitiveData(string(body), 1024))
	pr.metrics.UpstreamErrors.WithLabelValues("rate_limited").Inc()

	if keyCBs[idx].State() == circuitbreaker.StatePermanent {
		slog.Warn("key quota exhausted, disabling permanently", "provider", ps.Name, "key_index", idx, "key_name", pool.Name(idx))
		pool.Disable(idx)
		if pool.ActiveCount() == 0 {
			return pr.writeAllKeysExhausted(w, ps, method, start)
		}
	}
	return false
}

// handleAuthRejected processes a 401 Unauthorized or 403 Forbidden response.
// It disables the key permanently and returns true if all keys are exhausted.
// When returning true, the error response has already been written to w.
func (pr *ProviderRouter) handleAuthRejected(w http.ResponseWriter, ps *ProviderState, idx int, resp *http.Response, start time.Time, method, target string, bodyBytes []byte) bool {
	defer resp.Body.Close()
	pool := ps.Pool
	keyCBs := ps.Proxy.keyCBs

	body, _ := io.ReadAll(resp.Body)
	pr.metrics.UpstreamErrors.WithLabelValues("auth_rejected").Inc()
	if keyCBs[idx].RecordAuthFailure() {
		pool.Disable(idx)
		keyCBs[idx].RecordPerma("auth_rejected")
		slog.Warn("key permanently disabled", "provider", ps.Name, "key_index", idx, "key_name", pool.Name(idx), "status", resp.StatusCode, "body_preview", MaskSensitiveData(string(body), 1024))
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
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	slog.Warn("upstream server error", "provider", ps.Name, "key_index", idx, "key_name", ps.Pool.Name(idx), "status", resp.StatusCode, "body_preview", MaskSensitiveData(string(body), 1024))
	pr.metrics.UpstreamErrors.WithLabelValues("server_error").Inc()
	ps.Proxy.upCB.RecordFailure()
}

// handleNonRetryable copies a non-retryable 4xx response through to the client
// without further retry attempts.
func (pr *ProviderRouter) handleNonRetryable(w http.ResponseWriter, ps *ProviderState, idx int, resp *http.Response, start time.Time, method, target string, bodyBytes []byte, attempt int, key string) {
	defer resp.Body.Close()
	utils.CopyHeaders(w.Header(), resp.Header)
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)

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
	defer resp.Body.Close()
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
}

// ── Extracted Utilities ───────────────────────────────

// readRequestBody reads and limits the request body to 10MB.
// Returns the body bytes, or nil and an error if the body is too large.
func readRequestBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	r.Body = http.MaxBytesReader(w, r.Body, 10<<20) // 10MB limit
	bodyBytes, err := io.ReadAll(r.Body)
	r.Body.Close()
	if err != nil {
		writeProxyError(w, http.StatusBadRequest, ErrorBadRequest, "request body too large or unreadable")
		return nil, err
	}
	return bodyBytes, nil
}

// buildTargetURL constructs the upstream URL based on path routing rules.
// Routes /genai/ paths to GenaiBase, everything else to TargetBase.
func buildTargetURL(cfg *config.Config, path, rawQuery string) string {
	if strings.Contains(path, "/genai/") {
		target := cfg.GenaiBase + path
		if rawQuery != "" {
			target += "?" + rawQuery
		}
		return target
	}
	if strings.HasSuffix(cfg.TargetBase, "/v1") && strings.HasPrefix(path, "/v1") {
		path = path[3:]
	}
	if rawQuery != "" {
		path += "?" + rawQuery
	}
	return cfg.TargetBase + path
}

// writeAllKeysExhausted writes the "all keys exhausted" error response and records metrics.
func (pr *ProviderRouter) writeAllKeysExhausted(w http.ResponseWriter, ps *ProviderState, method string, start time.Time) bool {
	writeProxyError(w, http.StatusServiceUnavailable, ErrorAllKeysInvalid, fmt.Sprintf("%s 所有 API Key 已熔断，请稍后重试", ps.Name))
	pr.recordProxyMetrics(method, "5xx", "", start)
	return true
}