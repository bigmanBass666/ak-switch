package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// setupAlvus creates a mock upstream and an Alvus test server, returning both.
// The caller must close both servers.
func setupAlvus(t *testing.T, upstream *httptest.Server, poolKeys []string, maxRetries, cooldownSec int) *httptest.Server {
	t.Helper()
	cfg := Config{
		TargetBase:  upstream.URL,
		GenaiBase:   upstream.URL,
		Port:        "0",
		MaxRetries:  maxRetries,
		CooldownSec: cooldownSec,
		AdminToken:  "",
	}
	pool := NewKeyPool(poolKeys)
	state := newServerState(cfg, pool)
	return httptest.NewServer(state.mux)
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

	alvus := setupAlvus(t, upstream, []string{"test-key-a", "test-key-b", "test-key-c"}, 10, 60)
	defer alvus.Close()

	resp, err := http.Get(alvus.URL + "/v1/models")
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

	alvus := setupAlvus(t, upstream, []string{"test-key-a", "test-key-b", "test-key-c"}, 10, 60)
	defer alvus.Close()

	resp, err := http.Get(alvus.URL + "/v1/models")
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

	alvus := setupAlvus(t, upstream, []string{"test-key-a", "test-key-b", "test-key-c"}, 10, 60)
	defer alvus.Close()

	for i := 0; i < 2; i++ {
		resp, err := http.Get(alvus.URL + "/v1/models")
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

	alvus := setupAlvus(t, upstream, []string{"test-key-a", "test-key-b", "test-key-c"}, 10, 60)
	defer alvus.Close()

	resp, err := http.Get(alvus.URL + "/v1/models")
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

	alvus := setupAlvus(t, upstream, []string{"test-key-a", "test-key-b", "test-key-c"}, 10, 60)
	defer alvus.Close()

	resp, err := http.Get(alvus.URL + "/v1/models")
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

	alvus := setupAlvus(t, upstream, []string{"test-key-a", "test-key-b", "test-key-c"}, 10, 60)
	defer alvus.Close()

	resp, err := http.Get(alvus.URL + "/v1/models")
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
	alvus := setupAlvus(t, upstream, []string{"key-a", "key-b", "key-c"}, 3, 2)
	defer alvus.Close()

	resp, err := http.Get(alvus.URL + "/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 Service Unavailable after exhaustion, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "alvus: exhausted all retries") {
		t.Errorf("expected exhaustion message, got %q", strings.TrimSpace(string(body)))
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

	alvus := setupAlvus(t, upstream, []string{"test-key-a", "test-key-b", "test-key-c"}, 10, 60)
	defer alvus.Close()

	resp, err := http.Get(alvus.URL + "/v1/models")
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

	alvus := setupAlvus(t, upstream, []string{"test-key-a", "test-key-b", "test-key-c"}, 10, 60)
	defer alvus.Close()

	resp, err := http.Get(alvus.URL + "/v1/models")
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

	alvus := setupAlvus(t, upstream, []string{"test-key-a", "test-key-b", "test-key-c"}, 10, 60)
	defer alvus.Close()

	payload := `{"hello":"world"}`
	resp, err := http.Post(alvus.URL+"/v1/models", "application/json", bytes.NewReader([]byte(payload)))
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

	// Create Alvus with 1 initial key (must have at least 1 to avoid panic in Next())
	cfg := Config{
		TargetBase:  upstream.URL,
		GenaiBase:   upstream.URL,
		Port:        "0",
		MaxRetries:  10,
		CooldownSec: 60,
		AdminToken:  "",
	}
	pool := NewKeyPool([]string{"initial-key"})
	state := newServerState(cfg, pool)
	alvus := httptest.NewServer(state.mux)
	defer alvus.Close()

	// Step 1: POST /api/keys to add a new key
	addBody := `{"key":"added-key-456"}`
	resp, err := http.Post(alvus.URL+"/api/keys", "application/json", bytes.NewReader([]byte(addBody)))
	if err != nil {
		t.Fatalf("POST /api/keys: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /api/keys expected 200, got %d", resp.StatusCode)
	}

	// Step 2: GET /api/keys to verify count increased
	resp, err = http.Get(alvus.URL + "/api/keys")
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
	resp, err = http.Get(alvus.URL + "/v1/models")
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
	alvus := setupAlvus(t, upstream, []string{"test-key-a", "test-key-b", "test-key-c"}, 2, 60)
	defer alvus.Close()

	resp, err := http.Get(alvus.URL + "/v1/models")
	if err != nil {
		t.Fatalf("GET /v1/models: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 after exhausting MaxRetries=2, got %d", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "alvus: exhausted all retries") {
		t.Errorf("expected exhaustion message, got %q", strings.TrimSpace(string(body)))
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

	alvus := setupAlvus(t, upstream, []string{"key-a", "key-b", "key-c", "key-d", "key-e"}, 10, 60)
	defer alvus.Close()

	const concurrency = 20
	var wg sync.WaitGroup
	errs := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			resp, err := http.Get(alvus.URL + "/v1/models")
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

	alvus := setupAlvus(t, upstream, []string{"key-a", "key-b", "key-c"}, 10, 60)
	defer alvus.Close()

	const concurrency = 30
	var wg sync.WaitGroup
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			resp, err := http.Get(alvus.URL + "/v1/models")
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

	alvus := setupAlvus(t, upstream, []string{"key-a", "key-b", "key-c", "key-d", "key-e"}, 10, 2)
	defer alvus.Close()

	const concurrency = 15
	var wg sync.WaitGroup
	errs := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			resp, err := http.Get(alvus.URL + "/v1/models")
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

	alvus := setupAlvus(t, upstream, []string{"test-key"}, 10, 60)
	defer alvus.Close()

	req, err := http.NewRequest("GET", alvus.URL+"/v1/models", nil)
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