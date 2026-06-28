package main

import (
	"alvus/internal/config"
	"alvus/internal/keypool"
	"alvus/internal/server"
	"alvus/internal/utils"
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
	state := server.NewServerState(cfg, pool, "")
	return httptest.NewServer(state.Handler())
}

// ── Health ─────────────────────────────────────────

func TestHealthHandler(t *testing.T) {
	alvus := newTestServer([]string{"key-a", "key-b", "key-c"})
	defer alvus.Close()

	resp, err := http.Get(alvus.URL + "/health")
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

	if n, ok := body["keys"].(float64); !ok || int(n) != 3 {
		t.Errorf("expected keys=3, got %v", body["keys"])
	}

	if _, ok := body["details"]; !ok {
		t.Error("expected details field in response")
	}
}

// ── Config GET ─────────────────────────────────────

func TestConfigGet(t *testing.T) {
	alvus := newTestServer([]string{"key-a", "key-b", "key-c"})
	defer alvus.Close()

	resp, err := http.Get(alvus.URL + "/api/config")
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
	state := server.NewServerState(cfg, pool, "")
	alvus := httptest.NewServer(state.Handler())
	defer alvus.Close()

	reqBody := `{"targetBase":"https://new.example.com/v1","genaiBase":"https://genai.example.com","keys":["new-key-1","new-key-2"]}`
	resp, err := http.Post(alvus.URL+"/api/config", "application/json", strings.NewReader(reqBody))
	if err != nil {
		t.Fatalf("POST /api/config: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected status 200, got %d", resp.StatusCode)
	}

	var body map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}

	if body["status"] != "reloaded" {
		t.Errorf(`expected status="reloaded", got %v`, body["status"])
	}
}

// ── Keys GET ───────────────────────────────────────

func TestKeysGet(t *testing.T) {
	alvus := newTestServer([]string{"key-a", "key-b", "key-c"})
	defer alvus.Close()

	resp, err := http.Get(alvus.URL + "/api/keys")
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
	alvus := newTestServer([]string{"key-a", "key-b", "key-c"})
	defer alvus.Close()

	// POST 添加新 key
	reqBody := `{"key":"new-test-key"}`
	resp, err := http.Post(alvus.URL+"/api/keys", "application/json", strings.NewReader(reqBody))
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
	resp2, err := http.Get(alvus.URL + "/api/keys")
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
	alvus := newTestServer([]string{"key-a", "key-b", "key-c"})
	defer alvus.Close()

	// 先 GET 确认当前 key 数
	resp, err := http.Get(alvus.URL + "/api/keys")
	if err != nil {
		t.Fatalf("GET /api/keys: %v", err)
	}
	defer resp.Body.Close()

	var before []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&before); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	beforeCount := len(before)

	// DELETE 移除 index=0 的 key
	reqBody := `{"index":0}`
	req, err := http.NewRequest(http.MethodDelete, alvus.URL+"/api/keys", strings.NewReader(reqBody))
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
	resp3, err := http.Get(alvus.URL + "/api/keys")
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
	alvus := newTestServer([]string{"key-a", "key-b", "key-c"})
	defer alvus.Close()

	resp, err := http.Post(alvus.URL+"/clear", "application/json", strings.NewReader(`{}`))
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
	state := server.NewServerState(cfg, pool, "")
	alvus := httptest.NewServer(state.Handler())
	defer alvus.Close()

	// Without token → 401
	resp, err := http.Get(alvus.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health (no auth): %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without token, got %d", resp.StatusCode)
	}

	// With wrong token → 401
	req, _ := http.NewRequest("GET", alvus.URL+"/health", nil)
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
	req, _ = http.NewRequest("GET", alvus.URL+"/health", nil)
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
	state := server.NewServerState(cfg, pool, "")
	alvus := httptest.NewServer(state.Handler())
	defer alvus.Close()

	// Without token → 401
	resp, err := http.Post(alvus.URL+"/clear", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("POST /clear (no auth): %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without token, got %d", resp.StatusCode)
	}

	// With wrong token → 401
	req, _ := http.NewRequest("POST", alvus.URL+"/clear", strings.NewReader(`{}`))
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
	req, _ = http.NewRequest("POST", alvus.URL+"/clear", strings.NewReader(`{}`))
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
	alvus := newTestServer([]string{"key-a", "key-b", "key-c"})
	defer alvus.Close()

	resp, err := http.Get(alvus.URL + "/api/stats")
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
	alvus := newTestServer([]string{"key-a", "key-b", "key-c"})
	defer alvus.Close()

	// 禁用 index=1
	req, _ := http.NewRequest("POST", alvus.URL+"/api/keys/1/disable", nil)
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
	resp2, err := http.Get(alvus.URL + "/api/keys")
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
	req2, _ := http.NewRequest("POST", alvus.URL+"/api/keys/999/disable", nil)
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
	state := server.NewServerState(cfg, pool, "")
	alvus := httptest.NewServer(state.Handler())
	defer alvus.Close()

	// Without token → 401
	req, _ := http.NewRequest("POST", alvus.URL+"/api/keys/1/disable", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST /api/keys/1/disable (no auth): %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without token, got %d", resp.StatusCode)
	}

	// Wrong token → 401
	req, _ = http.NewRequest("POST", alvus.URL+"/api/keys/1/disable", nil)
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
	req, _ = http.NewRequest("POST", alvus.URL+"/api/keys/1/disable", nil)
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
	alvus := newTestServer([]string{"key-a", "key-b", "key-c"})
	defer alvus.Close()

	// 冷却 index=1
	req, _ := http.NewRequest("PUT", alvus.URL+"/api/keys/1/cooldown", nil)
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
	req2, _ := http.NewRequest("PUT", alvus.URL+"/api/keys/999/cooldown", nil)
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
	alvus := newTestServer([]string{"key-a", "key-b", "key-c"})
	defer alvus.Close()

	// 删除 index=1
	req, _ := http.NewRequest("DELETE", alvus.URL+"/api/keys/1", nil)
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
	resp2, err := http.Get(alvus.URL + "/api/keys")
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
	req2, _ := http.NewRequest("DELETE", alvus.URL+"/api/keys/999", nil)
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
	state := server.NewServerState(cfg, pool, "")
	alvus := httptest.NewServer(state.Handler())
	defer alvus.Close()

	// Without token → 401
	req, _ := http.NewRequest("DELETE", alvus.URL+"/api/keys/1", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE /api/keys/1 (no auth): %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401 without token, got %d", resp.StatusCode)
	}

	// With correct token → 200
	req, _ = http.NewRequest("DELETE", alvus.URL+"/api/keys/1", nil)
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
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	tmpDir := t.TempDir()
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)

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
	state := server.NewServerState(cfg, pool, "")
	alvus := httptest.NewServer(state.Handler())
	defer alvus.Close()

	resp, err := http.Post(alvus.URL+"/api/reload", "application/json", strings.NewReader(`{}`))
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
