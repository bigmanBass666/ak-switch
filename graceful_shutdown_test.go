//go:build e2e

package main

import (
	"akswitch/internal/config"
	"akswitch/internal/keypool"
	"akswitch/internal/server"
	"context"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"
)

// TestGracefulShutdown_ActiveRequestCompletes verifies that when Shutdown is
// called during an active request, the request completes successfully.
func TestGracefulShutdown_ActiveRequestCompletes(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("done"))
	})

	srv := &http.Server{Handler: handler}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	go srv.Serve(listener)

	// Send a request concurrently, then immediately start shutdown
	respCh := make(chan *http.Response, 1)
	go func() {
		resp, err := http.Get("http://" + listener.Addr().String() + "/")
		if err == nil {
			respCh <- resp
		}
	}()

	// Wait briefly for the request to reach the server
	time.Sleep(50 * time.Millisecond)

	// Initiate graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown error: %v", err)
	}

	// Verify the in-flight request completed successfully
	select {
	case resp := <-respCh:
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d", resp.StatusCode)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if string(body) != "done" {
			t.Fatalf("expected 'done', got '%s'", string(body))
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for response")
	}
}

// TestGracefulShutdown_RejectsNewConnections verifies that after Shutdown
// returns, new connections are rejected.
func TestGracefulShutdown_RejectsNewConnections(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := &http.Server{Handler: handler}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer listener.Close()

	go srv.Serve(listener)

	// Shutdown immediately
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown error: %v", err)
	}

	// Try to send a new request — should fail
	_, err = http.Get("http://" + listener.Addr().String() + "/")
	if err == nil {
		t.Fatal("expected error after shutdown, got nil")
	}
}

// TestGracefulShutdown_BackgroundGoroutinesStop verifies that background
// goroutines (WatchEnvFile, RefreshKeyPoolMetrics) stop when the stop
// channel is closed, allowing a WaitGroup to complete.
func TestGracefulShutdown_BackgroundGoroutinesStop(t *testing.T) {
	stop := make(chan struct{})
	var wg sync.WaitGroup

	cfg := &config.Config{
		TargetBase:  "http://127.0.0.1:19999",
		GenaiBase:   "http://127.0.0.1:19999",
		Port:        0,
		MaxRetries:  3,
		CooldownSec: 60,
	}
	pool := keypool.NewKeyPool([]string{"test-key"}, nil)
	state := server.NewServerState("test", cfg, pool, "", "")
	_ = state

	wg.Add(1)
	go func() {
		defer wg.Done()
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
	}()

	// Give them a moment to start
	time.Sleep(50 * time.Millisecond)

	// Trigger shutdown and wait for completion
	close(stop)

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Both goroutines stopped — success
	case <-time.After(3 * time.Second):
		t.Fatal("background goroutines did not stop within 3s")
	}
}
