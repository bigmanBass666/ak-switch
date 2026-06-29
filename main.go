package main

import (
	"context"
	_ "embed"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"alvus/internal/server"
)

//go:embed dashboard.html
var dashboardHTML string

// ── Main ──────────────────────────────────────

func main() {
	isLocal := flag.Bool("local", false, "Bind to 127.0.0.1 (local access only)")
	isNetwork := flag.Bool("network-only", false, "Bind to 0.0.0.0 (accessible via LAN)")
	managePath := flag.String("manage", "", "Path to manage.json for multi-instance mode")
	processTag := flag.String("tag", "", "Process identity tag (empty = production)")
	flag.Parse()

	// Shared stop channel for graceful shutdown
	stop := make(chan struct{})
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigCh
		slog.Info("shutting down")
		close(stop)
	}()

	// ── Manage Mode ────────────────────────────
	if *managePath != "" {
		runManager(*managePath, *processTag, stop)
		return
	}

	// ── Single Instance Mode (original) ────────
	host := "" // Default (binds to all interfaces)
	if *isLocal {
		host = "127.0.0.1"
	} else if *isNetwork {
		host = "0.0.0.0"
	}

	cfg, pool := server.LoadConfig()
	state := server.NewServerState(cfg, pool, dashboardHTML, cfg.KeysFile)

	// Initial key pool metric refresh
	state.Metrics().RefreshKeyPoolGauge(pool)

	go server.WatchEnvFile(state, stop)
	go server.RefreshKeyPoolMetrics(state, stop)

	addr := fmt.Sprintf("%s:%d", host, cfg.Port)

	// Check port availability and bind
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		slog.Error("port in use", "port", cfg.Port, "error", err)
		log.Fatalf("port %d is already in use: %v", cfg.Port, err)
	}

	httpServer := &http.Server{Handler: state.Handler()}

	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(ctx); err != nil {
			slog.Error("shutdown error", "error", err)
		}
	}()

	displayHost := host
	if displayHost == "" {
		displayHost = "0.0.0.0"
	}
	if *processTag != "" {
		slog.Info("starting", "tag", *processTag, "port", cfg.Port, "keys", len(pool.Keys()), "target", cfg.TargetBase, "genai", cfg.GenaiBase)
	} else {
		slog.Info("starting", "port", cfg.Port, "keys", len(pool.Keys()), "target", cfg.TargetBase, "genai", cfg.GenaiBase)
	}
	if err := httpServer.Serve(listener); err != http.ErrServerClosed {
		slog.Error("server error", "error", err)
		log.Fatalf("Server error: %v", err)
	}
}
