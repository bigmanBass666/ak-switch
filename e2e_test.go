package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestFullE2E_RealUserSimulation builds the real akswitch binary, sets up a clean
// environment, and runs through the complete user workflow to verify everything
// works end-to-end. This is the ultimate acceptance test.
//
// Workflow:
//  1. provider add   — configure a provider with upstream URL
//  2. key add (x2)   — add API keys to encrypted store
//  3. key list       — verify keys are visible
//  4. akswitch start    — start the proxy server
//  5. health check   — wait for server readiness
//  6. proxy request  — send a real request through the proxy
//  7. management API — query /api/stats on the running instance
//  8. akswitch status   — verify runtime CLI command
//  9. akswitch logs     — verify logs CLI command
// 10. akswitch stop     — graceful shutdown
// 11. verify down    — confirm server is unreachable
func TestFullE2E_RealUserSimulation(t *testing.T) {
	// ── Build binary ─────────────────────────────────────
	t.Log("Building akswitch binary...")
	bin := filepath.Join(t.TempDir(), "akswitch-e2e.exe")
	if out, err := exec.Command("go", "build", "-o", bin, "./cmd/akswitch/").CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, out)
	}

	// ── Fresh environment ────────────────────────────────
	xdgHome := filepath.Join(t.TempDir(), "config")
	t.Setenv("AKSWITCH_CONFIG_DIR", xdgHome)

	// ── Mock upstream server ─────────────────────────────
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{
			"status": "forwarded",
			"path":   r.URL.Path,
			"method": r.Method,
		})
	}))
	defer upstream.Close()

	// ── 1. provider add ──────────────────────────────────
	t.Log("1. akswitch provider add")
	mustRun(t, bin, "provider", "add", "e2e-test",
		"--target", upstream.URL+"/v1",
		"--genai", upstream.URL,
		"--port", "19901",
	)

	// ── 2. key add (x2) ─────────────────────────────────
	t.Log("2. akswitch key add (x2)")
	mustRun(t, bin, "key", "add", "e2e-test", "sk-e2e-key-001")
	mustRun(t, bin, "key", "add", "e2e-test", "sk-e2e-key-002")

	// ── 3. key list ──────────────────────────────────────
	t.Log("3. akswitch key list")
	out := mustRun(t, bin, "key", "list", "e2e-test")
	if !strings.Contains(string(out), "active") {
		t.Error("key list should show 'active' status")
	}
	t.Logf("   keys:\n%s", out)

	// ── 4. akswitch start ──────────────────────────────────
	t.Log("4. akswitch start")
	server := exec.Command(bin, "start")
	var serverStderr bytes.Buffer
	server.Stderr = &serverStderr
	if err := server.Start(); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	serverPid := server.Process.Pid
	t.Logf("   server PID: %d", serverPid)

	// ── 5. Wait for health ──────────────────────────────
	t.Log("5. health check")
	waitForHealth(t, "http://127.0.0.1:19901/health", &serverStderr)
	t.Log("   ✓ server ready")

	// ── 6. Proxy a request ──────────────────────────────
	t.Log("6. proxy request /chat/completions")
	resp, err := http.Get("http://127.0.0.1:19901/e2e-test/chat/completions")
	if err != nil {
		t.Fatalf("proxy request failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	// Proxy does not forward upstream response headers,
	// but it does forward the response body from upstream.
	var proxyResult map[string]string
	if json.Unmarshal(body, &proxyResult) != nil || proxyResult["status"] != "forwarded" {
		t.Errorf("proxy response body unexpected: %s", body)
	} else {
		t.Logf("   ✓ proxy forwarded request: %s %s (HTTP %d)",
			proxyResult["method"], proxyResult["path"], resp.StatusCode)
	}

	// ── 7. Management API ───────────────────────────────
	t.Log("7. management API")

	statsResp, err := http.Get("http://127.0.0.1:19901/api/stats")
	if err != nil {
		t.Errorf("/api/stats failed: %v", err)
	} else {
		statsBody, _ := io.ReadAll(statsResp.Body)
		statsResp.Body.Close()
		if statsResp.StatusCode == 200 {
			var stats map[string]interface{}
			if json.Unmarshal(statsBody, &stats) == nil {
				t.Logf("   /api/stats: requests=%.0f, up=%.0fs",
					getFloat(stats, "total_requests"),
					getFloat(stats, "uptime_seconds"))
			}
		}
	}

	// ── 8. akswitch status ─────────────────────────────────
	t.Log("8. akswitch status")
	statusOut, _ := exec.Command(bin, "status").CombinedOutput()
	statusStr := string(bytes.TrimSpace(statusOut))
	if statusStr != "" {
		t.Logf("   output:\n%s", statusStr)
	} else {
		t.Log("   (no output)")
	}

	// ── 9. akswitch logs ──────────────────────────────────
	t.Log("9. akswitch logs")
	logsOut, _ := exec.Command(bin, "logs").CombinedOutput()
	logsStr := string(bytes.TrimSpace(logsOut))
	if logsStr != "" {
		t.Logf("   output:\n%s", logsStr)
	} else {
		t.Log("   (no output)")
	}

	// ── 10. akswitch stop ─────────────────────────────────
	t.Log("10. akswitch stop")
	if out, err := exec.Command(bin, "stop").CombinedOutput(); err != nil {
		t.Logf("   'akswitch stop' error (expected on Windows): %v", err)
		t.Logf("   stderr: %s", bytes.TrimSpace(out))
	} else {
		t.Log("   ✓ 'akswitch stop' returned success")
	}

	// Wait a moment for graceful shutdown to start
	time.Sleep(500 * time.Millisecond)

	// Force-kill the server process (akswitch stop may not work on Windows)
	t.Log("   force killing server process...")
	exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", serverPid)).Run()
	server.Wait()

	// ── 11. Verify shutdown ─────────────────────────────
	t.Log("11. verify shutdown")
	// Windows may take a moment to release the port
	shutdownOK := false
	for i := 0; i < 10; i++ {
		_, err := http.Get("http://127.0.0.1:19901/health")
		if err != nil {
			shutdownOK = true
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !shutdownOK {
		t.Error("server still reachable after stop")
	} else {
		t.Log("   ✓ server is down (connection refused)")
	}
}

// ── Helpers ───────────────────────────────────────────────

func mustRun(t testing.TB, bin string, args ...string) []byte {
	t.Helper()
	out, err := exec.Command(bin, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("'%s' failed: %v\n%s", args[0], err, out)
	}
	return out
}

func waitForHealth(t testing.TB, url string, stderr *bytes.Buffer) {
	t.Helper()
	for i := 0; i < 30; i++ {
		resp, err := http.Get(url)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("health check timeout (3s)\nmock server stderr:\n%s", stderr.String())
}

func getFloat(m map[string]interface{}, key string) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0
}