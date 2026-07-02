package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"alvus/internal/config"
	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(logsCmd)
}

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Show request logs",
	Long:  `Display recent request logs from the running alvus server.`,
	Args:  cobra.MaximumNArgs(0),
	RunE: func(cmd *cobra.Command, args []string) error {
		client := &http.Client{Timeout: 5 * time.Second}

		// Determine the server port from config or default
		port := adminPort
		if xdgPath, err := config.XDGConfigPath(); err == nil {
			if providers, err := config.LoadAllTomlProviders(xdgPath); err == nil {
				for _, cfg := range providers {
					if cfg.Port > 0 {
						port = cfg.Port
						break
					}
				}
			}
		}

		logURL := fmt.Sprintf("http://127.0.0.1:%d/logs", port)
		resp, err := client.Get(logURL)
		if err != nil {
			return fmt.Errorf("server not reachable at %s: %w", logURL, err)
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)

		var entries []interface{}
		if err := json.Unmarshal(body, &entries); err != nil {
			return fmt.Errorf("failed to parse logs: %w", err)
		}

		if len(entries) == 0 {
			fmt.Println("No log entries")
			return nil
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

			provider := getStrField(entryMap, "provider", "")
			duration := getStrField(entryMap, "duration_ms", "")
			attempt := getStrField(entryMap, "attempt", "")
			keyName := getStrField(entryMap, "key_name", "")

			var extras []string
			if attempt != "" {
				extras = append(extras, "attempt "+attempt)
			}
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
