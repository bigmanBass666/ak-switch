package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"testing"
	"time"

	"alvus/internal/config"
	"alvus/internal/keypool"
)

// ── Test: Multi-Provider Start & Health Check ──────────────

func TestInstanceManager_MultiProvider(t *testing.T) {
	// Create two provider configs with distinct ports
	cfg1 := config.DefaultConfig()
	cfg1.Port = 19101
	cfg1.TargetBase = "http://localhost:18999"
	cfg1.GenaiBase = "http://localhost:18999"
	cfg1.Keys = []string{"sk-test-key-provider-1"}

	cfg2 := config.DefaultConfig()
	cfg2.Port = 19102
	cfg2.TargetBase = "http://localhost:18999"
	cfg2.GenaiBase = "http://localhost:18999"
	cfg2.Keys = []string{"sk-test-key-provider-2"}

	// Create InstanceManager
	mgr := NewInstanceManager("")
	pool1 := keypool.NewKeyPool(cfg1.Keys, nil)
	pool2 := keypool.NewKeyPool(cfg2.Keys, nil)
	mgr.AddInstance("alpha", cfg1, pool1)
	mgr.AddInstance("beta", cfg2, pool2)

	// Verify instance names
	names := mgr.InstanceNames()
	if len(names) != 2 {
		t.Fatalf("expected 2 instances, got %d: %v", len(names), names)
	}

	// Start all instances
	if err := mgr.StartAll("127.0.0.1"); err != nil {
		t.Fatalf("StartAll failed: %v", err)
	}
	defer func() {
		mgr.Shutdown(context.Background())
		mgr.Stop()
	}()

	// Allow servers a moment to start
	time.Sleep(100 * time.Millisecond)

	// Verify both health endpoints respond
	for _, port := range []int{19101, 19102} {
		addr := fmt.Sprintf("http://127.0.0.1:%d/health", port)
		resp, err := http.Get(addr)
		if err != nil {
			t.Errorf("GET %s failed: %v", addr, err)
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s returned status %d, want %d", addr, resp.StatusCode, http.StatusOK)
		}
		body, _ := io.ReadAll(resp.Body)
		if len(body) == 0 {
			t.Errorf("GET %s returned empty body", addr)
		}
	}

	// Verify port conflict returns error
	cfg3 := config.DefaultConfig()
	cfg3.Port = 19101 // same port as alpha
	cfg3.TargetBase = "http://localhost:18999"
	cfg3.GenaiBase = "http://localhost:18999"
	cfg3.Keys = []string{"sk-test-key-3"}
	pool3 := keypool.NewKeyPool(cfg3.Keys, nil)
	mgr.AddInstance("conflict", cfg3, pool3)

	err := mgr.StartAll("127.0.0.1")
	if err == nil {
		t.Error("expected port conflict error, got nil")
	}
}

func TestInstanceManager_GracefulShutdown(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Port = 19103
	cfg.TargetBase = "http://localhost:18999"
	cfg.GenaiBase = "http://localhost:18999"
	cfg.Keys = []string{"sk-test-shutdown"}

	mgr := NewInstanceManager("")
	pool := keypool.NewKeyPool(cfg.Keys, nil)
	mgr.AddInstance("shutdown-test", cfg, pool)

	if err := mgr.StartAll("127.0.0.1"); err != nil {
		t.Fatalf("StartAll failed: %v", err)
	}

	// Verify server is up
	time.Sleep(100 * time.Millisecond)
	resp, err := http.Get("http://127.0.0.1:19103/health")
	if err != nil {
		t.Fatalf("server not reachable before shutdown: %v", err)
	}
	resp.Body.Close()

	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	mgr.Shutdown(ctx)
	mgr.Stop()

	// Verify server is down
	_, err = http.Get("http://127.0.0.1:19103/health")
	if err == nil {
		t.Error("server still reachable after shutdown")
	}
}

func TestInstanceManager_Empty(t *testing.T) {
	mgr := NewInstanceManager("")
	names := mgr.InstanceNames()
	if len(names) != 0 {
		t.Errorf("expected 0 instances, got %d", len(names))
	}

	// StartAll on empty manager should succeed
	if err := mgr.StartAll("127.0.0.1"); err != nil {
		t.Errorf("StartAll on empty manager should succeed, got: %v", err)
	}
}

func TestInstanceManager_InstanceAccessors(t *testing.T) {
	mgr := NewInstanceManager("")
	cfg := config.DefaultConfig()
	cfg.Port = 19104
	cfg.Keys = []string{"sk-test-accessor"}
	pool := keypool.NewKeyPool(cfg.Keys, nil)

	inst := mgr.AddInstance("accessor-test", cfg, pool)
	if inst == nil {
		t.Fatal("AddInstance returned nil")
	}
	if inst.Name != "accessor-test" {
		t.Errorf("inst.Name = %q, want %q", inst.Name, "accessor-test")
	}

	// Instance() lookup
	found := mgr.Instance("accessor-test")
	if found == nil {
		t.Fatal("Instance() returned nil for existing instance")
	}
	if found != inst {
		t.Error("Instance() returned different pointer")
	}

	// Instance() not found
	missing := mgr.Instance("nonexistent")
	if missing != nil {
		t.Error("Instance() should return nil for missing name")
	}
}
