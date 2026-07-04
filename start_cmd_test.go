package main

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"akswitch/internal/cmd"
	"akswitch/internal/config"
)

// ── Test: akswitch start (TOML 模式，完整启动) ─────────────────
//
// 子进程模式，完整模拟用户操作链：
//
//	provider add → key add → akswitch start
//
// 验证：服务器启动后 health endpoint 可达。
func TestStartCmd_TOMLMode(t *testing.T) {
	if os.Getenv("ALVUS_TEST_START_CHILD") == "1" {
		os.Args = []string{"akswitch", "start"}
		cmd.PidFileName = filepath.Join(os.Getenv("AKSWITCH_CONFIG_DIR"), "akswitch.pid")
		cmd.Execute("")
		return
	}

	resetConfigEnv()
	tmpDir := t.TempDir()
	config.ConfigDir = tmpDir
t.Cleanup(func() { config.ConfigDir = "" })

	runCommand(t, "akswitch", "provider", "add", "testp",
		"--target", "http://localhost:18999/v1",
		"--genai", "http://localhost:18999",
		"--port", "19301",
	)
	runCommand(t, "akswitch", "key", "add", "testp", "sk-test-key-12345")

	testExe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable failed: %v", err)
	}

	cmd := exec.Command(testExe, "-test.run=^TestStartCmd_TOMLMode$")
	cmd.Env = append(os.Environ(),
		"ALVUS_TEST_START_CHILD=1",
		"AKSWITCH_CONFIG_DIR="+tmpDir,
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

// ── Test: akswitch start — 缺少 Key 时的错误处理 ──────────────
func TestStartCmd_NoKeys(t *testing.T) {
	if os.Getenv("ALVUS_TEST_START_CHILD") == "1" {
		os.Args = []string{"akswitch", "start"}
		cmd.PidFileName = filepath.Join(os.Getenv("AKSWITCH_CONFIG_DIR"), "akswitch.pid")
		cmd.Execute("")
		return
	}

	resetConfigEnv()
	tmpDir := t.TempDir()
	config.ConfigDir = tmpDir
t.Cleanup(func() { config.ConfigDir = "" })

	runCommand(t, "akswitch", "provider", "add", "nokey",
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
		"AKSWITCH_CONFIG_DIR="+tmpDir,
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

// ── Test: akswitch start --provider 过滤 ─────────────────────
func TestStartCmd_ProviderFilter(t *testing.T) {
	if os.Getenv("ALVUS_TEST_START_CHILD") == "1" {
		os.Args = []string{"akswitch", "start", "--provider", "test-a"}
		cmd.PidFileName = filepath.Join(os.Getenv("AKSWITCH_CONFIG_DIR"), "akswitch.pid")
		cmd.Execute("")
		return
	}

	resetConfigEnv()
	tmpDir := t.TempDir()
	config.ConfigDir = tmpDir
t.Cleanup(func() { config.ConfigDir = "" })

	runCommand(t, "akswitch", "provider", "add", "test-a",
		"--target", "http://localhost:18999/v1",
		"--genai", "http://localhost:18999",
		"--port", "19305",
	)
	runCommand(t, "akswitch", "key", "add", "test-a", "sk-test-key-aaa")

	runCommand(t, "akswitch", "provider", "add", "test-b",
		"--target", "http://localhost:18999/v1",
		"--genai", "http://localhost:18999",
		"--port", "19306",
	)
	runCommand(t, "akswitch", "key", "add", "test-b", "sk-test-key-bbb")

	testExe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable failed: %v", err)
	}

	cmd := exec.Command(testExe, "-test.run=^TestStartCmd_ProviderFilter$")
	cmd.Env = append(os.Environ(),
		"ALVUS_TEST_START_CHILD=1",
		"AKSWITCH_CONFIG_DIR="+tmpDir,
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
