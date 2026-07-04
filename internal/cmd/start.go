package cmd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
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
		startServer(dashHTML, providerFilter)
	},
}

func startServer(dashboardHTML string, providerFilter string) {
	// ── Crash recovery ─────────────────────────────
	defer server.CrashRecover("startServer")

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

	for name, cfg := range providers {
		// Apply provider filter first
		if providerFilter != "" && name != providerFilter {
			slog.Debug("skipping provider (filtered by --provider)", "name", name)
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
	pidData := []byte(fmt.Sprintf("%d\n", os.Getpid()))
	if err := os.WriteFile(pidFileName, pidData, 0644); err != nil {
		slog.Warn("failed to write PID file", "error", err)
	}
	defer func() {
		if err := os.Remove(pidFileName); err != nil && !os.IsNotExist(err) {
			slog.Warn("failed to remove PID file", "error", err)
		}
	}()

	// ── Background tasks ──────────────────────────────
	router.StartBackgroundTasks()

	// ── Signal handling ───────────────────────────────
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
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

func init() {
	rootCmd.AddCommand(startCmd)
}
