package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"time"

	"akswitch/internal/config"
	"akswitch/internal/keypool"
	"akswitch/internal/server"

	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the API key rotation proxy server",
	Long:  "Loads TOML configuration, initializes the key pool, and starts the HTTP proxy server on a single port with path-based provider routing.",
	Run: func(cmd *cobra.Command, args []string) {
		providerFilter, _ := cmd.Flags().GetString("provider")
		startAll, _ := cmd.Flags().GetBool("all")
		startServer(dashHTML, providerFilter, startAll)
	},
}

func startServer(dashboardHTML string, providerFilter string, startAll bool) {
	// ── Crash recovery ─────────────────────────────
	defer server.CrashRecover("startServer")

	// ── PID pre-check ───────────────────────────────
	if running, pid := checkPidFile(pidFilePath()); running {
		slog.Error("akswitch is already running", "pid", pid)
		fmt.Fprintf(os.Stderr, "akswitch is already running (PID %d). Stop it first with 'akswitch stop'.\n", pid)
		os.Exit(1)
	}

	// ── Default host ──────────────────────────────────
	host := "127.0.0.1"

	// ── Detect config source ──────────────────────────
	xdgPath, err := config.XDGConfigPath()
	if err != nil {
		slog.Error("config detection failed", "error", err)
		os.Exit(1)
	}

	// ── Load providers from TOML ──────────────────────
	providers, err := config.LoadAllTomlProviders(xdgPath)
	if err != nil {
		slog.Error("failed to load providers from TOML", "error", err)
		os.Exit(1)
	}
	if len(providers) == 0 {
		slog.Error("no providers found in TOML config")
		os.Exit(1)
	}

	// ── Create ProviderRouter ─────────────────────────
	router := server.NewProviderRouter(dashboardHTML)

	port := config.FindServerPort(xdgPath)

	// 四选一：--provider > --all > default_provider > 第一个 provider（字母序）
	var shouldStart func(name string) bool
	switch {
	case providerFilter != "":
		shouldStart = func(name string) bool { return name == providerFilter }
	case startAll:
		shouldStart = func(name string) bool { return true }
	case config.DefaultProviderName != "":
		shouldStart = func(name string) bool { return name == config.DefaultProviderName }
	default:
		names := slices.Sorted(maps.Keys(providers))
		first := names[0]
		slog.Info("default_provider 未配置，默认使用第一个 provider", "provider", first)
		shouldStart = func(name string) bool { return name == first }
	}

	for name, cfg := range providers {
		if !shouldStart(name) {
			slog.Debug("skipping provider", "name", name)
			continue
		}

		server.ApplyLogLevel(cfg.LogLevel)

		// Load API keys from encrypted store or env
		keys, keyNames := loadKeysForProvider(name, cfg)
		cfg.Keys = keys
		cfg.KeyNames = keyNames

		if err := cfg.Validate(); err != nil {
			slog.Error("invalid provider config", "provider", name, "error", err)
			continue
		}
		if len(keys) > 0 {
			keypool.SetEncryptionKey(cfg.EncryptionKey)
		}
		pool := keypool.NewKeyPool(keys, keyNames)
		if err := router.AddProvider(name, cfg, pool); err != nil {
			slog.Error("failed to add provider", "provider", name, "error", err)
			continue
		}
		slog.Info("provider configured",
			"name", name,
			"keys", len(keys),
			"target", cfg.TargetBase,
		)
	}

	// Warn if filter was set but no provider matched
	if providerFilter != "" {
		found := false
		for _, n := range router.ProviderNames() {
			if n == providerFilter {
				found = true
				break
			}
		}
		if !found {
			slog.Warn("no provider matched --provider filter", "provider", providerFilter)
		}
	}

	// ── Initialize file logging (from first provider) ──
	for _, cfg := range providers {
		server.InitFileHandler(cfg.LogFile, cfg.LogMaxSize, cfg.LogMaxAge)
		break
	}
	// ── Start server ──────────────────────────────────
	started := len(router.ProviderNames())
	if started == 0 {
		slog.Error("no providers configured, exiting")
		os.Exit(1)
	}
	if err := router.Start(host, port); err != nil {
		slog.Error("failed to start server", "error", err)
		os.Exit(1)
	}

	// ── Write PID file ─────────────────────────────────
	pidPath := pidFilePath()
	if err := os.MkdirAll(filepath.Dir(pidPath), 0755); err != nil {
		slog.Warn("failed to create PID file directory", "error", err)
	}
		pidData := []byte(fmt.Sprintf("%d\n", os.Getpid()))
	if err := os.WriteFile(pidPath, pidData, 0644); err != nil {
		slog.Warn("failed to write PID file", "error", err)
	}
	defer func() {
		if err := os.Remove(pidPath); err != nil && !os.IsNotExist(err) {
			slog.Warn("failed to remove PID file", "error", err)
		}
	}()

	// ── Background tasks ──────────────────────────────
	router.StartBackgroundTasks()

	// ── Signal handling ───────────────────────────────
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	// ── 二进制自监控（开发模式）─────────────────────────
	if exePath, err := os.Executable(); err == nil {
		SetupSelfRestart(exePath, sigCh)
	}

	<-sigCh
	slog.Info("shutting down")

	// ── Close file logger ────────────────────────────
	server.CloseFileHandler()

	// ── Graceful shutdown ─────────────────────────────
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	router.Shutdown(ctx)
	router.Stop()
	slog.Info("server stopped gracefully")

	// ── 自监控触发重启 ──────────────────────────────
	ExecRestart()
}

// loadKeysForProvider loads API keys for a provider from its keys file or env.
func loadKeysForProvider(name string, cfg *config.Config) (keys, names []string) {
	keys = cfg.Keys
	names = cfg.KeyNames

	// If a custom keys file is configured and has keys, use it
	if cfg.KeysFile != "" {
		fileKeys, fileNames, err := keypool.LoadKeysFromFile(cfg.KeysFile)
		if err == nil && fileKeys != nil {
			return fileKeys, fileNames
		}
		if len(keys) > 0 {
			_ = keypool.SaveKeysToFile(cfg.KeysFile, keys, names)
			return keys, names
		}
	}

	// Fallback: try the standard encrypted store path: <XDG>/keys/<name>.enc
	xdgPath, err := config.XDGConfigPath()
	if err != nil {
		return keys, names
	}
	keyFile := filepath.Join(filepath.Dir(xdgPath), "keys", name+".enc")
	fileKeys, fileNames, err := keypool.LoadKeysFromFile(keyFile)
	if err == nil && fileKeys != nil {
		return fileKeys, fileNames
	}

	return keys, names
}

// pidFilePath returns the path to the PID file, located in the config directory.
func pidFilePath() string {
	xdgPath, err := config.XDGConfigPath()
	if err != nil {
		return "akswitch.pid"
	}
	return filepath.Join(filepath.Dir(xdgPath), "akswitch.pid")
}

// checkPidFile reads the PID file and checks if the process is still running.
// Returns (true, pid) if running, (false, 0) otherwise.
func checkPidFile(pidFile string) (bool, int) {
	data, err := os.ReadFile(pidFile)
	if err != nil {
		return false, 0
	}

	pidStr := strings.TrimSpace(string(data))
	pid, err := strconv.Atoi(pidStr)
	if err != nil || pid <= 0 {
		return false, 0
	}

	if runtime.GOOS == "windows" {
		// Windows: use tasklist to check process existence
		cmd := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH")
		out, err := cmd.Output()
		if err != nil {
			return false, 0
		}
		return strings.Contains(string(out), strconv.Itoa(pid)), pid
	}

	// Unix: signal 0 checks process existence without sending a signal
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false, 0
	}
	defer proc.Release()
	return proc.Signal(syscall.Signal(0)) == nil, pid
}

func init() {
	startCmd.Flags().String("provider", "", "Only start the specified provider")
	startCmd.Flags().Bool("all", false, "Start all providers (default: first provider alphabetically, or error if none configured)")
	rootCmd.AddCommand(startCmd)
}
