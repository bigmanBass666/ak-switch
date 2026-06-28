package server

import (
	"log/slog"
	"os"
	"time"
)

// WatchEnvFile monitors .env for changes and hot-reloads the configuration.
func WatchEnvFile(state *ServerState, stop <-chan struct{}) {
	var lastMod time.Time
	if info, err := os.Stat(".env"); err == nil {
		lastMod = info.ModTime()
	}

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			info, err := os.Stat(".env")
			if err != nil {
				if os.IsNotExist(err) {
					slog.Info("env deleted, keeping current config")
				}
				time.Sleep(10 * time.Millisecond)
				continue
			}
			if !info.ModTime().After(lastMod) {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			lastMod = info.ModTime()
			time.Sleep(100 * time.Millisecond) // debounce

			slog.Info("env changed, reloading")

			state.mu.RLock()
			oldCfg := state.cfg
			state.mu.RUnlock()

			newCfg, newPool, err := ReloadConfig()
			if err != nil {
				slog.Error("env reload failed; keeping previous config", "error", err)
				time.Sleep(10 * time.Millisecond)
				continue
			}

			// Log configuration changes (sensitive fields masked)
			changes := oldCfg.Diff(newCfg)
			if len(changes) > 0 {
				for _, c := range changes {
					slog.Info("config changed", "field", c.Field, "old", c.OldValue, "new", c.NewValue)
				}
			}

			state.mu.Lock()
			state.cfg = newCfg
			state.pool = newPool
			state.mu.Unlock()

			slog.Info("config reloaded", "keys", len(newPool.Keys()), "target", newCfg.TargetBase, "genai", newCfg.GenaiBase)
		}
	}
}

// RefreshKeyPoolMetrics periodically updates the keypool gauge metrics.
func RefreshKeyPoolMetrics(state *ServerState, stop <-chan struct{}) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			state.mu.RLock()
			pool := state.pool
			state.mu.RUnlock()
			state.metrics.RefreshKeyPoolGauge(pool)
		}
	}
}
