package server

import (
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

	"alvus/internal/circuitbreaker"
	alvusmetrics "alvus/internal/metrics"
	"alvus/internal/utils"
)

// ErrorCode represents a machine-readable error category for proxy responses.
type ErrorCode string

const (
	ErrorBadRequest       ErrorCode = "BAD_REQUEST"
	ErrorUpstreamError    ErrorCode = "UPSTREAM_ERROR"
	ErrorAllKeysInvalid   ErrorCode = "ALL_KEYS_INVALID"
	ErrorExhaustedRetries ErrorCode = "EXHAUSTED_RETRIES"
)

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
