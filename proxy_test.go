package main

import (
	"akswitch/internal/config"
	"akswitch/internal/keypool"
	"akswitch/internal/server"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// setupServer creates a mock upstream and an AK Switch test server, returning both.
// The caller must close both servers.
func setupServer(tb testing.TB, upstream *httptest.Server, poolKeys []string, maxRetries, cooldownSec int) *httptest.Server {
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

// retryHandler returns a mock upstream handler that fails the first N calls
// and then returns a success status for all subsequent calls.
func retryHandler(failStatus, successStatus int, numFailures int, successBody string) http.HandlerFunc {
	var mu sync.Mutex
	var callCount int
	return func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count := callCount
		callCount++
		mu.Unlock()
		if count < numFailures {
			w.WriteHeader(failStatus)
			return
		}
		if successBody != "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(successStatus)
			w.Write([]byte(successBody))
		} else {
			w.WriteHeader(successStatus)
		}
	}
}

// ---------------------------------------------------------------------------
// 1. Basic forward
// ---------------------------------------------------------------------------

func TestProxyBasicForward(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstream.Close()

	srv := setupServer(t, upstream, []string{"test-key-a", "test-key-b", "test-key-c"}, 10, 60)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/test/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	var result map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if result["status"] != "ok" {
		t.Errorf(`expected "status":"ok", got %q`, result["status"])
	}
}

// ---------------------------------------------------------------------------
// 2. Auth header format
// ---------------------------------------------------------------------------

func TestProxyAuthHeader(t *testing.T) {
	var mu sync.Mutex
	var seenAuth string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seenAuth = r.Header.Get("Authorization")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	srv := setupServer(t, upstream, []string{"test-key-a", "test-key-b", "test-key-c"}, 10, 60)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/test/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	resp.Body.Close()

	mu.Lock()
	auth := seenAuth
	mu.Unlock()

	if !strings.HasPrefix(auth, "Bearer ") {
		t.Errorf("Authorization header should start with 'Bearer ', got %q", auth)
	}
	if len(auth) <= len("Bearer ") {
		t.Errorf("Authorization header %q is too short", auth)
	}
}

// ---------------------------------------------------------------------------
// 3. Key rotation across requests
// ---------------------------------------------------------------------------

func TestProxyKeyRotation(t *testing.T) {
	var mu sync.Mutex
	var auths []string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auths = append(auths, r.Header.Get("Authorization"))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	srv := setupServer(t, upstream, []string{"test-key-a", "test-key-b", "test-key-c"}, 10, 60)
	defer srv.Close()

	for i := 0; i < 2; i++ {
		resp, err := http.Get(srv.URL + "/test/v1/models")
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		resp.Body.Close()
	}

	mu.Lock()
	keys := make([]string, len(auths))
	copy(keys, auths)
	mu.Unlock()

	if len(keys) < 2 {
		t.Fatalf("expected at least 2 auth headers, got %d", len(keys))
	}
	if keys[0] == keys[1] {
		t.Errorf("expected different keys in rotation, both are %q", keys[0])
	}
}

// ---------------------------------------------------------------------------
// 4. Retry after 429 (cooldown)
// ---------------------------------------------------------------------------

func TestProxyRetryAfter429(t *testing.T) {
	upstream := httptest.NewServer(retryHandler(
		http.StatusTooManyRequests, http.StatusOK, 1, `{"status":"ok"}`,
	))
	defer upstream.Close()

	srv := setupServer(t, upstream, []string{"test-key-a", "test-key-b", "test-key-c"}, 10, 60)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/test/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK after 429 retry, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// 5. Disable key on 401 and fall through to next key
// ---------------------------------------------------------------------------

func TestProxyDisableOn401(t *testing.T) {
	// Return 401 for "test-key-a" (first in round-robin), 200 for others
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		if strings.Contains(auth, "test-key-a") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstream.Close()

	srv := setupServer(t, upstream, []string{"test-key-a", "test-key-b", "test-key-c"}, 10, 60)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/test/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK after 401 retry, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// 6. Retry on 503
// ---------------------------------------------------------------------------

func TestProxyRetryOn503(t *testing.T) {
	upstream := httptest.NewServer(retryHandler(
		http.StatusServiceUnavailable, http.StatusOK, 1, `{"status":"ok"}`,
	))
	defer upstream.Close()

	srv := setupServer(t, upstream, []string{"test-key-a", "test-key-b", "test-key-c"}, 10, 60)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/test/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK after 503 retry, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// 7. All keys exhausted (all return 429)
// ---------------------------------------------------------------------------

func TestProxyAllKeysExhausted(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer upstream.Close()

	// With 3 keys and MaxRetries=3, each key gets exactly one attempt,
	// all keys briefly cooled -> loop ends -> 503.
	srv := setupServer(t, upstream, []string{"key-a", "key-b", "key-c"}, 3, 2)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/test/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 Service Unavailable after exhaustion, got %d", resp.StatusCode)
	}

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode JSON error response: %v", err)
	}
	if body.Error.Code != "EXHAUSTED_RETRIES" {
		t.Errorf("expected error.code EXHAUSTED_RETRIES, got %q", body.Error.Code)
	}
}

// ---------------------------------------------------------------------------
// 8. SSE streaming
// ---------------------------------------------------------------------------

func TestProxySSEStream(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flusher", http.StatusInternalServerError)
			return
		}
		for i := 0; i < 3; i++ {
			fmt.Fprintf(w, "data: {\"x\":%d}\n\n", i)
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	srv := setupServer(t, upstream, []string{"test-key-a", "test-key-b", "test-key-c"}, 10, 60)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/test/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}

	bodyStr := string(body)

	// Count data: lines (robust against buffering)
	lines := strings.Split(bodyStr, "\n")
	var dataLines []string
	for _, line := range lines {
		if strings.HasPrefix(line, "data: ") {
			dataLines = append(dataLines, line)
		}
	}
	if len(dataLines) != 3 {
		t.Fatalf("expected 3 SSE data lines, got %d. Full body: %q", len(dataLines), bodyStr)
	}

	for i, line := range dataLines {
		expected := fmt.Sprintf(`data: {"x":%d}`, i)
		if line != expected {
			t.Errorf("data line %d: expected %q, got %q", i, expected, line)
		}
	}
}

// ---------------------------------------------------------------------------
// 9. Empty response (204)
// ---------------------------------------------------------------------------

func TestProxyEmptyResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	srv := setupServer(t, upstream, []string{"test-key-a", "test-key-b", "test-key-c"}, 10, 60)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/test/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("expected 204 No Content, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// 10. Request body passthrough
// ---------------------------------------------------------------------------

func TestProxyRequestBodyPassthrough(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.Write(body)
	}))
	defer upstream.Close()

	srv := setupServer(t, upstream, []string{"test-key-a", "test-key-b", "test-key-c"}, 10, 60)
	defer srv.Close()

	payload := `{"hello":"world"}`
	resp, err := http.Post(srv.URL+"/test/v1/models", "application/json", bytes.NewReader([]byte(payload)))
	if err != nil {
		t.Fatalf("POST /v1/models: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if strings.TrimSpace(string(body)) != payload {
		t.Errorf("expected body %q, got %q", payload, string(body))
	}
}

// ---------------------------------------------------------------------------
// 11. Key management (add key, check count, proxy through)
// ---------------------------------------------------------------------------

func TestProxyWithKeyManagement(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstream.Close()

	// Create AK Switch with 1 initial key (must have at least 1 to avoid panic in Next())
	cfg := &config.Config{
		TargetBase:  upstream.URL,
		GenaiBase:   upstream.URL,
		Port:        8080,
		MaxRetries:  10,
		CooldownSec: 60,
		AdminToken:  "",
		Keys:        []string{"initial-key"},
	}
	pool := keypool.NewKeyPool([]string{"initial-key"}, nil)
	pr := server.NewProviderRouter("")
	pr.AddProvider("test", cfg, pool)
	srv := httptest.NewServer(pr.Handler())
	defer srv.Close()

	// Step 1: POST /api/keys to add a new key
	addBody := `{"key":"added-key-456"}`
	resp, err := http.Post(srv.URL+"/api/keys", "application/json", bytes.NewReader([]byte(addBody)))
	if err != nil {
		t.Fatalf("POST /api/keys: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/keys expected 200, got %d", resp.StatusCode)
	}

	// Step 2: GET /api/keys to verify count increased
	resp, err = http.Get(srv.URL + "/api/keys")
	if err != nil {
		t.Fatalf("GET /api/keys: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/keys expected 200, got %d", resp.StatusCode)
	}

	var keys []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&keys); err != nil {
		t.Fatalf("failed to decode GET /api/keys response: %v", err)
	}
	if len(keys) != 2 {
		t.Fatalf("expected 2 keys after adding one, got %d", len(keys))
	}

	// Step 3: Proxy request still works with the updated pool
	resp, err = http.Get(srv.URL + "/test/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models after key management: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after key management, got %d", resp.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// 12. MaxRetries config respected
// ---------------------------------------------------------------------------

func TestProxyMaxRetriesConfig(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer upstream.Close()

	// MaxRetries=2 -> only 2 attempts, then 503
	srv := setupServer(t, upstream, []string{"test-key-a", "test-key-b", "test-key-c"}, 2, 60)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/test/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 after exhausting MaxRetries=2, got %d", resp.StatusCode)
	}

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode JSON error response: %v", err)
	}
	if body.Error.Code != "EXHAUSTED_RETRIES" {
		t.Errorf("expected error.code EXHAUSTED_RETRIES, got %q", body.Error.Code)
	}
}

// ---------------------------------------------------------------------------
// 13. Concurrent requests — all succeed
// ---------------------------------------------------------------------------

func TestProxyConcurrentRequests(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstream.Close()

	srv := setupServer(t, upstream, []string{"key-a", "key-b", "key-c", "key-d", "key-e"}, 10, 60)
	defer srv.Close()

	const concurrency = 20
	var wg sync.WaitGroup
	errs := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			resp, err := http.Get(srv.URL + "/test/v1/models")
			if err != nil {
				errs <- fmt.Errorf("req %d: %v", id, err)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				errs <- fmt.Errorf("req %d: expected 200, got %d", id, resp.StatusCode)
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	var failures []string
	for e := range errs {
		failures = append(failures, e.Error())
	}
	if len(failures) > 0 {
		t.Fatalf("%d/%d requests failed:\n%s", len(failures), concurrency, strings.Join(failures, "\n"))
	}
}

// ---------------------------------------------------------------------------
// 14. Concurrent requests — key rotation under load
// ---------------------------------------------------------------------------

func TestProxyConcurrentKeyRotation(t *testing.T) {
	var mu sync.Mutex
	authSet := make(map[string]int)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		authSet[r.Header.Get("Authorization")]++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	srv := setupServer(t, upstream, []string{"key-a", "key-b", "key-c"}, 10, 60)
	defer srv.Close()

	const concurrency = 30
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get(srv.URL + "/test/v1/models")
			if err != nil {
				t.Errorf("request error: %v", err)
				return
			}
			resp.Body.Close()
		}()
	}
	wg.Wait()

	mu.Lock()
	keys := make([]string, 0, len(authSet))
	for k := range authSet {
		keys = append(keys, k)
	}
	uniqueCount := len(keys)
	mu.Unlock()

	if uniqueCount < 2 {
		t.Fatalf("expected at least 2 different keys under concurrent load (%d concurrent), got %d: %v", concurrency, uniqueCount, keys)
	}
	t.Logf("Concurrent key rotation: %d different keys used across %d requests", uniqueCount, concurrency)
}

// ---------------------------------------------------------------------------
// 15. Concurrent requests with interleaved 429 cooldown
// ---------------------------------------------------------------------------

func TestProxyConcurrentWithCooldown(t *testing.T) {
	var mu sync.Mutex
	reqCount := 0

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count := reqCount
		reqCount++
		mu.Unlock()
		if count%3 == 0 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	srv := setupServer(t, upstream, []string{"key-a", "key-b", "key-c", "key-d", "key-e"}, 10, 2)
	defer srv.Close()

	const concurrency = 15
	var wg sync.WaitGroup
	errs := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			resp, err := http.Get(srv.URL + "/test/v1/models")
			if err != nil {
				errs <- fmt.Errorf("req %d: %v", id, err)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				errs <- fmt.Errorf("req %d: expected 200, got %d", id, resp.StatusCode)
			}
		}(i)
	}
	wg.Wait()
	close(errs)

	var failures []string
	for e := range errs {
		failures = append(failures, e.Error())
	}
	if len(failures) > 0 {
		t.Fatalf("%d/%d requests failed with 429 cooldown:\n%s", len(failures), concurrency, strings.Join(failures, "\n"))
	}
}

// ---------------------------------------------------------------------------
// 16. Sensitive headers filtered from upstream request
// ---------------------------------------------------------------------------

func TestProxyFilterSensitiveHeaders(t *testing.T) {
	var mu sync.Mutex
	var receivedHeaders http.Header

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		receivedHeaders = r.Header.Clone()
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	srv := setupServer(t, upstream, []string{"test-key"}, 10, 60)
	defer srv.Close()

	req, err := http.NewRequest("GET", srv.URL+"/test/v1/models", nil)
	if err != nil {
		t.Fatalf("failed to create request: %v", err)
	}
	req.Header.Set("X-Admin-Token", "my-secret-admin-token")
	req.Header.Set("Cookie", "session=abc123")
	req.Header.Set("Proxy-Authorization", "Basic dXNlcjpwYXNz")
	req.Header.Set("X-Custom-Header", "should-pass")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	resp.Body.Close()

	mu.Lock()
	headers := receivedHeaders
	mu.Unlock()

	if headers.Get("X-Admin-Token") != "" {
		t.Errorf("X-Admin-Token was forwarded to upstream (value=%q)", headers.Get("X-Admin-Token"))
	}
	if headers.Get("Cookie") != "" {
		t.Errorf("Cookie was forwarded to upstream (value=%q)", headers.Get("Cookie"))
	}
	if headers.Get("Proxy-Authorization") != "" {
		t.Errorf("Proxy-Authorization was forwarded to upstream (value=%q)", headers.Get("Proxy-Authorization"))
	}
	if h := headers.Get("Authorization"); h == "" {
		t.Error("Authorization header was stripped entirely")
	} else if !strings.HasPrefix(h, "Bearer ") {
		t.Errorf("Authorization header should start with 'Bearer ', got %q", h)
	}
	if headers.Get("X-Custom-Header") != "should-pass" {
		t.Errorf("X-Custom-Header was filtered out (should have passed through)")
	}
	if headers.Get("Accept") != "application/json" {
		t.Errorf("Accept header was filtered out (should have passed through)")
	}
}

// ---------------------------------------------------------------------------
// 17. Verify slog output format — proxy request produces structured JSON-like log
// ---------------------------------------------------------------------------

func TestProxySlogOutput(t *testing.T) {
	var buf bytes.Buffer
	origHandler := slog.Default().Handler()
	t.Cleanup(func() { slog.SetDefault(slog.New(origHandler)) })

	handler := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	slog.SetDefault(slog.New(handler))

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	srv := setupServer(t, upstream, []string{"test-key-a", "test-key-b"}, 10, 60)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/test/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	resp.Body.Close()

	output := buf.String()

	// Log format must be slog structured (key=value, not printf-style)
	if output == "" {
		t.Fatal("slog output is empty — no log was written")
	}

	// Must contain INFO level
	if !strings.Contains(output, "INFO") {
		t.Errorf("expected slog INFO level in output, got: %s", output)
	}

	// Must contain structured key=value fields
	for _, key := range []string{"method=GET", "url", "status=200"} {
		if !strings.Contains(output, key) {
			t.Errorf("expected slog field %q in output:\n%s", key, output)
		}
	}
	// key_index must exist but value is implementation-dependent
	if !strings.Contains(output, "key_index=") {
		t.Errorf("expected key_index field in output:\n%s", output)
	}

	// Must NOT contain printf-style log format
	if strings.Contains(output, "→ %s %s") || strings.Contains(output, "log.Printf") {
		t.Errorf("output appears to contain old-style log.Printf format:\n%s", output)
	}
}

// ---------------------------------------------------------------------------
// 18. Error handling — BadRequest (body too large)
// ---------------------------------------------------------------------------

func TestProxyError_BadRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	srv := setupServer(t, upstream, []string{"test-key-a"}, 10, 60)
	defer srv.Close()

	// 11MB body exceeds the 10MB MaxBytesReader limit
	largeBody := make([]byte, 11<<20)
	req, err := http.NewRequest("POST", srv.URL+"/test/v1/chat/completions", bytes.NewReader(largeBody))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected 400 Bad Request, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
	if body.Error.Code != "BAD_REQUEST" {
		t.Errorf("expected error.code BAD_REQUEST, got %q", body.Error.Code)
	}
	if body.Error.Message == "" {
		t.Error("expected non-empty error.message")
	}
}

// ---------------------------------------------------------------------------
// 19. Error handling — AllKeysInvalid (single key disabled by 401)
// ---------------------------------------------------------------------------

func TestProxyError_AllKeysInvalid(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer upstream.Close()

	// Only 1 key — after 401 it gets disabled, ActiveCount == 0
	srv := setupServer(t, upstream, []string{"single-key"}, 10, 60)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/test/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 Service Unavailable, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
	if body.Error.Code != "ALL_KEYS_INVALID" {
		t.Errorf("expected error.code ALL_KEYS_INVALID, got %q", body.Error.Code)
	}
}

// ---------------------------------------------------------------------------
// 20. Error handling — ExhaustedRetries (all keys rate-limited)
// ---------------------------------------------------------------------------

func TestProxyError_ExhaustedRetries(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer upstream.Close()

	srv := setupServer(t, upstream, []string{"key-a", "key-b", "key-c"}, 3, 2)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/test/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 Service Unavailable, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
	if body.Error.Code != "EXHAUSTED_RETRIES" {
		t.Errorf("expected error.code EXHAUSTED_RETRIES, got %q", body.Error.Code)
	}
}

// ---------------------------------------------------------------------------
// 21. Error handling — UpstreamError (invalid target URL)
// ---------------------------------------------------------------------------

func TestProxyError_UpstreamError(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	// Set TargetBase to something that makes NewRequestWithContext fail.
	// An invalid scheme causes http.NewRequestWithContext to return an error.
	cfg := &config.Config{
		TargetBase:  "://invalid",
		GenaiBase:   "://invalid",
		Port:        0,
		MaxRetries:  3,
		CooldownSec: 60,
	}
	pool := keypool.NewKeyPool([]string{"test-key-a"}, nil)
	pr := server.NewProviderRouter("")
	pr.AddProvider("test", cfg, pool)
	srv := httptest.NewServer(pr.Handler())
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/test/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 Internal Server Error, got %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Fatalf("expected Content-Type application/json, got %q", ct)
	}

	var body struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode JSON response: %v", err)
	}
	if body.Error.Code != "UPSTREAM_ERROR" {
		t.Errorf("expected error.code UPSTREAM_ERROR, got %q", body.Error.Code)
	}
}

// ---------------------------------------------------------------------------
// CB integration tests
// ---------------------------------------------------------------------------

// TestCB_RateLimitRecovery verifies that 429 triggers exponential backoff
// but the key recovers after the backoff period and success is possible.
func TestCB_RateLimitRecovery(t *testing.T) {
	var mu sync.Mutex
	callCount := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		count := callCount
		callCount++
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if count < 3 {
			w.WriteHeader(http.StatusTooManyRequests)
			w.Write([]byte(`{"error":"rate limited"}`))
		} else {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok"}`))
		}
	}))
	defer upstream.Close()

	// 3 keys, CooldownSec=2 so each key gets short pool cooldown
	cfg := &config.Config{
		TargetBase:          upstream.URL,
		GenaiBase:           upstream.URL,
		Port:                0,
		MaxRetries:          10,
		CooldownSec:         2,
		BackoffCapSec:       120,
		BackoffMultiplier:   2,
		CBResetSec:          30,
		UpstreamCBThreshold: 5,
	}
	pool := keypool.NewKeyPool([]string{"key-a", "key-b", "key-c"}, nil)
	pr := server.NewProviderRouter("")
	pr.AddProvider("test", cfg, pool)
	ts := httptest.NewServer(pr.Handler())
	defer ts.Close()

	// WHEN: send a proxy request
	req, err := http.NewRequest("GET", ts.URL+"/test", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// THEN: eventually succeed after 429 retries
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK after 429 recovery, got %d", resp.StatusCode)
	}
}

// TestCB_QuotaExhausted verifies that repeated 429s lead to retry exhaustion
// (503 EXHAUSTED_RETRIES) rather than permanently disabling the key.
func TestCB_QuotaExhausted(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		w.Write([]byte(`{"error":"rate limited"}`))
	}))
	defer upstream.Close()

	// 1 key with low BackoffCapSec and small MaxRetries for fast test
	cfg := &config.Config{
		TargetBase:          upstream.URL,
		GenaiBase:           upstream.URL,
		Port:                0,
		MaxRetries:          3,
		CooldownSec:         1,
		BackoffCapSec:       5,
		BackoffMultiplier:   2,
		CBResetSec:          60,
		UpstreamCBThreshold: 10,
	}
	pool := keypool.NewKeyPool([]string{"single-key"}, nil)
	pr := server.NewProviderRouter("")
	pr.AddProvider("test", cfg, pool)
	ts := httptest.NewServer(pr.Handler())
	defer ts.Close()

	req, err := http.NewRequest("GET", ts.URL+"/test", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// THEN: return 503 EXHAUSTED_RETRIES (not ALL_KEYS_INVALID)
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 Service Unavailable, got %d", resp.StatusCode)
	}

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode JSON error response: %v", err)
	}
	if body.Error.Code != "EXHAUSTED_RETRIES" {
		t.Errorf("expected error.code EXHAUSTED_RETRIES, got %q", body.Error.Code)
	}
}

// TestCB_UpstreamErrorNoKeyPenalty verifies that 502/503 errors do NOT
// disable the API key — only the upstream circuit breaker is affected.
func TestCB_UpstreamErrorNoKeyPenalty(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"upstream down"}`))
	}))
	defer upstream.Close()

	// MaxRetries=3, UpstreamCBThreshold high so upstream CB does not open
	cfg := &config.Config{
		TargetBase:          upstream.URL,
		GenaiBase:           upstream.URL,
		Port:                0,
		MaxRetries:          3,
		CooldownSec:         1,
		BackoffCapSec:       120,
		BackoffMultiplier:   2,
		CBResetSec:          300,
		UpstreamCBThreshold: 10,
	}
	pool := keypool.NewKeyPool([]string{"test-key"}, nil)
	pr := server.NewProviderRouter("")
	pr.AddProvider("test", cfg, pool)
	ts := httptest.NewServer(pr.Handler())
	defer ts.Close()

	// WHEN: send proxy request -> gets 503 -> exhausts retries
	req, err := http.NewRequest("GET", ts.URL+"/test", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// THEN: return 503 EXHAUSTED_RETRIES
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 Service Unavailable, got %d", resp.StatusCode)
	}

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode JSON error response: %v", err)
	}
	if body.Error.Code != "EXHAUSTED_RETRIES" {
		t.Errorf("expected error.code EXHAUSTED_RETRIES, got %q", body.Error.Code)
	}

	// THEN: key should NOT be disabled (check via health endpoint)
	healthResp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatal(err)
	}
	defer healthResp.Body.Close()

	var health struct {
		Details map[string]struct {
			Status string `json:"status"`
			Keys   int    `json:"keys"`
		} `json:"details"`
	}
	if err := json.NewDecoder(healthResp.Body).Decode(&health); err != nil {
		t.Fatalf("failed to decode health response: %v", err)
	}
	ph, ok := health.Details["test"]
	if !ok {
		t.Fatal("health response missing provider 'test'")
	}
	if ph.Keys == 0 {
		t.Error("expected at least 1 key after 503 errors (upstream error should not disable keys)")
	}
}

// TestCB_UpstreamCircuitBreakerOpens verifies that after UPSTREAM_CB_THRESHOLD
// consecutive 503s, the upstream circuit breaker opens and subsequent retries
// fail fast without calling the upstream.
func TestCB_UpstreamCircuitBreakerOpens(t *testing.T) {
	var mu sync.Mutex
	upstreamCallCount := 0

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		upstreamCallCount++
		mu.Unlock()
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"error":"upstream down"}`))
	}))
	defer upstream.Close()

	cfg := &config.Config{
		TargetBase:          upstream.URL,
		GenaiBase:           upstream.URL,
		Port:                0,
		MaxRetries:          10,
		CooldownSec:         1,
		BackoffCapSec:       120,
		BackoffMultiplier:   2,
		CBResetSec:          60,
		UpstreamCBThreshold: 3,
	}
	pool := keypool.NewKeyPool([]string{"test-key"}, nil)
	pr := server.NewProviderRouter("")
	pr.AddProvider("test", cfg, pool)
	ts := httptest.NewServer(pr.Handler())
	defer ts.Close()

	req, err := http.NewRequest("GET", ts.URL+"/test", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	// THEN: return 503 EXHAUSTED_RETRIES
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.StatusCode)
	}

	// THEN: upstream should have been called at most UPSTREAM_CB_THRESHOLD times
	// After 3 failures, CB opens. Remaining 7 retries fail fast without upstream call.
	mu.Lock()
	count := upstreamCallCount
	mu.Unlock()
	if count > 5 {
		t.Errorf("expected at most ~3 upstream calls after CB opens, got %d", count)
	}
	t.Logf("upstream call count: %d (threshold=3, should be ~3)", count)
}

// ---------------------------------------------------------------------------
// 22. All keys disabled via API — returns 503 immediately (no CPU spin)
// ---------------------------------------------------------------------------

func TestProxy_AllDisabled_Returns503(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstream.Close()

	srv := setupServer(t, upstream, []string{"key-a", "key-b"}, 10, 60)
	defer srv.Close()

	// Disable both keys via API
	for _, idx := range []string{"1", "2"} {
		resp, err := http.Post(srv.URL+"/api/keys/"+idx+"/disable", "application/json", nil)
		if err != nil {
			t.Fatalf("POST /api/keys/%s/disable: %v", idx, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("POST /api/keys/%s/disable: got %d, want 200", idx, resp.StatusCode)
		}
	}

	// Send proxy request — should get 503 immediately, not spin on retries
	resp, err := http.Get(srv.URL + "/test/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 Service Unavailable when all keys disabled, got %d", resp.StatusCode)
	}

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("failed to decode JSON error response: %v", err)
	}
	if body.Error.Code != "ALL_KEYS_INVALID" {
		t.Errorf("expected error.code ALL_KEYS_INVALID, got %q", body.Error.Code)
	}
}


// ---------------------------------------------------------------------------
// NonRetryable error classification tests
// ---------------------------------------------------------------------------

// TestProxy_NonRetryable400_ReturnsImmediately verifies that a 400 response
// from upstream is returned immediately without retrying.
func TestProxy_NonRetryable400_ReturnsImmediately(t *testing.T) {
	var mu sync.Mutex
	callCount := 0

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callCount++
		mu.Unlock()
		w.WriteHeader(http.StatusBadRequest)
		w.Write([]byte(`{"error":"bad request"}`))
	}))
	defer upstream.Close()

	srv := setupServer(t, upstream, []string{"test-key-a", "test-key-b"}, 3, 60)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/test/v1/models")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}

	mu.Lock()
	count := callCount
	mu.Unlock()
	if count > 1 {
		t.Errorf("expected upstream to be called exactly once (non-retryable), called %d times", count)
	}
}

// TestProxy_NonRetryable422_ReturnsImmediately verifies that a 422 response
// from upstream is returned immediately without retrying.
func TestProxy_NonRetryable422_ReturnsImmediately(t *testing.T) {
	var mu sync.Mutex
	callCount := 0

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callCount++
		mu.Unlock()
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"error":"unprocessable"}`))
	}))
	defer upstream.Close()

	srv := setupServer(t, upstream, []string{"test-key-a", "test-key-b"}, 3, 60)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/test/v1/models")
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Errorf("expected 422, got %d", resp.StatusCode)
	}

	mu.Lock()
	count := callCount
	mu.Unlock()
	if count > 1 {
		t.Errorf("expected upstream to be called exactly once (non-retryable), called %d times", count)
	}
}

// TestProxy_NonRetryable_DoesNotPenalizeKey verifies that after a NonRetryable
// 4xx error, the key remains usable for the next (valid) request.
func TestProxy_NonRetryable_DoesNotPenalizeKey(t *testing.T) {
	var mu sync.Mutex
	callCount := 0

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		callCount++
		count := callCount
		mu.Unlock()
		if count == 1 {
			// First call: return non-retryable 400
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":"bad request"}`))
		} else {
			// Subsequent calls: succeed
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok"}`))
		}
	}))
	defer upstream.Close()

	srv := setupServer(t, upstream, []string{"test-key-a"}, 1, 60)
	defer srv.Close()

	// First request — should get 400
	resp1, err := http.Get(srv.URL + "/test/v1/models")
	if err != nil {
		t.Fatalf("first request failed: %v", err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 on first request, got %d", resp1.StatusCode)
	}

	// Second request — same key should still work
	resp2, err := http.Get(srv.URL + "/test/v1/models")
	if err != nil {
		t.Fatalf("second request failed: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected 200 on second request, got %d", resp2.StatusCode)
	}
}

	// ---------------------------------------------------------------------------
	// Key persistence tests
	// ---------------------------------------------------------------------------

// TestKeyPersistence_AddKeyRestart verifies that adding a key via API
// persists it to disk and survives a restart.
func TestKeyPersistence_AddKeyRestart(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstream.Close()

	tmpDir := t.TempDir()
	keysFile := filepath.Join(tmpDir, "keys.json")

	cfg := &config.Config{
		TargetBase:  upstream.URL,
		GenaiBase:   upstream.URL,
		Port:        0,
		MaxRetries:  3,
		CooldownSec: 60,
		KeysFile:    keysFile,
	}
	pool := keypool.NewKeyPool([]string{"initial-key"}, nil)
	pr := server.NewProviderRouter("")
	pr.AddProvider("test", cfg, pool)
	srv := httptest.NewServer(pr.Handler())

	resp, err := http.Post(srv.URL+"/api/keys", "application/json",
		strings.NewReader(`{"key":"persistent-key","name":"test-key"}`))
	if err != nil {
		t.Fatalf("POST /api/keys: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/keys: got status %d, want 200", resp.StatusCode)
	}

	srv.Close()

	data, err := os.ReadFile(keysFile)
	if err != nil {
		t.Fatalf("read keys.json: %v", err)
	}
	t.Logf("keys.json content: %s", string(data))

	if !strings.Contains(string(data), "persistent-key") {
		t.Errorf("keys.json should contain 'persistent-key', got: %s", string(data))
	}

	// Simulate a restart: load keys from file
	fileKeys, fileNames, err := keypool.LoadKeysFromFile(keysFile)
	if err != nil {
		t.Fatalf("LoadKeysFromFile: %v", err)
	}
	if fileKeys == nil {
		t.Fatal("keys.json should exist after first server wrote it")
	}

	restoredPool := keypool.NewKeyPool(fileKeys, fileNames)
	newCfg := &config.Config{
		TargetBase:  upstream.URL,
		GenaiBase:   upstream.URL,
		Port:        0,
		MaxRetries:  3,
		CooldownSec: 60,
		KeysFile:    keysFile,
	}
	pr2 := server.NewProviderRouter("")
	pr2.AddProvider("test", newCfg, restoredPool)
	srv2 := httptest.NewServer(pr2.Handler())
	defer srv2.Close()

	// Verify the key through the API
	resp2, err := http.Get(srv2.URL + "/api/keys")
	if err != nil {
		t.Fatalf("GET /api/keys: %v", err)
	}
	defer resp2.Body.Close()
	body, _ := io.ReadAll(resp2.Body)

	// Name is not masked in the API response
	if !strings.Contains(string(body), "test-key") {
		t.Errorf("restored pool should contain name 'test-key', got: %s", string(body))
	}

	// Key is masked (e.g. "pers...ey"); decode JSON to verify structure
	var keyList []map[string]interface{}
	if err := json.Unmarshal(body, &keyList); err != nil {
		t.Fatalf("failed to decode /api/keys response: %v", err)
	}
	if len(keyList) != 2 {
		t.Errorf("expected 2 keys after restoration, got %d", len(keyList))
	}
}

// TestKeyPersistence_DeleteKeyRestart verifies that deleting a key via API
// persists the removal and the key is gone after restart.
func TestKeyPersistence_DeleteKeyRestart(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstream.Close()

	tmpDir := t.TempDir()
	keysFile := filepath.Join(tmpDir, "keys.json")

	cfg := &config.Config{
		TargetBase:  upstream.URL,
		GenaiBase:   upstream.URL,
		Port:        0,
		MaxRetries:  3,
		CooldownSec: 60,
		KeysFile:    keysFile,
	}
	pool := keypool.NewKeyPool([]string{"key-a", "key-b"}, nil)
	pr := server.NewProviderRouter("")
	pr.AddProvider("test", cfg, pool)
	srv := httptest.NewServer(pr.Handler())

	// Delete key-a via API (index 1 = first key, 1-based in URL)
	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/keys/1", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/keys/1: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("DELETE /api/keys/1: got status %d, want 200", resp.StatusCode)
	}

	srv.Close()

	fileKeys, _, err := keypool.LoadKeysFromFile(keysFile)
	if err != nil {
		t.Fatalf("LoadKeysFromFile: %v", err)
	}
	if fileKeys == nil {
		t.Fatal("keys.json should exist")
	}
	t.Logf("keys after delete: %v", fileKeys)

	for _, k := range fileKeys {
		if k == "key-a" {
			t.Error("key-a should not be in the persisted keys after deletion")
		}
	}
	found := false
	for _, k := range fileKeys {
		if k == "key-b" {
			found = true
			break
		}
	}
	if !found {
		t.Error("key-b should still be in the persisted keys")
	}
}

// TestKeyPersistence_DisableKeyAndPersist verifies that disabling a key
// via API persists the disabled state to disk.
func TestKeyPersistence_DisableKeyAndPersist(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstream.Close()

	tmpDir := t.TempDir()
	keysFile := filepath.Join(tmpDir, "keys.json")

	cfg := &config.Config{
		TargetBase:  upstream.URL,
		GenaiBase:   upstream.URL,
		Port:        0,
		MaxRetries:  3,
		CooldownSec: 60,
		KeysFile:    keysFile,
	}
	pool := keypool.NewKeyPool([]string{"key-a", "key-b"}, nil)
	pr := server.NewProviderRouter("")
	pr.AddProvider("test", cfg, pool)
	srv := httptest.NewServer(pr.Handler())

	// Disable key 1 (first key, 1-based in URL) via API
	resp, err := http.Post(srv.URL+"/api/keys/1/disable", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/keys/1/disable: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("got status %d, want 200", resp.StatusCode)
	}

	srv.Close()

	store, err := keypool.LoadFullStore(keysFile)
	if err != nil {
		t.Fatalf("LoadFullStore: %v", err)
	}
	if store == nil {
		t.Fatal("keys.json should exist")
	}

	t.Logf("store contents: %+v", store.Keys)

	for _, entry := range store.Keys {
		if entry.Key == "key-a" && !entry.Disabled {
			t.Error("key-a should be disabled in persisted store")
		}
	}
}

// ── Key Encryption Integration Tests ────────────────────────

func makeEncKey(b byte) []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = b
	}
	return k
}

// TestKeyEncryption_EndToEnd verifies that with encryption enabled:
//  1. keys.json does NOT contain plaintext keys
//  2. After "restart" (new server loading the file), keys work correctly
func TestKeyEncryption_EndToEnd(t *testing.T) {
	keypool.SetEncryptionKey(makeEncKey('T'))
	defer keypool.SetEncryptionKey(nil)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstream.Close()

	tmpDir := t.TempDir()
	keysFile := filepath.Join(tmpDir, "keys.json")

	cfg := &config.Config{
		TargetBase:     upstream.URL,
		GenaiBase:      upstream.URL,
		Port:           0,
		MaxRetries:     3,
		CooldownSec:    60,
		KeysFile:       keysFile,
		EncryptionKey:  makeEncKey('T'),
	}
	pool := keypool.NewKeyPool([]string{"initial-key"}, nil)

	pr := server.NewProviderRouter("")
	pr.AddProvider("test", cfg, pool)
	srv := httptest.NewServer(pr.Handler())

	// Add a key via API — triggers PersistKeys
	resp, err := http.Post(srv.URL+"/api/keys", "application/json",
		strings.NewReader(`{"key":"encrypted-key-via-api"}`))
	if err != nil {
		t.Fatalf("POST /api/keys: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/keys: got %d, want 200", resp.StatusCode)
	}

	srv.Close()

	// Read the raw file — plaintext must NOT appear
	data, err := os.ReadFile(keysFile)
	if err != nil {
		t.Fatalf("read keys.json: %v", err)
	}
	if strings.Contains(string(data), "encrypted-key-via-api") {
		t.Fatal("plaintext key found in encrypted keys.json")
	}
	if strings.Contains(string(data), "initial-key") {
		t.Fatal("plaintext initial key found in encrypted keys.json")
	}
	t.Logf("encrypted keys.json (no plaintext keys): %s", string(data))

	// Simulate restart: load the encrypted file and create a new server
	fileKeys, fileNames, err := keypool.LoadKeysFromFile(keysFile)
	if err != nil {
		t.Fatalf("LoadKeysFromFile after encryption: %v", err)
	}
	if len(fileKeys) != 2 {
		t.Fatalf("expected 2 keys after restart, got %d: %v", len(fileKeys), fileKeys)
	}

	restoredPool := keypool.NewKeyPool(fileKeys, fileNames)
	newCfg := &config.Config{
		TargetBase:    upstream.URL,
		GenaiBase:     upstream.URL,
		Port:          0,
		MaxRetries:    3,
		CooldownSec:   60,
		KeysFile:      keysFile,
		EncryptionKey: makeEncKey('T'),
	}
	pr2 := server.NewProviderRouter("")
	pr2.AddProvider("test", newCfg, restoredPool)
	srv2 := httptest.NewServer(pr2.Handler())
	defer srv2.Close()

	// Verify both keys are present via API
	resp2, err := http.Get(srv2.URL + "/api/keys")
	if err != nil {
		t.Fatalf("GET /api/keys: %v", err)
	}
	defer resp2.Body.Close()
	body, _ := io.ReadAll(resp2.Body)

	var keyList []map[string]interface{}
	if err := json.Unmarshal(body, &keyList); err != nil {
		t.Fatalf("decode /api/keys: %v", err)
	}
	if len(keyList) != 2 {
		t.Errorf("expected 2 keys, got %d: %s", len(keyList), string(body))
	}
}

// TestKeyEncryption_TamperedFile verifies that loading a tampered encrypted
// keys.json results in an error.
func TestKeyEncryption_TamperedFile(t *testing.T) {
	keypool.SetEncryptionKey(makeEncKey('S'))
	defer keypool.SetEncryptionKey(nil)

	tmpDir := t.TempDir()
	keysFile := filepath.Join(tmpDir, "keys.json")

	store := &keypool.KeyStore{
		Keys: []keypool.KeyEntry{
			{Key: "my-secret-key"},
		},
	}
	if err := keypool.SaveFullStore(keysFile, store); err != nil {
		t.Fatalf("SaveFullStore: %v", err)
	}

	// Tamper with the file
	data, err := os.ReadFile(keysFile)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(data) > 30 {
		data[20] ^= 0xFF
		os.WriteFile(keysFile, data, 0644)
	}

	// Loading should fail
	_, _, err = keypool.LoadKeysFromFile(keysFile)
	if err == nil {
		t.Error("expected error when loading tampered encrypted file, got nil")
	}
}

// TestKeyEncryption_NoEncryption_BackwardCompatible verifies that
// without an encryption key, the system works as before (plaintext keys).
func TestKeyEncryption_NoEncryption_BackwardCompatible(t *testing.T) {
	keypool.SetEncryptionKey(nil)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstream.Close()

	tmpDir := t.TempDir()
	keysFile := filepath.Join(tmpDir, "keys.json")

	cfg := &config.Config{
		TargetBase:  upstream.URL,
		GenaiBase:   upstream.URL,
		Port:        0,
		MaxRetries:  3,
		CooldownSec: 60,
		KeysFile:    keysFile,
	}
	pool := keypool.NewKeyPool([]string{"plaintext-key-a"}, nil)
	pr := server.NewProviderRouter("")
	pr.AddProvider("test", cfg, pool)
	srv := httptest.NewServer(pr.Handler())

	// Add another key
	resp, err := http.Post(srv.URL+"/api/keys", "application/json",
		strings.NewReader(`{"key":"plaintext-key-b"}`))
	if err != nil {
		t.Fatalf("POST /api/keys: %v", err)
	}
	resp.Body.Close()
	srv.Close()

	// Read raw file — keys should be plaintext
	data, err := os.ReadFile(keysFile)
	if err != nil {
		t.Fatalf("read keys.json: %v", err)
	}
	if !strings.Contains(string(data), "plaintext-key-a") {
		t.Error("plaintext key 'plaintext-key-a' not found in unencrypted file")
	}
}

// ---------------------------------------------------------------------------
// LogEntry new field integration tests (T4: 测试覆盖)
// ---------------------------------------------------------------------------

// TestLogEntry_HasNewFields verifies that a successful proxy request
// produces a log entry with DurationMs, Attempt, Provider, and KeyName.
func TestLogEntry_HasNewFields(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstream.Close()

	srv := setupServer(t, upstream, []string{"test-key-a", "test-key-b"}, 10, 60)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/test/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	resp.Body.Close()

	logResp, err := http.Get(srv.URL + "/logs")
	if err != nil {
		t.Fatalf("GET /logs: %v", err)
	}
	defer logResp.Body.Close()

	var entries []map[string]interface{}
	if err := json.NewDecoder(logResp.Body).Decode(&entries); err != nil {
		t.Fatalf("decode logs: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least 1 log entry")
	}

	entry := entries[0]
	for _, field := range []string{"provider", "duration_ms", "retry", "key_name"} {
		if _, ok := entry[field]; !ok {
			t.Errorf("log entry missing %q field", field)
		}
	}

	if p, ok := entry["provider"].(string); !ok || p != "test" {
		t.Errorf("expected provider=\"test\", got %v", entry["provider"])
	}
	if a, ok := entry["retry"].(float64); !ok || a < 0 {
		t.Errorf("expected retry >= 0, got %v", entry["retry"])
	}
	if d, ok := entry["duration_ms"].(float64); !ok || d < 0 {
		t.Errorf("expected duration_ms >= 0, got %v", entry["duration_ms"])
	}
}

// TestLogEntry_ExhaustionHas503 verifies that after all keys are exhausted
// the log store contains a 503 entry with the new fields populated.
func TestLogEntry_ExhaustionHas503(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer upstream.Close()

	// MaxRetries=2, 3 keys, all 429 -> 503
	srv := setupServer(t, upstream, []string{"key-a", "key-b", "key-c"}, 2, 2)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/test/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.StatusCode)
	}

	logResp, err := http.Get(srv.URL + "/logs")
	if err != nil {
		t.Fatalf("GET /logs: %v", err)
	}
	defer logResp.Body.Close()

	var entries []map[string]interface{}
	if err := json.NewDecoder(logResp.Body).Decode(&entries); err != nil {
		t.Fatalf("decode logs: %v", err)
	}

	var found503 bool
	for _, entry := range entries {
		status, _ := entry["status"].(float64)
		if int(status) != http.StatusServiceUnavailable {
			continue
		}
		found503 = true
		for _, field := range []string{"provider", "duration_ms", "retry", "key_name"} {
			if _, ok := entry[field]; !ok {
				t.Errorf("503 log entry missing %q field", field)
			}
		}
		if p, ok := entry["provider"].(string); !ok || p != "test" {
			t.Errorf("expected provider=\"test\", got %v", entry["provider"])
		}
		if a, ok := entry["retry"].(float64); !ok || a < 2 {
			t.Errorf("expected retry >= 2 (MaxRetries), got %v", entry["retry"])
		}
		break
	}
	if !found503 {
		t.Error("no 503 log entry found after retry exhaustion")
	}
}

// TestLogEntry_CLIFormat verifies the log JSON structure is suitable
// for CLI display — all fields the CLI command needs are present.
func TestLogEntry_CLIFormat(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
	}))
	defer upstream.Close()

	srv := setupServer(t, upstream, []string{"test-key-a", "test-key-b"}, 10, 60)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/test/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	resp.Body.Close()

	logResp, err := http.Get(srv.URL + "/logs")
	if err != nil {
		t.Fatalf("GET /logs: %v", err)
	}
	defer logResp.Body.Close()

	var entries []map[string]interface{}
	if err := json.NewDecoder(logResp.Body).Decode(&entries); err != nil {
		t.Fatalf("decode logs: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least 1 log entry")
	}

	entry := entries[0]
	// All fields the CLI display code in internal/cmd/logs.go reads
	for _, field := range []string{"method", "url", "status", "timestamp", "provider", "duration_ms", "retry", "key_name"} {
		if _, ok := entry[field]; !ok {
			t.Errorf("CLI display needs field %q, but log entry missing it", field)
		}
	}

	t.Logf("CLI-ready log entry: provider=%v duration_ms=%v retry=%v key_name=%v",
		entry["provider"], entry["duration_ms"], entry["retry"], entry["key_name"])
}
