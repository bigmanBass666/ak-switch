package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"alvus/internal/config"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(configCmd)
	configCmd.AddCommand(configInitCmd)
	configCmd.AddCommand(configViewCmd)

	configInitCmd.Flags().StringP("path", "p", "", "Output path for config.toml (default: XDG config directory)")
}

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Manage configuration",
	Long:  `View and initialize the alvus configuration file.`,
}

var configInitCmd = &cobra.Command{
	Use:   "init",
	Short: "Initialize default config.toml",
	Long: `Create a default configuration file at the XDG config directory
(or a custom path via --path).

If the file already exists, the command refuses to overwrite it.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		path, _ := cmd.Flags().GetString("path")
		if path == "" {
			var err error
			path, err = config.XDGConfigPath()
			if err != nil {
				return fmt.Errorf("failed to determine XDG config path: %w", err)
			}
		}

		// Refuse to overwrite existing file
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("config file already exists at %s (remove it first or use --path to specify a different location)", path)
		}

		// Create directory if needed
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create config directory %s: %w", dir, err)
		}

		// Write example config with placeholder providers
		tc := &config.TomlConfig{
			Port: 8080,
			Provider: map[string]config.TomlProviderConfig{
				"example-a": {
					Target:      "https://api.example-a.com/v1",
					Genai:       "https://api.example-a.com",
					CooldownSec: 60,
					MaxRetries:  3,
				},
				"example-b": {
					Target:      "https://api.example-b.com/v1",
					CooldownSec: 30,
					MaxRetries:  5,
				},
			},
		}
		if err := config.SaveTomlConfig(tc, path); err != nil {
			return fmt.Errorf("failed to write config file: %w", err)
		}

		fmt.Printf("Example configuration written to %s\n", path)
		fmt.Println("Edit the file to add your providers, then run:")
		fmt.Println("  alvus key add <provider> <api-key>  # to add API keys")
		fmt.Println("  alvus start                         # to start the proxy")

		return nil
	},
}

var configViewCmd = &cobra.Command{
	Use:   "view",
	Short: "Display current configuration",
	Long: `Read the TOML configuration file and print its contents in a human-readable format.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		source, err := config.XDGConfigPath()
		if err != nil {
			return fmt.Errorf("failed to determine XDG config path: %w", err)
		}

		// Check if the source file actually exists
		if _, statErr := os.Stat(source); statErr != nil {
			return fmt.Errorf("no configuration file found (looked at %s)", source)
		}

		// Load config from TOML
		cfg, err := config.LoadToml(source)
		if err != nil {
			return fmt.Errorf("failed to load configuration: %w", err)
		}

		sanitized := cfg.Sanitized()
		fmt.Printf("Configuration source: %s\n", source)
		fmt.Printf("Port: %d\n", sanitized.Port)
		fmt.Printf("Target base URL: %s\n", sanitized.TargetBase)
		fmt.Printf("GenAI base URL: %s\n", sanitized.GenaiBase)
		if sanitized.AdminToken != "" {
			fmt.Println("Admin token: (set)")
		}
		fmt.Printf("Disable thinking: %t\n", sanitized.DisableThinking)
		fmt.Printf("GenAI model: %s\n", sanitized.GenaiModel)
		fmt.Printf("Max retries: %d\n", sanitized.MaxRetries)
		fmt.Printf("Log level: %s\n", sanitized.LogLevel)
		fmt.Printf("Cooldown seconds: %d\n", sanitized.CooldownSec)
		fmt.Printf("Backoff cap seconds: %d\n", sanitized.BackoffCapSec)
		fmt.Printf("Backoff multiplier: %.1f\n", sanitized.BackoffMultiplier)
		fmt.Printf("Circuit breaker reset seconds: %d\n", sanitized.CBResetSec)
		fmt.Printf("Circuit breaker threshold: %d\n", sanitized.UpstreamCBThreshold)
		fmt.Printf("Health check interval seconds: %d\n", sanitized.HealthCheckIntervalSec)
		fmt.Printf("Health check path: %s\n", sanitized.HealthCheckPath)
		fmt.Printf("Health check timeout seconds: %d\n", sanitized.HealthCheckTimeoutSec)
		fmt.Printf("Keys file: %s\n", sanitized.KeysFile)
		for i, key := range sanitized.Keys {
			name := ""
			if i < len(sanitized.KeyNames) {
				name = sanitized.KeyNames[i]
			}
			if name != "" {
				fmt.Printf("Key[%d]: %s (name: %s)\n", i, key, name)
			} else {
				fmt.Printf("Key[%d]: %s\n", i, key)
			}
		}

		return nil
	},
}
