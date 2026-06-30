// Package server provides the HTTP server, proxy, and management handlers for Alvus.
package server

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"sync"

	"alvus/internal/config"
	"alvus/internal/keypool"
)

// ManagedInstance holds all state for a single proxy instance managed by InstanceManager.
type ManagedInstance struct {
	Name     string
	Config   *config.Config
	Pool     *keypool.KeyPool
	State    *ServerState
	Listener net.Listener
	Server   *http.Server
	stop     chan struct{}
	started  bool
}

// InstanceManager manages multiple proxy instances in a single process.
// Each instance runs on its own port with its own config, key pool, and server state.
type InstanceManager struct {
	instances     map[string]*ManagedInstance
	mu            sync.RWMutex
	stop          chan struct{}
	wg            sync.WaitGroup
	dashboardHTML string
}

// NewInstanceManager creates a new InstanceManager.
func NewInstanceManager(dashboardHTML string) *InstanceManager {
	return &InstanceManager{
		instances:     make(map[string]*ManagedInstance),
		stop:          make(chan struct{}),
		dashboardHTML: dashboardHTML,
	}
}

// AddInstance creates a new ManagedInstance with the given name, config, and key pool,
// then registers it with the manager. The returned pointer is valid for the lifetime
// of the manager.
func (im *InstanceManager) AddInstance(name string, cfg *config.Config, pool *keypool.KeyPool) *ManagedInstance {
	state := NewServerState(cfg, pool, im.dashboardHTML, cfg.KeysFile)
	inst := &ManagedInstance{
		Name:   name,
		Config: cfg,
		Pool:   pool,
		State:  state,
		stop:   make(chan struct{}),
	}
	im.mu.Lock()
	im.instances[name] = inst
	im.mu.Unlock()
	return inst
}

// StartAll binds ports and starts HTTP servers for all registered instances.
// Each instance is bound to <host>:<port>.
// Returns the first port binding error encountered; already-started instances
// are skipped and continue running unaffected.
func (im *InstanceManager) StartAll(host string) error {
	im.mu.RLock()
	defer im.mu.RUnlock()

	var firstErr error
	for _, inst := range im.instances {
		if inst.started {
			continue
		}
		addr := fmt.Sprintf("%s:%d", host, inst.Config.Port)
		listener, err := net.Listen("tcp", addr)
		if err != nil {
			slog.Error("instance port bind failed",
				"name", inst.Name, "port", inst.Config.Port, "error", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		inst.Listener = listener
		inst.Server = &http.Server{Handler: inst.State.Handler()}
		inst.started = true

		im.wg.Add(1)
		go func(inst *ManagedInstance) {
			defer im.wg.Done()
			slog.Info("instance started",
				"name", inst.Name,
				"addr", listener.Addr().String(),
				"keys", len(inst.Pool.Keys()))
			if err := inst.Server.Serve(listener); err != http.ErrServerClosed {
				slog.Error("instance serve error", "name", inst.Name, "error", err)
			}
		}(inst)
	}
	return firstErr
}

// StartBackgroundTasks launches background goroutines (env watcher, metrics refresh,
// active health check) for each registered instance.
func (im *InstanceManager) StartBackgroundTasks() {
	im.mu.RLock()
	defer im.mu.RUnlock()

	for _, inst := range im.instances {
		// WatchEnvFile is safe to run even without a .env file.
		im.wg.Add(1)
		go func(inst *ManagedInstance) {
			defer im.wg.Done()
			WatchEnvFile(inst.State, inst.stop)
		}(inst)

		im.wg.Add(1)
		go func(inst *ManagedInstance) {
			defer im.wg.Done()
			RefreshKeyPoolMetrics(inst.State, inst.stop)
		}(inst)

		im.wg.Add(1)
		go func(inst *ManagedInstance) {
			defer im.wg.Done()
			ActiveHealthCheck(inst.State, inst.stop)
		}(inst)
	}
}

// Shutdown gracefully shuts down all instances' HTTP servers in parallel.
// The caller should call Stop() after Shutdown returns to wait for all
// background goroutines to finish.
func (im *InstanceManager) Shutdown(ctx context.Context) {
	im.mu.RLock()
	defer im.mu.RUnlock()

	var wg sync.WaitGroup
	for _, inst := range im.instances {
		if inst.Server == nil {
			continue
		}
		wg.Add(1)
		go func(inst *ManagedInstance) {
			defer wg.Done()
			if err := inst.Server.Shutdown(ctx); err != nil {
				slog.Error("instance shutdown error", "name", inst.Name, "error", err)
			} else {
				slog.Info("instance shut down", "name", inst.Name)
			}
		}(inst)
	}
	wg.Wait()
}

// Stop signals all background tasks to stop and waits for all goroutines
// (both server and background tasks) to finish.
func (im *InstanceManager) Stop() {
	im.mu.RLock()
	for _, inst := range im.instances {
		close(inst.stop)
	}
	im.mu.RUnlock()
	close(im.stop)
	im.wg.Wait()
}

// InstanceNames returns the names of all registered instances.
func (im *InstanceManager) InstanceNames() []string {
	im.mu.RLock()
	defer im.mu.RUnlock()
	names := make([]string, 0, len(im.instances))
	for name := range im.instances {
		names = append(names, name)
	}
	return names
}

// Instance returns the ManagedInstance with the given name, or nil if not found.
func (im *InstanceManager) Instance(name string) *ManagedInstance {
	im.mu.RLock()
	defer im.mu.RUnlock()
	return im.instances[name]
}
