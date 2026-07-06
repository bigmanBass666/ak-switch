package server

import (
	"fmt"
	"net/http"
)

// ConfigPayload is the JSON structure for config API responses.
type ConfigPayload struct {
	TargetBase string   `json:"targetBase"`
	GenaiBase  string   `json:"genaiBase"`
	Keys       []string `json:"keys"`
}

// lookupProvider returns the ProviderState for a given provider name.
func (pr *ProviderRouter) lookupProvider(name string) *ProviderState {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	return pr.providers[name]
}

// firstProvider returns the first (alphabetically) provider, or nil if none exist.
func (pr *ProviderRouter) firstProvider() *ProviderState {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	for _, ps := range pr.providers {
		return ps
	}
	return nil
}

// resolveProvider gets the provider specified by the "provider" query parameter.
// If not set, returns the first provider. Returns an error string if no provider found.
func (pr *ProviderRouter) resolveProvider(r *http.Request) (*ProviderState, string) {
	pName := r.URL.Query().Get("provider")
	if pName == "" {
		ps := pr.firstProvider()
		if ps == nil {
			return nil, "no providers configured"
		}
		return ps, ""
	}
	ps := pr.lookupProvider(pName)
	if ps == nil {
		return nil, fmt.Sprintf("provider %q not found", pName)
	}
	return ps, ""
}

// checkAdminToken validates the X-Admin-Token header against a specific provider's admin token.
func (pr *ProviderRouter) checkAdminToken(w http.ResponseWriter, r *http.Request, providerName string) bool {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	ps, ok := pr.providers[providerName]
	if !ok {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	token := r.Header.Get("X-Admin-Token")
	if ps.Config.AdminToken == "" {
		// No admin token configured for this provider — access allowed
		return true
	}
	if ps.Config.AdminToken == token {
		return true
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	return false
}

// checkAnyAdminToken validates the X-Admin-Token header against any configured admin token.
// If at least one provider has an AdminToken configured, a valid token must be provided.
// If no providers have AdminToken configured, access is allowed without token.
func (pr *ProviderRouter) checkAnyAdminToken(w http.ResponseWriter, r *http.Request) bool {
	pr.mu.RLock()
	defer pr.mu.RUnlock()
	token := r.Header.Get("X-Admin-Token")
	hasAnyToken := false
	for _, ps := range pr.providers {
		if ps.Config.AdminToken != "" {
			hasAnyToken = true
			if ps.Config.AdminToken == token {
				return true
			}
		}
	}
	if !hasAnyToken {
		return true
	}
	http.Error(w, "unauthorized", http.StatusUnauthorized)
	return false
}