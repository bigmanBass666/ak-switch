package cmd

import (
	"fmt"
	"net/http"
	"time"

	"akswitch/internal/config"
)

// triggerReload sends a reload request to the running server.
// If the server is not running, it silently ignores the error.
func triggerReload() {
	port := adminPort
	if xdgPath, err := config.XDGConfigPath(); err == nil {
		if p := config.FindServerPort(xdgPath); p > 0 {
			port = p
		}
	}

	client := &http.Client{Timeout: 3 * time.Second}
	url := fmt.Sprintf("http://127.0.0.1:%d/api/reload", port)
	resp, err := client.Post(url, "application/json", nil)
	if err != nil {
		// Server not running, silently ignore
		return
	}
	resp.Body.Close()
}