package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func init() {
	logsCmd.Flags().IntVar(&logsLast, "last", 0, "Show only the last N entries (0 = all)")
	rootCmd.AddCommand(logsCmd)
}

var logsLast int

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Show request logs",
	Long:  `Display recent request logs from the running akswitch server.`,
	Args:  cobra.MaximumNArgs(0),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := &http.Client{Timeout: 5 * time.Second}

		// Determine the server port from config or default
		port := detectServerPort()

		logURL := fmt.Sprintf("http://127.0.0.1:%d/logs", port)
		resp, err := client.Get(logURL)
		if err != nil {
			return fmt.Errorf("server not reachable at %s: %w", logURL, err)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)

		var entries []interface{}
		if err := json.Unmarshal(body, &entries); err != nil {
			// Check if response is non-JSON (e.g., HTML from another service)
			if len(body) > 0 && body[0] != '{' && body[0] != '[' {
				return fmt.Errorf("server not running or returned unexpected response (HTTP %d)", resp.StatusCode)
			}
			return fmt.Errorf("failed to parse logs: %w", err)
		}

		if len(entries) == 0 {
			fmt.Println("No log entries")
			return nil
		}

		if logsLast > 0 && len(entries) > logsLast {
			entries = entries[len(entries)-logsLast:]
		}

		for _, entry := range entries {
			entryMap, ok := entry.(map[string]interface{})
			if !ok {
				continue
			}
			method := getStrField(entryMap, "method", "?")
			path := getStrField(entryMap, "url", "?")
			status := getStrField(entryMap, "status", "?")
			ts := getStrField(entryMap, "timestamp", "?")
			if t, err := time.Parse(time.RFC3339, ts); err == nil {
				ts = t.Format("15:04:05.000")
			}

			provider := getStrField(entryMap, "provider", "")
			duration := getStrField(entryMap, "duration_ms", "")
			retry := getStrField(entryMap, "retry", "")
			keyName := getStrField(entryMap, "key_name", "")

			var extras []string
			if retry != "" && retry != "0" { extras = append(extras, "retry "+retry) }
			if duration != "" {
				extras = append(extras, duration+"ms")
			}
			if keyName != "" {
				extras = append(extras, "key: "+keyName)
			}

			prefix := fmt.Sprintf("  [%s]", ts)
			if provider != "" {
				prefix += " " + provider
			}

			extraStr := ""
			if len(extras) > 0 {
				extraStr = " (" + strings.Join(extras, ", ") + ")"
			}

			fmt.Printf("%s %s %s -> %s%s\n", prefix, method, path, status, extraStr)
		}

		return nil
	},
}

func getStrField(m map[string]interface{}, key, fallback string) string {
	if v, ok := m[key]; ok {
		switch s := v.(type) {
		case string:
			return s
		case float64:
			return fmt.Sprintf("%.0f", s)
		}
	}
	return fallback
}
