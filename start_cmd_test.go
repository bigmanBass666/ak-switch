package main

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"alvus/internal/cmd"
)

// ── Test: alvus start (TOML 模式，完整启动) ─────────────────
//
// 子进程模式，完整模拟用户操作链：
//
//	provider add → key add → alvus start
//
// 验证：服务器启动后 health endpoint 可达。
func TestStartCmd_TOMLMode(t *testing.T) {
	if os.Getenv("ALVUS_TEST_START_CHILD") == "1" {
		os.Args = []string{"alvus", "start"}
		cmd.Execute("")
		return
	}

	resetConfigEnv()
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	runCommand(t, "alvus", "provider", "add", "testp",
		"--target", "http://localhost:18999/v1",
		"--genai", "http://localhost:18999",
		"--port", "19301",
	)
	runCommand(t, "alvus", "key", "add", "testp", "sk-test-key-12345")

	testExe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable failed: %v", err)
	}

	cmd := exec.Command(testExe, "-test.run=^TestStartCmd_TOMLMode$")
	cmd.Env = append(os.Environ(),
		"ALVUS_TEST_START_CHILD=1",
		"XDG_CONFIG_HOME="+tmpDir,
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("subprocess start failed: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	var healthOK bool
	for i := 0; i < 50; i++ {
		resp, err := http.Get("http://127.0.0.1:19301/health")
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 {
				healthOK = true
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !healthOK {
		t.Fatal("server health check never returned 200 within 5s")
	}
}

// ── Test: alvus start — 缺少 Key 时的错误处理 ──────────────
func TestStartCmd_NoKeys(t *testing.T) {
	if os.Getenv("ALVUS_TEST_START_CHILD") == "1" {
		os.Args = []string{"alvus", "start"}
		cmd.Execute("")
		return
	}

	resetConfigEnv()
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	runCommand(t, "alvus", "provider", "add", "nokey",
		"--target", "http://localhost:18999/v1",
		"--genai", "http://localhost:18999",
		"--port", "19302",
	)
	// 故意不加 Key

	testExe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable failed: %v", err)
	}

	cmd := exec.Command(testExe, "-test.run=^TestStartCmd_NoKeys$")
	cmd.Env = append(os.Environ(),
		"ALVUS_TEST_START_CHILD=1",
		"XDG_CONFIG_HOME="+tmpDir,
	)
	out, err := cmd.CombinedOutput()
	output := string(out)

	if err == nil {
		t.Fatal("expected error for missing keys, got exit code 0")
	}
	if !strings.Contains(output, "no providers configured") &&
		!strings.Contains(output, "no API keys") &&
		!strings.Contains(output, "invalid provider config") {
		t.Errorf("expected error about missing keys in output, got:\n%s", output)
	}
}

// ── Test: alvus start --provider 过滤 ─────────────────────
func TestStartCmd_ProviderFilter(t *testing.T) {
	if os.Getenv("ALVUS_TEST_START_CHILD") == "1" {
		os.Args = []string{"alvus", "start", "--provider", "test-a"}
		cmd.Execute("")
		return
	}

	resetConfigEnv()
	tmpDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", tmpDir)

	runCommand(t, "alvus", "provider", "add", "test-a",
		"--target", "http://localhost:18999/v1",
		"--genai", "http://localhost:18999",
		"--port", "19305",
	)
	runCommand(t, "alvus", "key", "add", "test-a", "sk-test-key-aaa")

	runCommand(t, "alvus", "provider", "add", "test-b",
		"--target", "http://localhost:18999/v1",
		"--genai", "http://localhost:18999",
		"--port", "19306",
	)
	runCommand(t, "alvus", "key", "add", "test-b", "sk-test-key-bbb")

	testExe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable failed: %v", err)
	}

	cmd := exec.Command(testExe, "-test.run=^TestStartCmd_ProviderFilter$")
	cmd.Env = append(os.Environ(),
		"ALVUS_TEST_START_CHILD=1",
		"XDG_CONFIG_HOME="+tmpDir,
	)
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatalf("subprocess start failed: %v", err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	})

	var healthOK bool
	for i := 0; i < 50; i++ {
		resp, err := http.Get("http://127.0.0.1:19305/health")
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 {
				healthOK = true
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !healthOK {
		t.Fatal("server health check never returned 200 within 5s")
	}
}
