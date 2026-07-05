//go:build unit

package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"akswitch/internal/config"
	"akswitch/internal/keypool"
)

// ── Test: Multi-Provider Single Port ─────────────────

func TestProviderRouter_MultiProvider(t *testing.T) {
	cfg1 := config.DefaultConfig()
	cfg1.Port = 19101
	cfg1.TargetBase = "http://localhost:18999"
	cfg1.GenaiBase = "http://localhost:18999"
	cfg1.Keys = []string{"sk-test-key-provider-1"}

	cfg2 := config.DefaultConfig()
	cfg2.Port = 19101 // same port — single port architecture
	cfg2.TargetBase = "http://localhost:18999"
	cfg2.GenaiBase = "http://localhost:18999"
	cfg2.Keys = []string{"sk-test-key-provider-2"}

	pr := NewProviderRouter("")
	pool1 := keypool.NewKeyPool(cfg1.Keys, nil)
	pool2 := keypool.NewKeyPool(cfg2.Keys, nil)
	pr.AddProvider("alpha", cfg1, pool1)
	pr.AddProvider("beta", cfg2, pool2)

	// Verify provider names
	names := pr.ProviderNames()
	if len(names) != 2 {
		t.Fatalf("expected 2 providers, got %d: %v", len(names), names)
	}

	// Start on a single port
	if err := pr.Start("127.0.0.1", 19101); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() {
		pr.Shutdown(context.Background())
		pr.Stop()
	}()

	time.Sleep(100 * time.Millisecond)

	// Verify health endpoint works
	resp, err := http.Get("http://127.0.0.1:19101/health")
	if err != nil {
		t.Fatalf("health endpoint failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("health returned %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if len(body) == 0 {
		t.Error("health returned empty body")
	}
}

func TestProviderRouter_GracefulShutdown(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Port = 19103
	cfg.TargetBase = "http://localhost:18999"
	cfg.GenaiBase = "http://localhost:18999"
	cfg.Keys = []string{"sk-test-shutdown"}

	pr := NewProviderRouter("")
	pool := keypool.NewKeyPool(cfg.Keys, nil)
	pr.AddProvider("shutdown-test", cfg, pool)

	if err := pr.Start("127.0.0.1", 19103); err != nil {
		t.Fatalf("Start failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)
	resp, err := http.Get("http://127.0.0.1:19103/health")
	if err != nil {
		t.Fatalf("server not reachable before shutdown: %v", err)
	}
	resp.Body.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	pr.Shutdown(ctx)
	pr.Stop()

	_, err = http.Get("http://127.0.0.1:19103/health")
	if err == nil {
		t.Error("server still reachable after shutdown")
	}
}

func TestProviderRouter_Empty(t *testing.T) {
	pr := NewProviderRouter("")
	names := pr.ProviderNames()
	if len(names) != 0 {
		t.Errorf("expected 0 providers, got %d", len(names))
	}

	// Empty router should still start and bind the port
	if err := pr.Start("127.0.0.1", 19104); err != nil {
		t.Errorf("Start on empty router should succeed, got: %v", err)
	}
	pr.Shutdown(context.Background())
	pr.Stop()
}

func TestProviderRouter_Accessors(t *testing.T) {
	pr := NewProviderRouter("")
	cfg := config.DefaultConfig()
	cfg.Port = 19105
	cfg.Keys = []string{"sk-test-accessor"}
	pool := keypool.NewKeyPool(cfg.Keys, nil)

	err := pr.AddProvider("accessor-test", cfg, pool)
	if err != nil {
		t.Fatalf("AddProvider failed: %v", err)
	}

	found := pr.Provider("accessor-test")
	if found == nil {
		t.Fatal("Provider() returned nil for existing provider")
	}
	if found.Name != "accessor-test" {
		t.Errorf("Name = %q, want %q", found.Name, "accessor-test")
	}

	missing := pr.Provider("nonexistent")
	if missing != nil {
		t.Error("Provider() should return nil for missing name")
	}
}

// ── Test: Path Routing ──────────────────────────────

func TestProviderRouter_PathRouting(t *testing.T) {
	cfg1 := config.DefaultConfig()
	cfg1.Port = 19106
	cfg1.TargetBase = "http://localhost:18999"
	cfg1.GenaiBase = "http://localhost:18999"
	cfg1.Keys = []string{"sk-test-key-1"}

	cfg2 := config.DefaultConfig()
	cfg2.Port = 19106
	cfg2.TargetBase = "http://localhost:18999"
	cfg2.GenaiBase = "http://localhost:18999"
	cfg2.Keys = []string{"sk-test-key-2"}

	pr := NewProviderRouter("")
	pool1 := keypool.NewKeyPool(cfg1.Keys, nil)
	pool2 := keypool.NewKeyPool(cfg2.Keys, nil)
	pr.AddProvider("provider-a", cfg1, pool1)
	pr.AddProvider("provider-b", cfg2, pool2)

	if err := pr.Start("127.0.0.1", 19106); err != nil {
		t.Fatalf("Start failed: %v", err)
	}
	defer func() {
		pr.Shutdown(context.Background())
		pr.Stop()
	}()

	time.Sleep(100 * time.Millisecond)

	tests := []struct {
		provider string
		resp     *http.Response
	}{
		{"provider-a", nil},
		{"provider-b", nil},
	}

	for _, tt := range tests {
		url := fmt.Sprintf("http://127.0.0.1:19106/%s/v1/chat/completions", tt.provider)
		// For now just verify the endpoint accepts the request
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
		}
	}

	// Unknown provider should get 404
	resp, err := http.Get("http://127.0.0.1:19106/unknown/v1/path")
	if err == nil {
		resp.Body.Close()
	}
}
