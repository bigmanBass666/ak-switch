package cmd

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"alvus/internal/config"
	"alvus/internal/keypool"
	"alvus/internal/server"

	"github.com/spf13/cobra"
)

var startCmd = &cobra.Command{
	Use:   "start",
	Short: "Start the API key rotation proxy server",
	Long:  "Loads configuration, initializes the key pool, and starts the HTTP proxy server. Supports both single-provider (.env) and multi-provider (config.toml) modes.",
	Run: func(cmd *cobra.Command, args []string) {
		local, _ := cmd.Flags().GetBool("local")
		networkOnly, _ := cmd.Flags().GetBool("network-only")
		tag, _ := cmd.Flags().GetString("tag")
		startServer(dashHTML, local, networkOnly, tag)
	},
}

func startServer(dashboardHTML string, isLocal, isNetwork bool, processTag string) {
	// ── Host binding ──────────────────────────────
	host := "0.0.0.0" // Default (binds to all interfaces)
	if isLocal {
		host = "127.0.0.1"
	} else if isNetwork {
		host = "0.0.0.0"
	}

	// ── Detect config source ──────────────────────
	source, fromToml, err := config.DetectConfigSource("")
	if err != nil {
		slog.Error("config detection failed", "error", err)
		os.Exit(1)
	}

	// ── Create InstanceManager ────────────────────
	mgr := server.NewInstanceManager(dashboardHTML)

	if fromToml {
		// ── TOML mode: multiple providers ────────────
		providers, err := config.LoadAllTomlProviders(source)
		if err != nil {
			slog.Error("failed to load providers from TOML", "error", err)
			os.Exit(1)
		}
		if len(providers) == 0 {
			slog.Error("no providers found in TOML config")
			os.Exit(1)
		}
		for name, cfg := range providers {
			if err := cfg.Validate(); err != nil {
				slog.Error("invalid provider config", "provider", name, "error", err)
				continue
			}
			server.ApplyLogLevel(cfg.LogLevel)

			// Load API keys from keys file or env
			keys, keyNames := loadKeysForProvider(cfg)
			cfg.Keys = keys
			cfg.KeyNames = keyNames
			if len(keys) == 0 {
				slog.Warn("no API keys configured, provider will be unavailable", "provider", name)
			} else {
				keypool.SetEncryptionKey(cfg.EncryptionKey)
			}
			pool := keypool.NewKeyPool(keys, keyNames)
			inst := mgr.AddInstance(name, cfg, pool)
			_ = inst // tag not used on instance yet
			slog.Info("provider configured",
				"name", name,
				"port", cfg.Port,
				"keys", len(keys),
				"target", cfg.TargetBase,
			)
		}
	} else {
		// ── .env mode: single provider (backward compat) ──
		cfg, pool := server.LoadConfig()
		server.ApplyLogLevel(cfg.LogLevel)
		mgr.AddInstance("default", cfg, pool)
		slog.Info("provider configured (from .env)",
			"port", cfg.Port,
			"keys", len(pool.Keys()),
			"target", cfg.TargetBase,
		)
	}

	// ── Start all instances ───────────────────────
	started := len(mgr.InstanceNames())
	if started == 0 {
		slog.Error("no instances configured, exiting")
		os.Exit(1)
	}
	if err := mgr.StartAll(host); err != nil {
		slog.Error("some instances failed to start", "error", err)
	}

	// ── Background tasks ──────────────────────────
	mgr.StartBackgroundTasks()

	// ── Signal handling ───────────────────────────
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh
	slog.Info("shutting down")

	// ── Graceful shutdown ─────────────────────────
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	mgr.Shutdown(ctx)
	mgr.Stop()
	slog.Info("all instances stopped gracefully")
}

// loadKeysForProvider loads API keys for a provider from its keys file or env.
func loadKeysForProvider(cfg *config.Config) (keys, names []string) {
	keys = cfg.Keys
	names = cfg.KeyNames
	if cfg.KeysFile != "" {
		fileKeys, fileNames, err := keypool.LoadKeysFromFile(cfg.KeysFile)
		if err == nil && fileKeys != nil {
			// File exists — use its keys as source of truth
			return fileKeys, fileNames
		}
		// File not found — auto-create from env keys if available
		if len(keys) > 0 {
			_ = keypool.SaveKeysToFile(cfg.KeysFile, keys, names)
		}
	}
	return keys, names
}

func init() {
	rootCmd.AddCommand(startCmd)
}