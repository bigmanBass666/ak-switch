//go:build unit

package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"akswitch/internal/config"
	"akswitch/internal/keypool"
)

// ── checkAdminToken ─────────────────────────────────

func TestCheckAdminToken_ProviderNotFound(t *testing.T) {
	pr := NewProviderRouter("")
	cfg := config.DefaultConfig()
	cfg.Keys = []string{"sk-test-key"}
	pool := keypool.NewKeyPool(cfg.Keys, nil)
	pr.AddProvider("test-provider", cfg, pool)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	got := pr.checkAdminToken(w, r, "nonexistent")
	if got {
		t.Errorf("checkAdminToken for nonexistent provider: got true, want false")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestCheckAdminToken_HasToken_NoHeader(t *testing.T) {
	pr := NewProviderRouter("")
	cfg := config.DefaultConfig()
	cfg.AdminToken = "secret-token"
	cfg.Keys = []string{"sk-test-key"}
	pool := keypool.NewKeyPool(cfg.Keys, nil)
	pr.AddProvider("test-provider", cfg, pool)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	got := pr.checkAdminToken(w, r, "test-provider")
	if got {
		t.Errorf("expected false (no token provided), got true")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestCheckAdminToken_HasToken_CorrectHeader(t *testing.T) {
	pr := NewProviderRouter("")
	cfg := config.DefaultConfig()
	cfg.AdminToken = "secret-token"
	cfg.Keys = []string{"sk-test-key"}
	pool := keypool.NewKeyPool(cfg.Keys, nil)
	pr.AddProvider("test-provider", cfg, pool)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Admin-Token", "secret-token")

	got := pr.checkAdminToken(w, r, "test-provider")
	if !got {
		t.Errorf("expected true (correct token), got false")
	}
}

func TestCheckAdminToken_HasToken_WrongHeader(t *testing.T) {
	pr := NewProviderRouter("")
	cfg := config.DefaultConfig()
	cfg.AdminToken = "secret-token"
	cfg.Keys = []string{"sk-test-key"}
	pool := keypool.NewKeyPool(cfg.Keys, nil)
	pr.AddProvider("test-provider", cfg, pool)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Admin-Token", "wrong-token")

	got := pr.checkAdminToken(w, r, "test-provider")
	if got {
		t.Errorf("expected false (wrong token), got true")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestCheckAdminToken_NoTokenConfigured_NoHeader(t *testing.T) {
	pr := NewProviderRouter("")
	cfg := config.DefaultConfig()
	cfg.Keys = []string{"sk-test-key"}
	pool := keypool.NewKeyPool(cfg.Keys, nil)
	pr.AddProvider("test-provider", cfg, pool)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	got := pr.checkAdminToken(w, r, "test-provider")
	if !got {
		t.Errorf("expected true (no token configured), got false")
	}
}

func TestCheckAdminToken_NoTokenConfigured_WithHeader(t *testing.T) {
	pr := NewProviderRouter("")
	cfg := config.DefaultConfig()
	cfg.Keys = []string{"sk-test-key"}
	pool := keypool.NewKeyPool(cfg.Keys, nil)
	pr.AddProvider("test-provider", cfg, pool)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Admin-Token", "some-random-token")

	got := pr.checkAdminToken(w, r, "test-provider")
	if !got {
		t.Errorf("expected true (no token configured, header ignored), got false")
	}
}

func TestCheckAdminToken_MixedProviders_AWithToken_NoHeader(t *testing.T) {
	// Provider A has AdminToken, Provider B has no AdminToken.
	// Accessing A without a token must reject — B's lack of token must not bypass A's auth.
	pr := NewProviderRouter("")

	cfgA := config.DefaultConfig()
	cfgA.AdminToken = "token-a"
	cfgA.Keys = []string{"sk-key-a"}
	poolA := keypool.NewKeyPool(cfgA.Keys, nil)
	pr.AddProvider("provider-a", cfgA, poolA)

	cfgB := config.DefaultConfig()
	cfgB.Keys = []string{"sk-key-b"}
	poolB := keypool.NewKeyPool(cfgB.Keys, nil)
	pr.AddProvider("provider-b", cfgB, poolB)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	got := pr.checkAdminToken(w, r, "provider-a")
	if got {
		t.Errorf("expected false (A requires token but none provided), got true")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

// ── checkAnyAdminToken ──────────────────────────────

func TestCheckAnyAdminToken_HasToken_NoHeader(t *testing.T) {
	pr := NewProviderRouter("")
	cfg := config.DefaultConfig()
	cfg.AdminToken = "secret-token"
	cfg.Keys = []string{"sk-test-key"}
	pool := keypool.NewKeyPool(cfg.Keys, nil)
	pr.AddProvider("test-provider", cfg, pool)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	got := pr.checkAnyAdminToken(w, r)
	if got {
		t.Errorf("expected false (no token provided), got true")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestCheckAnyAdminToken_HasToken_CorrectHeader(t *testing.T) {
	pr := NewProviderRouter("")
	cfg := config.DefaultConfig()
	cfg.AdminToken = "secret-token"
	cfg.Keys = []string{"sk-test-key"}
	pool := keypool.NewKeyPool(cfg.Keys, nil)
	pr.AddProvider("test-provider", cfg, pool)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Admin-Token", "secret-token")

	got := pr.checkAnyAdminToken(w, r)
	if !got {
		t.Errorf("expected true (correct token), got false")
	}
}

func TestCheckAnyAdminToken_HasToken_WrongHeader(t *testing.T) {
	pr := NewProviderRouter("")
	cfg := config.DefaultConfig()
	cfg.AdminToken = "secret-token"
	cfg.Keys = []string{"sk-test-key"}
	pool := keypool.NewKeyPool(cfg.Keys, nil)
	pr.AddProvider("test-provider", cfg, pool)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Admin-Token", "wrong-token")

	got := pr.checkAnyAdminToken(w, r)
	if got {
		t.Errorf("expected false (wrong token), got true")
	}
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status code = %d, want %d", w.Code, http.StatusUnauthorized)
	}
}

func TestCheckAnyAdminToken_AllNoToken(t *testing.T) {
	pr := NewProviderRouter("")
	cfg := config.DefaultConfig()
	cfg.Keys = []string{"sk-test-key"}
	pool := keypool.NewKeyPool(cfg.Keys, nil)
	pr.AddProvider("test-provider", cfg, pool)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	got := pr.checkAnyAdminToken(w, r)
	if !got {
		t.Errorf("expected true (no tokens anywhere), got false")
	}
}

func TestCheckAnyAdminToken_EmptyRouter(t *testing.T) {
	pr := NewProviderRouter("")

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)

	got := pr.checkAnyAdminToken(w, r)
	if !got {
		t.Errorf("expected true (empty router, no auth needed), got false")
	}
}

func TestCheckAnyAdminToken_AnyTokenMatches(t *testing.T) {
	// Provider A: no AdminToken; Provider B: AdminToken = "token-b".
	// Request with "token-b" must match provider-B's token.
	pr := NewProviderRouter("")

	cfgA := config.DefaultConfig()
	cfgA.Keys = []string{"sk-key-a"}
	poolA := keypool.NewKeyPool(cfgA.Keys, nil)
	pr.AddProvider("provider-a", cfgA, poolA)

	cfgB := config.DefaultConfig()
	cfgB.AdminToken = "token-b"
	cfgB.Keys = []string{"sk-key-b"}
	poolB := keypool.NewKeyPool(cfgB.Keys, nil)
	pr.AddProvider("provider-b", cfgB, poolB)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Admin-Token", "token-b")

	got := pr.checkAnyAdminToken(w, r)
	if !got {
		t.Errorf("expected true (token-b matches provider-b), got false")
	}
}