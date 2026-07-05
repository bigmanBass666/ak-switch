//go:build integration

package main

import (
	"akswitch/internal/config"
	"akswitch/internal/keypool"
	"akswitch/internal/server"
	"akswitch/internal/utils"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestServer(keys []string) *httptest.Server {
	cfg := &config.Config{
		TargetBase:  "http://localhost:19999",
		GenaiBase:   "http://localhost:19999",
		Port:        19999,
		MaxRetries:  3,
		CooldownSec: 60,
		AdminToken:  "",
		Keys:        []string{"key-a", "key-b"},
	}
	pool := keypool.NewKeyPool(keys, nil)
	pr := server.NewProviderRouter("")
	pr.AddProvider("test", cfg, pool)
	return httptest.NewServer(pr.Handler())
}

// ── Health ─────────────────────────────────────────

func TestHealthHandler(t *testing.T) {
	srv := newTestServer([]string{"key-a", "key-b", "key-c"})
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body["status"] != "ok" {
		t.Errorf(`expected status="ok", got %v`, body["status"])
	}

	if n, ok := body["providers"].(float64); !ok || int(n) != 1 {
		t.Errorf("expected providers=1, got %v", body["providers"])
	}

	details, ok := body["details"].(map[string]interface{})
	if !ok {
		t.Fatal("expected details field with per-provider data")
	}
	testProv, ok := details["test"].(map[string]interface{})
	if !ok {
		t.Fatal("expected test provider in details")
	}
	if n, ok := testProv["keys"].(float64); !ok || int(n) != 3 {
		t.Errorf("expected keys=3 for test provider, got %v", testProv["keys"])
	}
}

// ── Config GET ─────────────────────────────────────

func TestConfigGet(t *testing.T) {
	srv := newTestServer([]string{"key-a", "key-b", "key-c"})
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/config")
	if err != nil {
		t.Fatalf("GET /api/config: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body["targetBase"] != "http://localhost:19999" {
		t.Errorf(`expected targetBase="http://localhost:19999", got %v`, body["targetBase"])
	}
	if body["genaiBase"] != "http://localhost:19999" {
		t.Errorf(`expected genaiBase="http://localhost:19999", got %v`, body["genaiBase"])
	}

	keys, ok := body["keys"].([]interface{})
	if !ok {
		t.Fatal("expected keys field as array")
	}
	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}

	expectedMasked := utils.MaskKey("key-a")

	for i, k := range keys {
		masked, ok := k.(string)
		if !ok {
			t.Errorf("keys[%d] is not a string", i)
			continue
		}
		// All keys should be masked — none should equal the raw key
		if masked == "key-a" || masked == "key-b" || masked == "key-c" {
			t.Errorf("keys[%d]=%q appears unmasked", i, masked)
		}
		// The masking format should match utils.MaskKey()
		if i == 0 && masked != expectedMasked {
			t.Errorf("keys[0]=%q, want masking like %q", masked, expectedMasked)
		}
	}
}

// ── Config POST ────────────────────────────────────

func TestConfigPost(t *testing.T) {
	// ConfigPost 会写 .env 并调用 reloadConfig，需要隔离到临时目录
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	// 写入初始 .env 供 reloadConfig 使用
	envContent := "PORT=19999\nTARGET_BASE_URL=http://localhost:19999\nGENAI_BASE_URL=http://localhost:19999\nAPI_KEYS=key-a,key-b\nCOOLDOWN_SEC=60\nMAX_RETRIES=3\n"
	if err := os.WriteFile(filepath.Join(tmpDir, ".env"), []byte(envContent), 0600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		TargetBase:  "http://localhost:19999",
		GenaiBase:   "http://localhost:19999",
		Port:        19999,
		MaxRetries:  3,
		CooldownSec: 60,
		AdminToken:  "",
		Keys:        []string{"key-a", "key-b"},
	}
	pool := keypool.NewKeyPool([]string{"key-a", "key-b"}, nil)
	pr := server.NewProviderRouter("")
	pr.AddProvider("test", cfg, pool)
	srv := httptest.NewServer(pr.Handler())
	defer srv.Close()

	// POST /api/config is no longer supported in ProviderRouter architecture
	reqBody := `{"targetBase":"https://new.example.com/v1","genaiBase":"https://genai.example.com","keys":["new-key-1","new-key-2"]}`
	resp, err := http.Post(srv.URL+"/api/config", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /api/config: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405 (POST removed), got %d", resp.StatusCode)
	}
}

// ── Keys GET ───────────────────────────────────────

func TestKeysGet(t *testing.T) {
	srv := newTestServer([]string{"key-a", "key-b", "key-c"})
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/keys")
	if err != nil {
		t.Fatalf("GET /api/keys: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var keys []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&keys); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if len(keys) != 3 {
		t.Fatalf("expected 3 keys, got %d", len(keys))
	}

	for i, k := range keys {
		// index is 1-based in keysHandler GET
		if idx, ok := k["index"].(float64); !ok || int(idx) != i+1 {
			t.Errorf("keys[%d] index=%v, want %d", i, k["index"], i+1)
		}
		if key, ok := k["key"].(string); !ok || key == "" {
			t.Errorf("keys[%d] key=%v, want non-empty masked string", i, k["key"])
		}
		if status, ok := k["status"].(string); !ok || status == "" {
			t.Errorf("keys[%d] status=%v, want non-empty string", i, k["status"])
		}
		if _, ok := k["requests_1m"]; !ok {
			t.Errorf("keys[%d] missing requests_1m field", i)
		}
	}
}

// ── Keys POST ──────────────────────────────────────

func TestKeysPost(t *testing.T) {
	srv := newTestServer([]string{"key-a", "key-b", "key-c"})
	defer srv.Close()

	// POST 添加新 key
	reqBody := `{"key":"new-test-key"}`
	resp, err := http.Post(srv.URL+"/api/keys", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /api/keys: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var addResp map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&addResp); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	// AddKey 返回的 index 为 0-based: 长度 3→新 key idx=3
	if idx, ok := addResp["index"].(float64); !ok || int(idx) != 3 {
		t.Errorf("expected index=3, got %v", addResp["index"])
	}
	if key, ok := addResp["key"].(string); !ok || key == "" {
		t.Errorf("expected non-empty masked key, got %v", addResp["key"])
	}
	if addResp["key"] != utils.MaskKey("new-test-key") {
		t.Errorf("expected key=%q, got %q", utils.MaskKey("new-test-key"), addResp["key"])
	}

	// GET 验证 key 数量为 4
	resp2, err := http.Get(srv.URL + "/api/keys")
	if err != nil {
		t.Fatalf("GET /api/keys: %v", err)
	}
	defer resp2.Body.Close()

	var keys []map[string]interface{}
	if err := json.NewDecoder(resp2.Body).Decode(&keys); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(keys) != 4 {
		t.Errorf("expected 4 keys after POST, got %d", len(keys))
	}
}

// ── Keys DELETE ────────────────────────────────────

func TestKeysDelete(t *testing.T) {
	srv := newTestServer([]string{"key-a", "key-b", "key-c"})
	defer srv.Close()

	// 先 GET 确认当前 key 数
	resp, err := http.Get(srv.URL + "/api/keys")
	if err != nil {
		t.Fatalf("GET /api/keys: %v", err)
	}
	defer resp.Body.Close()

	var before []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&before); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	beforeCount := len(before)

	// DELETE 移除 index=1 (1-based) 的 key
	reqBody := `{"index":1}`
	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/keys", strings.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/keys: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp2.StatusCode)
	}

	var delResp map[string]string
	if err := json.NewDecoder(resp2.Body).Decode(&delResp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if delResp["status"] != "removed" {
		t.Errorf(`expected status="removed", got %q`, delResp["status"])
	}

	// GET 验证 key 数减 1
	resp3, err := http.Get(srv.URL + "/api/keys")
	if err != nil {
		t.Fatalf("GET /api/keys: %v", err)
	}
	defer resp3.Body.Close()

	var after []map[string]interface{}
	if err := json.NewDecoder(resp3.Body).Decode(&after); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(after) != beforeCount-1 {
		t.Errorf("expected %d keys after DELETE, got %d", beforeCount-1, len(after))
	}
}

// ── Clear ──────────────────────────────────────────

func TestClearHandler(t *testing.T) {
	srv := newTestServer([]string{"key-a", "key-b", "key-c"})
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/clear", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST /clear: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body["status"] != "cleared" {
		t.Errorf(`expected status="cleared", got %v`, body["status"])
	}
}

// ── Health with AdminToken auth ──────────────────────

func TestHealthHandlerAuth(t *testing.T) {
	cfg := &config.Config{
		TargetBase:  "http://localhost:19999",
		GenaiBase:   "http://localhost:19999",
		Port:        19999,
		MaxRetries:  3,
		CooldownSec: 60,
		AdminToken:  "my-token",
	}
	pool := keypool.NewKeyPool([]string{"key-a", "key-b", "key-c"}, nil)
	pr := server.NewProviderRouter("")
	pr.AddProvider("test", cfg, pool)
	srv := httptest.NewServer(pr.Handler())
	defer srv.Close()

	// Without token → 401
	resp, err := http.Get(srv.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health (no auth): %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without token, got %d", resp.StatusCode)
	}

	// With wrong token → 401
	req, _ := http.NewRequest("GET", srv.URL+"/health", nil)
	req.Header.Set("X-Admin-Token", "wrong-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /health (wrong token): %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 with wrong token, got %d", resp.StatusCode)
	}

	// With correct token → 200
	req, _ = http.NewRequest("GET", srv.URL+"/health", nil)
	req.Header.Set("X-Admin-Token", "my-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /health (correct token): %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 with correct token, got %d", resp.StatusCode)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["status"] != "ok" {
		t.Errorf(`expected status="ok", got %v`, body["status"])
	}
}

// ── Clear with AdminToken auth ───────────────────────

func TestClearHandlerAuth(t *testing.T) {
	cfg := &config.Config{
		TargetBase:  "http://localhost:19999",
		GenaiBase:   "http://localhost:19999",
		Port:        19999,
		MaxRetries:  3,
		CooldownSec: 60,
		AdminToken:  "my-token",
	}
	pool := keypool.NewKeyPool([]string{"key-a", "key-b", "key-c"}, nil)
	pr := server.NewProviderRouter("")
	pr.AddProvider("test", cfg, pool)
	srv := httptest.NewServer(pr.Handler())
	defer srv.Close()

	// Without token → 401
	resp, err := http.Post(srv.URL+"/clear", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST /clear (no auth): %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without token, got %d", resp.StatusCode)
	}

	// With wrong token → 401
	req, _ := http.NewRequest("POST", srv.URL+"/clear", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Token", "wrong-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /clear (wrong token): %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 with wrong token, got %d", resp.StatusCode)
	}

	// With correct token → 200
	req, _ = http.NewRequest("POST", srv.URL+"/clear", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Token", "my-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /clear (correct token): %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 with correct token, got %d", resp.StatusCode)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["status"] != "cleared" {
		t.Errorf(`expected status="cleared", got %v`, body["status"])
	}
}

// ── Stats GET ───────────────────────────────────────

func TestStatsHandler(t *testing.T) {
	srv := newTestServer([]string{"key-a", "key-b", "key-c"})
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/stats")
	if err != nil {
		t.Fatalf("GET /api/stats: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	fields := []string{"total_requests", "successful_requests", "failed_requests", "success_rate", "active_keys", "cooling_keys", "disabled_keys", "uptime_seconds"}
	for _, f := range fields {
		if _, ok := body[f]; !ok {
			t.Errorf("missing field %q in response", f)
		}
	}
}

// ── Disable Key POST ──────────────────────────────────

func TestDisableKeyHandler(t *testing.T) {
	srv := newTestServer([]string{"key-a", "key-b", "key-c"})
	defer srv.Close()

	// 禁用 index=1
	req, _ := http.NewRequest("POST", srv.URL+"/api/keys/1/disable", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/keys/1/disable: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["success"] != true {
		t.Errorf("expected success=true, got %v", body["success"])
	}

	// GET /api/keys 验证该 key 状态为 "disabled"
	resp2, err := http.Get(srv.URL + "/api/keys")
	if err != nil {
		t.Fatalf("GET /api/keys: %v", err)
	}
	defer resp2.Body.Close()

	var keys []map[string]interface{}
	if err := json.NewDecoder(resp2.Body).Decode(&keys); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(keys) < 1 {
		t.Fatal("expected at least 1 key")
	}
	status, _ := keys[0]["status"].(string)
	if status != "disabled" {
		t.Errorf("expected keys[0] status=disabled, got %q", status)
	}

	// 越界 index=999 → 404
	req2, _ := http.NewRequest("POST", srv.URL+"/api/keys/999/disable", nil)
	resp3, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("POST /api/keys/999/disable: %v", err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for out-of-range index, got %d", resp3.StatusCode)
	}
}

func TestDisableKeyHandlerAuth(t *testing.T) {
	cfg := &config.Config{
		TargetBase:  "http://localhost:19999",
		GenaiBase:   "http://localhost:19999",
		Port:        19999,
		MaxRetries:  3,
		CooldownSec: 60,
		AdminToken:  "my-token",
		Keys:        []string{"key-a", "key-b", "key-c"},
	}
	pool := keypool.NewKeyPool([]string{"key-a", "key-b", "key-c"}, nil)
	pr := server.NewProviderRouter("")
	pr.AddProvider("test", cfg, pool)
	srv := httptest.NewServer(pr.Handler())
	defer srv.Close()

	// Without token → 401
	req, _ := http.NewRequest("POST", srv.URL+"/api/keys/1/disable", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/keys/1/disable (no auth): %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without token, got %d", resp.StatusCode)
	}

	// Wrong token → 401
	req, _ = http.NewRequest("POST", srv.URL+"/api/keys/1/disable", nil)
	req.Header.Set("X-Admin-Token", "wrong-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/keys/1/disable (wrong token): %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 with wrong token, got %d", resp.StatusCode)
	}

	// Correct token → 200
	req, _ = http.NewRequest("POST", srv.URL+"/api/keys/1/disable", nil)
	req.Header.Set("X-Admin-Token", "my-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/keys/1/disable (correct token): %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 with correct token, got %d", resp.StatusCode)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["success"] != true {
		t.Errorf("expected success=true, got %v", body["success"])
	}
}

// ── Cooldown Key PUT ──────────────────────────────────

func TestCooldownKeyHandler(t *testing.T) {
	srv := newTestServer([]string{"key-a", "key-b", "key-c"})
	defer srv.Close()

	// 冷却 index=1
	req, _ := http.NewRequest("PUT", srv.URL+"/api/keys/1/cooldown", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT /api/keys/1/cooldown: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["success"] != true {
		t.Errorf("expected success=true, got %v", body["success"])
	}

	// 越界 index=999 → 404
	req2, _ := http.NewRequest("PUT", srv.URL+"/api/keys/999/cooldown", nil)
	resp3, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("PUT /api/keys/999/cooldown: %v", err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for out-of-range index, got %d", resp3.StatusCode)
	}
}

// ── Delete Key by Index ───────────────────────────────

func TestDeleteKeyByIndexHandler(t *testing.T) {
	srv := newTestServer([]string{"key-a", "key-b", "key-c"})
	defer srv.Close()

	// 删除 index=1
	req, _ := http.NewRequest("DELETE", srv.URL+"/api/keys/1", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/keys/1: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["success"] != true {
		t.Errorf("expected success=true, got %v", body["success"])
	}

	// GET /api/keys 验证只剩 2 个 key
	resp2, err := http.Get(srv.URL + "/api/keys")
	if err != nil {
		t.Fatalf("GET /api/keys: %v", err)
	}
	defer resp2.Body.Close()

	var keys []map[string]interface{}
	if err := json.NewDecoder(resp2.Body).Decode(&keys); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(keys) != 2 {
		t.Errorf("expected 2 keys after DELETE, got %d", len(keys))
	}

	// 越界 index=999 → 404
	req2, _ := http.NewRequest("DELETE", srv.URL+"/api/keys/999", nil)
	resp3, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("DELETE /api/keys/999: %v", err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404 for out-of-range index, got %d", resp3.StatusCode)
	}
}

func TestDeleteKeyByIndexHandlerAuth(t *testing.T) {
	cfg := &config.Config{
		TargetBase:  "http://localhost:19999",
		GenaiBase:   "http://localhost:19999",
		Port:        19999,
		MaxRetries:  3,
		CooldownSec: 60,
		AdminToken:  "my-token",
		Keys:        []string{"key-a", "key-b", "key-c"},
	}
	pool := keypool.NewKeyPool([]string{"key-a", "key-b", "key-c"}, nil)
	pr := server.NewProviderRouter("")
	pr.AddProvider("test", cfg, pool)
	srv := httptest.NewServer(pr.Handler())
	defer srv.Close()

	// Without token → 401
	req, _ := http.NewRequest("DELETE", srv.URL+"/api/keys/1", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/keys/1 (no auth): %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without token, got %d", resp.StatusCode)
	}

	// With correct token → 200
	req, _ = http.NewRequest("DELETE", srv.URL+"/api/keys/1", nil)
	req.Header.Set("X-Admin-Token", "my-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/keys/1 (correct token): %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 with correct token, got %d", resp.StatusCode)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["success"] != true {
		t.Errorf("expected success=true, got %v", body["success"])
	}
}

// ── Reload POST ──────────────────────────────────────

func TestReloadHandler(t *testing.T) {
	tmpDir := t.TempDir()
	config.ConfigDir = tmpDir
	t.Cleanup(func() { config.ConfigDir = "" })

	// Write a valid config.toml at the XDG path for reload to read
	xdgPath, err := config.XDGConfigPath()
	if err != nil {
		t.Fatalf("XDGConfigPath failed: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(xdgPath), 0755); err != nil {
		t.Fatalf("mkdir failed: %v", err)
	}
	tomlContent := `[provider.test]
target = "http://localhost:19999/v1"
genai = "http://localhost:19999"
port = 19999
max_retries = 3
cooldown_sec = 60
`
	if err := os.WriteFile(xdgPath, []byte(tomlContent), 0600); err != nil {
		t.Fatalf("write config failed: %v", err)
	}

	cfg := &config.Config{
		TargetBase:  "http://localhost:19999",
		GenaiBase:   "http://localhost:19999",
		Port:        19999,
		MaxRetries:  3,
		CooldownSec: 60,
		AdminToken:  "",
		Keys:        []string{"key-a", "key-b"},
	}
	pool := keypool.NewKeyPool([]string{"key-a", "key-b"}, nil)
	pr := server.NewProviderRouter("")
	pr.AddProvider("test", cfg, pool)
	srv := httptest.NewServer(pr.Handler())
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/reload", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST /api/reload: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if body["success"] != true {
		t.Errorf("expected success=true, got %v", body["success"])
	}
}

// ── Log Level API ─────────────────────────────────────

func TestLogLevelHandler_Success(t *testing.T) {
	srv := newTestServer([]string{"key-a"})
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/log-level", "application/json", strings.NewReader(`{"level":"debug"}`))
	if err != nil {
		t.Fatalf("POST /api/log-level: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if level, ok := body["level"].(string); !ok || level != "debug" {
		t.Errorf(`expected level="debug", got %v`, body["level"])
	}
}

func TestLogLevelHandler_InvalidLevel(t *testing.T) {
	srv := newTestServer([]string{"key-a"})
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/log-level", "application/json", strings.NewReader(`{"level":"verbose"}`))
	if err != nil {
		t.Fatalf("POST /api/log-level: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", resp.StatusCode)
	}
}

func TestLogLevelHandler_WrongMethod(t *testing.T) {
	srv := newTestServer([]string{"key-a"})
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/log-level")
	if err != nil {
		t.Fatalf("GET /api/log-level: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected status 405, got %d", resp.StatusCode)
	}
}

func TestLogLevelHandler_Unauthorized(t *testing.T) {
	cfg := &config.Config{
		TargetBase:  "http://localhost:19999",
		GenaiBase:   "http://localhost:19999",
		Port:        19999,
		MaxRetries:  3,
		CooldownSec: 60,
		AdminToken:  "secret-token",
		Keys:        []string{"key-a"},
	}
	pool := keypool.NewKeyPool([]string{"key-a"}, nil)
	pr := server.NewProviderRouter("")
	pr.AddProvider("test", cfg, pool)
	srv := httptest.NewServer(pr.Handler())
	defer srv.Close()

	// No token → 401
	resp, err := http.Post(srv.URL+"/api/log-level", "application/json", strings.NewReader(`{"level":"debug"}`))
	if err != nil {
		t.Fatalf("POST /api/log-level: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected status 401, got %d", resp.StatusCode)
	}
}

// ── Keys DELETE — 1-based index validation ─────────────

func TestKeysHandlerDelete_OneBased(t *testing.T) {
	srv := newTestServer([]string{"key-a", "key-b", "key-c"})
	defer srv.Close()

	// GET initial count
	resp, err := http.Get(srv.URL + "/api/keys")
	if err != nil {
		t.Fatalf("GET /api/keys: %v", err)
	}
	defer resp.Body.Close()
	var before []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&before); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	beforeCount := len(before)

	// DELETE index=1 (1-based) → should succeed
	reqBody := `{"index":1}`
	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/keys", strings.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/keys: %v", err)
	}
	defer resp2.Body.Close()

	if resp2.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp2.StatusCode)
	}

	// GET after delete — count should be reduced by 1
	resp3, err := http.Get(srv.URL + "/api/keys")
	if err != nil {
		t.Fatalf("GET /api/keys: %v", err)
	}
	defer resp3.Body.Close()

	var after []map[string]interface{}
	if err := json.NewDecoder(resp3.Body).Decode(&after); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(after) != beforeCount-1 {
		t.Errorf("expected %d keys after DELETE, got %d", beforeCount-1, len(after))
	}
}

func TestKeysHandlerDelete_IndexZeroReturns400(t *testing.T) {
	srv := newTestServer([]string{"key-a", "key-b", "key-c"})
	defer srv.Close()

	reqBody := `{"index":0}`
	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/keys", strings.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/keys: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400 for index=0, got %d", resp.StatusCode)
	}
}

func TestKeysHandlerDelete_IndexNegativeReturns400(t *testing.T) {
	srv := newTestServer([]string{"key-a", "key-b", "key-c"})
	defer srv.Close()

	reqBody := `{"index":-1}`
	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/keys", strings.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/keys: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400 for index=-1, got %d", resp.StatusCode)
	}
}

func TestKeysHandlerDelete_IndexTooLargeReturns400(t *testing.T) {
	srv := newTestServer([]string{"key-a", "key-b", "key-c"})
	defer srv.Close()

	reqBody := `{"index":999}`
	req, err := http.NewRequest(http.MethodDelete, srv.URL+"/api/keys", strings.NewReader(reqBody))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/keys: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected status 400 for index=999, got %d", resp.StatusCode)
	}
}

// ── DELETE /api/keys — unauthenticated ──────────────────

func TestKeysHandlerDelete_Unauthenticated(t *testing.T) {
	cfg := &config.Config{
		TargetBase:  "http://localhost:19999",
		GenaiBase:   "http://localhost:19999",
		Port:        19999,
		MaxRetries:  3,
		CooldownSec: 60,
		AdminToken:  "my-token",
		Keys:        []string{"key-a", "key-b", "key-c"},
	}
	pool := keypool.NewKeyPool([]string{"key-a", "key-b", "key-c"}, nil)
	pr := server.NewProviderRouter("")
	pr.AddProvider("test", cfg, pool)
	srv := httptest.NewServer(pr.Handler())
	defer srv.Close()

	// Without token → 401
	reqBody := `{"index":1}`
	req, _ := http.NewRequest(http.MethodDelete, srv.URL+"/api/keys", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/keys (no auth): %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without token, got %d", resp.StatusCode)
	}

	// With correct token → 200
	req, _ = http.NewRequest(http.MethodDelete, srv.URL+"/api/keys", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Token", "my-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/keys (correct token): %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 with correct token, got %d", resp.StatusCode)
	}
}

// ── Config POST — unauthenticated ──────────────────────

func TestConfigHandlerPost_Unauthenticated(t *testing.T) {
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

	// Write initial .env for ReloadConfig
	envContent := "PORT=19999\nTARGET_BASE_URL=http://localhost:19999\nGENAI_BASE_URL=http://localhost:19999\nAPI_KEYS=key-a,key-b\nCOOLDOWN_SEC=60\nMAX_RETRIES=3\n"
	if err := os.WriteFile(filepath.Join(tmpDir, ".env"), []byte(envContent), 0600); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		TargetBase:  "http://localhost:19999",
		GenaiBase:   "http://localhost:19999",
		Port:        19999,
		MaxRetries:  3,
		CooldownSec: 60,
		AdminToken:  "my-token",
		Keys:        []string{"key-a", "key-b"},
	}
	pool := keypool.NewKeyPool([]string{"key-a", "key-b"}, nil)
	pr := server.NewProviderRouter("")
	pr.AddProvider("test", cfg, pool)
	srv := httptest.NewServer(pr.Handler())
	defer srv.Close()

	// POST /api/config is no longer supported — both no token and with token return 405
	reqBody := `{"targetBase":"http://example.com","genaiBase":"http://genai.example.com","keys":["new-key"]}`
	resp, err := http.Post(srv.URL+"/api/config", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /api/config (no auth): %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 for POST (method not allowed), got %d", resp.StatusCode)
	}

	// With correct token — still 405
	req, _ := http.NewRequest("POST", srv.URL+"/api/config", strings.NewReader(reqBody))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Admin-Token", "my-token")
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/config (correct token): %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 with token, got %d", resp.StatusCode)
	}
}
