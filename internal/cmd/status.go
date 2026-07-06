package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(statusCmd)
}

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show runtime status",
	Long:  `Query the running akswitch server and display health, key counts, and request statistics.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		client := &http.Client{Timeout: 3 * time.Second}

		// Determine the server port from config or default
		port := detectServerPort()

		// Query health endpoint on the server port
		healthURL := fmt.Sprintf("http://127.0.0.1:%d/health", port)
		resp, err := client.Get(healthURL)
		if err != nil {
			return fmt.Errorf("server not reachable at %s: %w", healthURL, err)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		var healthData map[string]interface{}
		if err := json.Unmarshal(body, &healthData); err != nil {
			// Check if response is non-JSON (e.g., HTML from another service)
			if len(body) > 0 && body[0] != '{' && body[0] != '[' {
				return fmt.Errorf("server not running or returned unexpected response (HTTP %d)", resp.StatusCode)
			}
			return fmt.Errorf("failed to parse health response: %w", err)
		}

		fmt.Printf("Server: http://127.0.0.1:%d\n", port)
		fmt.Printf("Status: %s\n", healthData["status"])

		if providers, ok := healthData["providers"]; ok {
			fmt.Printf("Providers: %v\n", providers)
		}

		if details, ok := healthData["details"]; ok {
			if det, ok2 := details.(map[string]interface{}); ok2 {
				fmt.Print(formatProviderTable(det))
			}
		}

		// Query stats endpoint
		statsURL := fmt.Sprintf("http://127.0.0.1:%d/api/stats", port)
		statsResp, err := client.Get(statsURL)
		if err == nil {
			statsBody, _ := io.ReadAll(statsResp.Body)
			statsResp.Body.Close()
			var stats map[string]interface{}
			if err := json.Unmarshal(statsBody, &stats); err == nil {
				fmt.Printf("Requests: %v (success: %v, failed: %v)\n",
					stats["total_requests"], stats["successful_requests"], stats["failed_requests"])
				fmt.Printf("Active keys: %v, Cooling: %v, Disabled: %v\n",
					stats["active_keys"], stats["cooling_keys"], stats["disabled_keys"])
				fmt.Printf("Uptime: %vs\n", stats["uptime_seconds"])
			}
		}

		return nil
	},
}

// formatProviderTable formats the provider details map as a tab-aligned table.
// Exported for testing.
func formatProviderTable(det map[string]interface{}) string {
	var buf bytes.Buffer
	w := tabwriter.NewWriter(&buf, 0, 0, 3, ' ', 0)
	fmt.Fprintln(w, "PROVIDER\tKEYS\tCB_STATE")
	for name, info := range det {
		if inf, ok3 := info.(map[string]interface{}); ok3 {
			fmt.Fprintf(w, "%s\t%v\t%v\n",
				name, inf["keys"], inf["upstream_cb_state"])
		}
	}
	w.Flush()
	return buf.String()
}