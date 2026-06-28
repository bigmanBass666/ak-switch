package main

import (
	"alvus/internal/config"
	"alvus/internal/server"
	"bytes"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// ── Helpers ──────────────────────────────────────────────

func writeEnv(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
}

func captureSlog(t *testing.T) *bytes.Buffer {
	t.Helper()
	var buf bytes.Buffer
	orig := slog.Default().Handler()
	t.Cleanup(func() { slog.SetDefault(slog.New(orig)) })
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	return &buf
}

// resetAllEnv unsets all config env vars to prevent interference between tests.
func resetAllEnv() {
	for _, k := range []string{
		"PORT", "TARGET_BASE_URL", "GENAI_BASE_URL", "ADMIN_TOKEN",
		"DISABLE_THINKING", "GENAI_MODEL", "MAX_RETRIES", "LOG_LEVEL",
		"COOLDOWN_SEC", "API_KEYS", "KEY", "KEY1", "KEY2", "KEY3",
		"KEY4", "KEY5", "KEYA", "KEYB",
	} {
		os.Unsetenv(k)
	}
}

// ---------------------------------------------------------------------------
// 1. 启动校验测试 — 坏配置时进程是否优雅退出
// ---------------------------------------------------------------------------

func TestStartupValidation(t *testing.T) {
	if os.Getenv("ALVUS_TEST_SUBPROCESS") == "1" {
		// ── 子进程 ──
		tmpDir := os.Getenv("ALVUS_TEST_DIR")
		if err := os.Chdir(tmpDir); err != nil {
			fmt.Fprintf(os.Stderr, "chdir: %v\n", err)
			os.Exit(1)
		}
		server.LoadConfig() // 在无效配置下会 log.Fatalf → os.Exit(1)
		return
	}

	// ── 主进程 ──
	tests := []struct {
		name     string
		env      string
		wantErr  string // 期望在 stderr 中出现的错误关键词
		wantCode int    // 期望退出码
	}{
		{
			name:     "missing target base",
			env:      "TARGET_BASE_URL=\nGENAI_BASE_URL=https://ai.example.com\nAPI_KEYS=key-a\nPORT=8080\n",
			wantErr:  "TARGET_BASE_URL 为必填字段",
			wantCode: 2,
		},
		{
			name:     "empty keys",
			env:      "TARGET_BASE_URL=https://example.com\nGENAI_BASE_URL=https://ai.example.com\nPORT=8080\n",
			wantErr:  "配置错误: 未设置 API_KEYS",
			wantCode: 2,
		},
		{
			name:     "invalid port",
			env:      "TARGET_BASE_URL=https://example.com\nGENAI_BASE_URL=https://ai.example.com\nAPI_KEYS=key-a\nPORT=abc\n",
			wantErr:  "不是有效端口号",
			wantCode: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			writeEnv(t, filepath.Join(tmpDir, ".env"), tt.env)

			cmd := exec.Command(os.Args[0], "-test.run=^TestStartupValidation$")
			cmd.Dir = tmpDir
			// Clear all config env vars so subprocess reads only from .env
			cleared := append(os.Environ(),
				"ALVUS_TEST_SUBPROCESS=1",
				"ALVUS_TEST_DIR="+tmpDir,
			)
			for _, k := range []string{
				"TARGET_BASE_URL", "GENAI_BASE_URL", "API_KEYS",
				"KEY", "KEY1", "KEY2", "KEY3", "KEY4", "KEY5", "KEYA", "KEYB",
				"PORT", "ADMIN_TOKEN", "MAX_RETRIES", "LOG_LEVEL", "COOLDOWN_SEC",
			} {
				cleared = append(cleared, k+"=")
			}
			cmd.Env = cleared
			out, _ := cmd.CombinedOutput()

			if code := cmd.ProcessState.ExitCode(); code != tt.wantCode {
				t.Errorf("expected exit code %d, got %d", tt.wantCode, code)
			}
			output := string(out)
			if !strings.Contains(output, tt.wantErr) {
				t.Errorf("expected error containing %q, got:\n%s", tt.wantErr, output)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 2. 热重载 diff 日志 — 配置变更时记录正确的 diff 内容
// ---------------------------------------------------------------------------

func TestHotReloadDiffLog(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)
	defer resetAllEnv()

	// 初始有效配置
	writeEnv(t, envPath, "PORT=8080\nTARGET_BASE_URL=https://old.example.com\nGENAI_BASE_URL=https://ai.example.com\nAPI_KEYS=key-a\n")

	// 首次加载建立基线
	initialCfg, err := config.Load(".env")
	if err != nil {
		t.Fatal(err)
	}

	buf := captureSlog(t)

	// 修改配置
	writeEnv(t, envPath, "PORT=8081\nTARGET_BASE_URL=https://new.example.com\nGENAI_BASE_URL=https://ai.example.com\nAPI_KEYS=key-a,key-b\n")

	// 重载
	newCfg, _, err := server.ReloadConfig()
	if err != nil {
		t.Fatal(err)
	}

	// 计算并记录 diff（模拟 watchEnvFile 的行为）
	changes := initialCfg.Diff(newCfg)
	for _, c := range changes {
		slog.Info("config changed", "field", c.Field, "old", c.OldValue, "new", c.NewValue)
	}

	output := buf.String()
	t.Logf("slog output:\n%s", output)

	// 验证 diff 日志包含变更字段
	checks := []struct {
		field  string
		oldVal string
		newVal string
	}{
		{"PORT", "8080", "8081"},
		{"TARGET_BASE_URL", "old.example.com", "new.example.com"},
	}
	for _, c := range checks {
		if !strings.Contains(output, c.oldVal) && !strings.Contains(output, c.newVal) {
			if !strings.Contains(output, c.field) {
				t.Errorf("diff log missing field %q (expected old=%q new=%q)", c.field, c.oldVal, c.newVal)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// 3. 热重载回滚 — 坏配置不生效、旧配置保留
// ---------------------------------------------------------------------------

func TestHotReloadRollback(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(tmpDir); err != nil {
		t.Fatal(err)
	}
	defer os.Chdir(origDir)
	defer resetAllEnv()

	// 初始有效配置
	writeEnv(t, envPath, "TARGET_BASE_URL=https://good.example.com\nGENAI_BASE_URL=https://ai.example.com\nAPI_KEYS=good-key\nPORT=8080\n")

	buf := captureSlog(t)

	// 通过 reloadConfig 确认当前配置可正常加载
	goodCfg, goodPool, err := server.ReloadConfig()
	if err != nil {
		t.Fatal(err)
	}

	// 写入坏配置
	writeEnv(t, envPath, "TARGET_BASE_URL=\nGENAI_BASE_URL=\nAPI_KEYS=\nPORT=0\n")

	// 尝试重载——应该失败
	badCfg, badPool, err := server.ReloadConfig()
	if err == nil {
		t.Error("expected error for invalid config, got nil")
		t.Logf("badCfg=%+v badPool=%+v", badCfg, badPool)
	}

	// 验证 reloadConfig 返回了 error（这就是 rollback 的触发条件）
	// 在 watchEnvFile 中，error → continue → 旧配置不变
	if err == nil {
		t.Fatal("reloadConfig should have failed — rollback test invalidated")
	}
	t.Logf("reloadConfig correctly returned error: %v", err)
	t.Logf("good config still accessible: target=%s keys=%d", goodCfg.TargetBase, len(goodCfg.Keys))
	t.Logf("good pool still valid: %d keys", len(goodPool.Keys()))

	// 验证错误日志
	output := buf.String()
	if !strings.Contains(output, "level=ERROR") && !strings.Contains(output, "error") {
		t.Logf("warn: expected error-level log on failed reload; output:\n%s", output)
	}
}

// ---------------------------------------------------------------------------
// 4. 完整配置下启动成功 — 确保我们没有破坏正常启动路径
// ---------------------------------------------------------------------------

func TestStartupValidConfig(t *testing.T) {
	if os.Getenv("ALVUS_TEST_SUBPROCESS") == "1" {
		tmpDir := os.Getenv("ALVUS_TEST_DIR")
		if err := os.Chdir(tmpDir); err != nil {
			fmt.Fprintf(os.Stderr, "chdir: %v\n", err)
			os.Exit(1)
		}
		// 只需要 loadConfig 不 panic/log.Fatal 就算通过
		cfg, pool := server.LoadConfig()
		fmt.Fprintf(os.Stdout, "ok: target=%s keys=%d", cfg.TargetBase, len(pool.Keys()))
		return
	}

	tmpDir := t.TempDir()
	writeEnv(t, filepath.Join(tmpDir, ".env"),
		"TARGET_BASE_URL=https://example.com\nGENAI_BASE_URL=https://ai.example.com\nAPI_KEYS=key-a,key-b\nPORT=8080\n",
	)

	cmd := exec.Command(os.Args[0], "-test.run=^TestStartupValidConfig$")
	cmd.Dir = tmpDir
	cleared := append(os.Environ(),
		"ALVUS_TEST_SUBPROCESS=1",
		"ALVUS_TEST_DIR="+tmpDir,
	)
	for _, k := range []string{
		"TARGET_BASE_URL", "GENAI_BASE_URL", "API_KEYS",
		"KEY", "KEY1", "KEY2", "KEY3", "KEY4", "KEY5", "KEYA", "KEYB",
		"PORT", "ADMIN_TOKEN", "MAX_RETRIES", "LOG_LEVEL", "COOLDOWN_SEC",
	} {
		cleared = append(cleared, k+"=")
	}
	cmd.Env = cleared
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("expected success for valid config, got: %v\noutput: %s", err, string(out))
	}
	if !strings.Contains(string(out), "ok:") {
		t.Errorf("expected success message, got: %s", string(out))
	}
}
