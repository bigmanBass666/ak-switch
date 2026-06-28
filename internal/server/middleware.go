package server

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strconv"

	"alvus/internal/config"
)

// adminAuth checks the X-Admin-Token header against the configured admin token.
// Returns true if authenticated, false and writes a 401 response if not.
func (s *ServerState) adminAuth(cfg *config.Config, w http.ResponseWriter, r *http.Request) bool {
	if cfg.AdminToken != "" && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Admin-Token")), []byte(cfg.AdminToken)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return false
	}
	return true
}

// parseKeyIndex extracts and validates a key index from the request path.
// Returns the 0-based index and true on success.
func (s *ServerState) parseKeyIndex(r *http.Request) (int, bool) {
	raw := r.PathValue("index")
	idx, err := strconv.Atoi(raw)
	if err != nil || idx < 1 {
		return 0, false
	}
	return idx - 1, true // convert to 0-based
}

// respondJSON writes a JSON response with the given status code and data.
func (s *ServerState) respondJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// filterEmpty removes empty strings from a slice.
func filterEmpty(ss []string) []string {
	filtered := make([]string, 0, len(ss))
	for _, s := range ss {
		if s != "" {
			filtered = append(filtered, s)
		}
	}
	return filtered
}
