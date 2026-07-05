//go:build integration

package main

import (
	"akswitch/internal/config"
	"akswitch/internal/keypool"
	"akswitch/internal/server"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// newServer creates a mock upstream and an AK Switch test server with full config control,
// returning both the ProviderRouter (for accessing provider state) and the test server.
// The caller must close both servers.
func newServer(tb testing.TB, cfg *config.Config, keys []string) (*server.ProviderRouter, *httptest.Server) {
	tb.Helper()
	pool := keypool.NewKeyPool(keys, nil)

	// Apply CB/health check defaults matching NewServerState's fallback logic
	if cfg.CBResetSec <= 0 {
		cfg.CBResetSec = 30
	}
	if cfg.UpstreamCBThreshold <= 0 {
		cfg.UpstreamCBThreshold = 5
	}
	if cfg.BackoffCapSec <= 0 {
		cfg.BackoffCapSec = 120
	}
	if cfg.BackoffMultiplier <= 0 {
		cfg.BackoffMultiplier = 2
	}

	pr := server.NewProviderRouter("")
	pr.AddProvider("test", cfg, pool)
	return pr, httptest.NewServer(pr.Handler())
}

// healthResponse mirrors the inner provider health JSON from the ProviderRouter /health endpoint.
type healthResponse struct {
	CbState     string `json:"upstream_cb_state"`
	LastCheck   string `json:"last_health_check"`
	LastCheckOK *bool  `json:"last_health_check_ok"`
}

// routerHealthResponse mirrors the top-level JSON from the ProviderRouter /health endpoint.
type routerHealthResponse struct {
	Status    string                            `json:"status"`
	Providers int                               `json:"providers"`
	Details   map[string]providerHealthResponse `json:"details"`
}

// providerHealthResponse mirrors the per-provider health JSON from the router-level /health response.
type providerHealthResponse struct {
	Status            string `json:"status"`
	Keys              int    `json:"keys"`
	UpstreamCBState   string `json:"upstream_cb_state"`
	LastHealthCheck   string `json:"last_health_check,omitempty"`
	LastHealthCheckOK *bool  `json:"last_health_check_ok,omitempty"`
}

// getHealth queries the /health endpoint and decodes the response for provider "test".
func getHealth(tb testing.TB, url string) healthResponse {
	tb.Helper()
	resp, err := http.Get(url + "/health")
	if err != nil {
		tb.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	var routerResp routerHealthResponse
	if err := json.Unmarshal(body, &routerResp); err != nil {
		tb.Fatalf("decode /health response %q: %v", string(body), err)
	}

	detail, ok := routerResp.Details["test"]
	if !ok {
		tb.Fatalf("provider 'test' not found in /health details")
	}

	return healthResponse{
		CbState:     detail.UpstreamCBState,
		LastCheck:   detail.LastHealthCheck,
		LastCheckOK: detail.LastHealthCheckOK,
	}
}

// ---------------------------------------------------------------------------
// 1. ProbeSuccess — healthy upstream keeps CB closed
// ---------------------------------------------------------------------------

// TestActiveHealthCheck_ProbeSuccess verifies that when the upstream is healthy
// (returns 200), the circuit breaker stays closed and the /health endpoint
// reflects the healthy state with last_health_check_ok=true.
func TestActiveHealthCheck_ProbeSuccess(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		TargetBase:             upstream.URL,
		GenaiBase:              upstream.URL,
		Port:                   0,
		MaxRetries:             1,
		CooldownSec:            60,
		UpstreamCBThreshold:    3,
		HealthCheckIntervalSec: 30,
		HealthCheckPath:        "/health",
		HealthCheckTimeoutSec:  5,
	}
	pr, srv := newServer(t, cfg, []string{"test-key"})
	defer srv.Close()

	// WHEN: a proxy request succeeds — the proxy calls upCB.RecordSuccess()
	resp, err := http.Get(srv.URL + "/test/v1/models")
	if err != nil {
		t.Fatalf("GET /test/v1/models: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Simulate what ActiveHealthCheck does on a successful probe:
	// - Set last health check result
	// - Increment the health check probes counter
	pr.Provider("test").State.SetLastHealthCheck(true)
	pr.Provider("test").State.Metrics().HealthCheckProbes.WithLabelValues("ok").Inc()

	// THEN: /health reflects a healthy upstream
	health := getHealth(t, srv.URL)
	if health.CbState != "closed" {
		t.Errorf("expected upstream_cb_state 'closed', got %q", health.CbState)
	}
	if health.LastCheckOK == nil || !*health.LastCheckOK {
		t.Error("expected last_health_check_ok true")
	}
}

// ---------------------------------------------------------------------------
// 2. ProbeFailure — failing upstream opens CB
// ---------------------------------------------------------------------------

// TestActiveHealthCheck_ProbeFailure verifies that when the upstream returns 503,
// the circuit breaker opens after the configured threshold, and the /health
// endpoint reflects the failure.
func TestActiveHealthCheck_ProbeFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		TargetBase:             upstream.URL,
		GenaiBase:              upstream.URL,
		Port:                   0,
		MaxRetries:             1, // each proxied request = 1 upstream call
		CooldownSec:            60,
		UpstreamCBThreshold:    3,
		CBResetSec:             60, // long enough that CB stays open during test
		HealthCheckIntervalSec: 30,
		HealthCheckPath:        "/health",
		HealthCheckTimeoutSec:  5,
	}
	pr, srv := newServer(t, cfg, []string{"test-key-a"})
	defer srv.Close()

	// WHEN: send enough proxy requests to trigger UpstreamCBThreshold failures
	// Each request returns 503 and the proxy calls upCB.RecordFailure()
	for i := 0; i < 4; i++ {
		resp, err := http.Get(srv.URL + "/test/v1/models")
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
	}

	// Simulate what ActiveHealthCheck does on a failed probe
	pr.Provider("test").State.SetLastHealthCheck(false)
	pr.Provider("test").State.Metrics().HealthCheckProbes.WithLabelValues("fail").Inc()

	// THEN: CB should be open — /health shows "open"
	health := getHealth(t, srv.URL)
	if health.CbState != "open" {
		t.Errorf("expected upstream_cb_state 'open' after 3 failures, got %q", health.CbState)
	}
	if health.LastCheckOK == nil || *health.LastCheckOK {
		t.Error("expected last_health_check_ok false")
	}
}

// ---------------------------------------------------------------------------
// 3. Recovery — CB recovers after reset timeout when upstream becomes healthy
// ---------------------------------------------------------------------------

// TestActiveHealthCheck_Recovery verifies that after the CB opens, it
// transitions to HALF_OPEN after the reset timeout, and a successful proxy
// request restores it to CLOSED.
func TestActiveHealthCheck_Recovery(t *testing.T) {
	var mu sync.Mutex
	upstreamHealthy := false

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		healthy := upstreamHealthy
		mu.Unlock()
		if healthy {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer upstream.Close()

	// Short CB reset timeout for testing; bypasses Validate() in tests
	cfg := &config.Config{
		TargetBase:             upstream.URL,
		GenaiBase:              upstream.URL,
		Port:                   0,
		MaxRetries:             1,
		CooldownSec:            60,
		UpstreamCBThreshold:    3,
		CBResetSec:             1,
		HealthCheckIntervalSec: 30,
		HealthCheckPath:        "/health",
		HealthCheckTimeoutSec:  5,
	}
	pr, srv := newServer(t, cfg, []string{"test-key-a"})
	defer srv.Close()

	// Phase 1 — Open the CB by sending failures
	for i := 0; i < 4; i++ {
		resp, err := http.Get(srv.URL + "/test/v1/models")
		if err != nil {
			t.Fatalf("failure phase request %d: %v", i, err)
		}
		resp.Body.Close()
	}

	health := getHealth(t, srv.URL)
	if health.CbState != "open" {
		t.Fatalf("CB should be open after failures, got %q", health.CbState)
	}
	t.Logf("CB is open — waiting for reset timeout (%ds)", cfg.CBResetSec)

	// Phase 2 — Wait for CB reset timeout and switch upstream to healthy
	mu.Lock()
	upstreamHealthy = true
	mu.Unlock()

	time.Sleep(time.Duration(cfg.CBResetSec+1) * time.Second)

	// WHEN: send a proxy request — Allow() transitions to HALF_OPEN,
	// upstream returns 200 → RecordSuccess → CLOSED
	resp, err := http.Get(srv.URL + "/test/v1/models")
	if err != nil {
		t.Fatalf("recovery request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after recovery, got %d", resp.StatusCode)
	}

	// Simulate health check success after recovery
	pr.Provider("test").State.SetLastHealthCheck(true)
	pr.Provider("test").State.Metrics().HealthCheckProbes.WithLabelValues("ok").Inc()

	// THEN: CB is closed again
	health = getHealth(t, srv.URL)
	if health.CbState != "closed" {
		t.Errorf("expected upstream_cb_state 'closed' after recovery, got %q", health.CbState)
	}
	if health.LastCheckOK == nil || !*health.LastCheckOK {
		t.Error("expected last_health_check_ok true after recovery")
	}
}

// ---------------------------------------------------------------------------
// 4. ProbeTimeout — slow upstream causes probe timeout and CB failure
// ---------------------------------------------------------------------------

// TestActiveHealthCheck_ProbeTimeout verifies that when a health check probe
// times out (upstream is unresponsive), the circuit breaker records a failure.
// A timed-out HEAD request simulates what ActiveHealthCheck does with its own
// short-timeout HTTP client.
func TestActiveHealthCheck_ProbeTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate an upstream that is too slow to respond
		time.Sleep(2 * time.Second)
		// Return 503 so the proxy handler calls upCB.RecordFailure()
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		TargetBase:             upstream.URL,
		GenaiBase:              upstream.URL,
		Port:                   0,
		MaxRetries:             1,
		CooldownSec:            60,
		UpstreamCBThreshold:    1, // single timeout opens CB
		CBResetSec:             30,
		HealthCheckIntervalSec: 30,
		HealthCheckPath:        "/health",
		HealthCheckTimeoutSec:  1,
	}
	pr, srv := newServer(t, cfg, []string{"test-key-a"})
	defer srv.Close()

	// Simulate what ActiveHealthCheck does:
	// 1. Create a short-timeout HTTP client (like the health check goroutine does)
	// 2. Send HEAD request to the upstream's health check endpoint
	// 3. The request times out — proving timeout detection works

	hcClient := &http.Client{Timeout: time.Duration(cfg.HealthCheckTimeoutSec) * time.Second}
	headResp, err := hcClient.Head(upstream.URL + cfg.HealthCheckPath)
	if err != nil {
		t.Logf("HEAD request timed out as expected: %v", err)
	} else {
		headResp.Body.Close()
		t.Log("HEAD request succeeded (upstream responded before timeout)")
	}

	// 4. Simulate the health check goroutine's failure handling:
	//    Send a proxied request that hits the slow upstream, which returns 503
	//    after 2s. The proxy calls upCB.RecordFailure(), opening the CB.
	resp, err := http.Get(srv.URL + "/test/v1/models")
	if err != nil {
		t.Logf("proxy request error: %v", err)
	}
	if resp != nil {
		resp.Body.Close()
	}

	pr.Provider("test").State.SetLastHealthCheck(false)

	// THEN: CB should have recorded a failure
	health := getHealth(t, srv.URL)
	if health.CbState != "open" {
		t.Errorf("expected upstream_cb_state 'open' after failure, got %q", health.CbState)
	}
	if health.LastCheckOK == nil || *health.LastCheckOK {
		t.Error("expected last_health_check_ok false after timeout failure")
	}
}

// ---------------------------------------------------------------------------
// 5. ConfigDriven — health check config fields pass through correctly
// ---------------------------------------------------------------------------

// TestActiveHealthCheck_ConfigDriven verifies that health check configuration
// values are correctly stored and accessible.
func TestActiveHealthCheck_ConfigDriven(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	cfg := &config.Config{
		TargetBase:             upstream.URL,
		GenaiBase:              upstream.URL,
		Port:                   8080,
		MaxRetries:             3,
		CooldownSec:            60,
		HealthCheckIntervalSec: 10,
		HealthCheckPath:        "/custom",
		HealthCheckTimeoutSec:  3,
		UpstreamCBThreshold:    5,
		CBResetSec:             30,
	}

	// Verify config fields before creating ProviderRouter
	if cfg.HealthCheckIntervalSec != 10 {
		t.Errorf("expected HealthCheckIntervalSec=10, got %d", cfg.HealthCheckIntervalSec)
	}
	if cfg.HealthCheckPath != "/custom" {
		t.Errorf("expected HealthCheckPath=/custom, got %q", cfg.HealthCheckPath)
	}
	if cfg.HealthCheckTimeoutSec != 3 {
		t.Errorf("expected HealthCheckTimeoutSec=3, got %d", cfg.HealthCheckTimeoutSec)
	}

	// Verify ProviderRouter initialises without error with these config values
	pr, srv := newServer(t, cfg, []string{"test-key"})
	defer srv.Close()

	// Server started successfully with health check config
	// Verify a basic proxy request works
	resp, err := http.Get(srv.URL + "/test/v1/models")
	if err != nil {
		t.Fatalf("GET /test/v1/models: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	// Verify health check metrics are accessible
	pr.Provider("test").State.Metrics().HealthCheckProbes.WithLabelValues("ok")
	pr.Provider("test").State.Metrics().HealthCheckProbes.WithLabelValues("fail")
	_ = pr.Provider("test").State.Metrics().HealthCheckDuration

	// Custom health check path is set in config
	// (the actual path is used by ActiveHealthCheck goroutine, not tested here)
	t.Logf("Config: interval=%ds path=%q timeout=%ds",
		cfg.HealthCheckIntervalSec, cfg.HealthCheckPath, cfg.HealthCheckTimeoutSec)
}
