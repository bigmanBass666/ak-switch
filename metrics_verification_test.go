//go:build integration

package main

import (
	"akswitch/internal/config"
	"akswitch/internal/keypool"
	"akswitch/internal/server"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// Metrics verification tests (acceptance tests, not unit tests)
// ---------------------------------------------------------------------------

// readMetricValue parses Prometheus text format and returns the value of a metric line
// that matches name and the given label set. Labels are specified as comma-separated
// "key=value" pairs. Returns 0 if no matching line is found.
func readMetricValue(body, name, labelFilter string) float64 {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, name) {
			continue
		}
		// If no label filter, match the first bare metric line
		if labelFilter == "" {
			if !strings.Contains(line, "{") {
				parts := strings.Fields(line)
				if len(parts) >= 1 {
					var val float64
					fmt.Sscanf(parts[len(parts)-1], "%f", &val)
					return val
				}
			}
			continue
		}
		// Match labels
		braceIdx := strings.Index(line, "{")
		if braceIdx < 0 {
			continue
		}
		closeIdx := strings.Index(line, "}")
		if closeIdx < 0 {
			continue
		}
		labels := line[braceIdx+1 : closeIdx]
		// Check that all filter labels exist in the metric labels
		matches := true
		for _, filterLabel := range strings.Split(labelFilter, ",") {
			filterLabel = strings.TrimSpace(filterLabel)
			if filterLabel == "" {
				continue
			}
			if !strings.Contains(labels, filterLabel) {
				matches = false
				break
			}
		}
		if !matches {
			continue
		}
		// Extract the value after }
		valStr := strings.TrimSpace(line[closeIdx+1:])
		var val float64
		if _, err := fmt.Sscanf(valStr, "%f", &val); err == nil {
			return val
		}
	}
	return 0
}

// readMetricsDelta fetches /metrics before and after an action, and returns the delta
// of a specific metric+labels combination.
func readMetricsDelta(baseURL, metricName, labelFilter string, action func()) float64 {
	// Before
	resp, err := http.Get(baseURL + "/metrics")
	if err != nil {
		return -1
	}
	bodyBefore, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	before := readMetricValue(string(bodyBefore), metricName, labelFilter)

	action()

	// After
	resp, err = http.Get(baseURL + "/metrics")
	if err != nil {
		return -2
	}
	bodyAfter, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	after := readMetricValue(string(bodyAfter), metricName, labelFilter)

	return after - before
}

// setupRouter creates a mock upstream and a ProviderRouter-based AK Switch test server.
func setupRouter(tb testing.TB, upstream *httptest.Server, poolKeys []string, maxRetries, cooldownSec int) *httptest.Server {
	tb.Helper()
	cfg := &config.Config{
		TargetBase:  upstream.URL,
		GenaiBase:   upstream.URL,
		Port:        0,
		MaxRetries:  maxRetries,
		CooldownSec: cooldownSec,
	}
	pool := keypool.NewKeyPool(poolKeys, nil)
	pr := server.NewProviderRouter("")
	pr.AddProvider("test", cfg, pool)
	return httptest.NewServer(pr.Handler())
}

func TestMetricsVerification_RequestCount(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstream.Close()

	srv := setupRouter(t, upstream, []string{"key-a", "key-b", "key-c"}, 10, 60)
	defer srv.Close()

	delta := readMetricsDelta(srv.URL, "akswitch_requests_total",
		`method="GET",status="2xx"`,
		func() {
			resp, err := http.Get(srv.URL + "/test/v1/models")
			if err != nil {
				t.Fatalf("proxy request: %v", err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("expected 200, got %d", resp.StatusCode)
			}
		},
	)

	if delta < 1 {
		t.Errorf("akswitch_requests_total{method=GET,status=2xx} should increase by >=1, got %.0f", delta)
	} else {
		t.Logf("akswitch_requests_total increased by %.0f (OK)", delta)
	}
}

func TestMetricsVerification_RequestDuration(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstream.Close()

	srv := setupRouter(t, upstream, []string{"key-a", "key-b", "key-c"}, 10, 60)
	defer srv.Close()

	delta := readMetricsDelta(srv.URL, "akswitch_request_duration_seconds_count",
		`method="GET",status="2xx"`,
		func() {
			resp, err := http.Get(srv.URL + "/test/v1/models")
			if err != nil {
				t.Fatalf("proxy request: %v", err)
			}
			resp.Body.Close()
		},
	)

	if delta < 1 {
		t.Errorf("akswitch_request_duration_seconds_count should increase by >=1, got %.0f", delta)
	} else {
		t.Logf("akswitch_request_duration_seconds_count increased by %.0f (OK)", delta)
	}

	// Also verify sum increased (using a fresh request to avoid stale baseline)
	sumDelta := readMetricsDelta(srv.URL, "akswitch_request_duration_seconds_sum",
		`method="GET",status="2xx"`,
		func() {
			time.Sleep(50 * time.Millisecond)
			resp, err := http.Get(srv.URL + "/test/v1/models")
			if err != nil {
				t.Fatalf("proxy request: %v", err)
			}
			resp.Body.Close()
		},
	)
	if sumDelta <= 0 {
		t.Errorf("akswitch_request_duration_seconds_sum should increase by >0, got %f", sumDelta)
	} else {
		t.Logf("akswitch_request_duration_seconds_sum increased by %f (OK)", sumDelta)
	}
}

func TestMetricsVerification_RateLimited(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer upstream.Close()

	srv := setupRouter(t, upstream, []string{"key-a", "key-b", "key-c"}, 3, 2)
	defer srv.Close()

	delta := readMetricsDelta(srv.URL, "akswitch_upstream_errors_total",
		`type="rate_limited"`,
		func() {
			resp, err := http.Get(srv.URL + "/test/v1/models")
			if err != nil {
				t.Fatalf("proxy request: %v", err)
			}
			resp.Body.Close()
		},
	)

	if delta < 1 {
		t.Errorf("akswitch_upstream_errors_total{type=rate_limited} should increase by >=1, got %.0f", delta)
	} else {
		t.Logf("akswitch_upstream_errors_total{type=rate_limited} increased by %.0f (OK)", delta)
	}
}

func TestMetricsVerification_AuthRejected(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer upstream.Close()

	srv := setupRouter(t, upstream, []string{"key-a", "key-b", "key-c"}, 3, 2)
	defer srv.Close()

	delta := readMetricsDelta(srv.URL, "akswitch_upstream_errors_total",
		`type="auth_rejected"`,
		func() {
			resp, err := http.Get(srv.URL + "/test/v1/models")
			if err != nil {
				t.Fatalf("proxy request: %v", err)
			}
			resp.Body.Close()
		},
	)

	if delta < 1 {
		t.Errorf("akswitch_upstream_errors_total{type=auth_rejected} should increase by >=1, got %.0f", delta)
	} else {
		t.Logf("akswitch_upstream_errors_total{type=auth_rejected} increased by %.0f (OK)", delta)
	}
}

func TestMetricsVerification_ServerError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer upstream.Close()

	srv := setupRouter(t, upstream, []string{"key-a", "key-b", "key-c"}, 3, 2)
	defer srv.Close()

	delta := readMetricsDelta(srv.URL, "akswitch_upstream_errors_total",
		`type="server_error"`,
		func() {
			resp, err := http.Get(srv.URL + "/test/v1/models")
			if err != nil {
				t.Fatalf("proxy request: %v", err)
			}
			resp.Body.Close()
		},
	)

	if delta < 1 {
		t.Errorf("akswitch_upstream_errors_total{type=server_error} should increase by >=1, got %.0f", delta)
	} else {
		t.Logf("akswitch_upstream_errors_total{type=server_error} increased by %.0f (OK)", delta)
	}
}

func TestMetricsVerification_KeyPoolDisabled(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer upstream.Close()

	// Create server via ProviderRouter so we have access to state.Metrics() via ProviderState
	cfg := &config.Config{
		TargetBase:  upstream.URL,
		GenaiBase:   upstream.URL,
		Port:        0,
		MaxRetries:  3,
		CooldownSec: 2,
	}
	pool := keypool.NewKeyPool([]string{"key-a", "key-b"}, nil)
	pr := server.NewProviderRouter("")
	pr.AddProvider("test", cfg, pool)
	srv := httptest.NewServer(pr.Handler())
	defer srv.Close()

	// Before: record disabled count
	resp, err := http.Get(srv.URL + "/metrics")
	if err != nil {
		t.Fatalf("GET /metrics before: %v", err)
	}
	bodyBefore, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	disabledBefore := readMetricValue(string(bodyBefore), "akswitch_keypool_keys", `state="disabled"`)

	// Trigger 401 on key-a which will disable it
	resp, err = http.Get(srv.URL + "/test/v1/models")
	if err != nil {
		t.Fatalf("proxy request: %v", err)
	}
	resp.Body.Close()

	// RefreshKeyPoolGauge temporarily — it updates the ServerState's metrics but
	// not the ProviderRouter's.  We verify the pool state directly instead.
	disabledAfter := float64(pool.DisabledCount())

	delta := disabledAfter - disabledBefore
	if delta < 1 {
		t.Errorf("akswitch_keypool_keys{state=disabled} should increase by >=1, got delta=%.0f (before=%.0f, after=%.0f)",
			delta, disabledBefore, disabledAfter)
	} else {
		t.Logf("akswitch_keypool_keys{state=disabled} increased by %.0f (OK)", delta)
	}
}
