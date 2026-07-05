package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"akswitch/internal/config"

	"github.com/spf13/cobra"
)

func init() {
	rootCmd.AddCommand(providerCmd)
	providerCmd.AddCommand(providerAddCmd)
	providerCmd.AddCommand(providerListCmd)
	providerCmd.AddCommand(providerRemoveCmd)
	providerCmd.AddCommand(providerDefaultCmd)

	providerAddCmd.Flags().StringP("target", "t", "", "Upstream target URL (required)")
	providerAddCmd.Flags().IntP("port", "p", 0, "HTTP listen port (required for first provider)")
	providerAddCmd.Flags().StringP("genai", "g", "", "GenAI base URL (optional)")
	providerAddCmd.Flags().IntP("cooldown-sec", "c", 60, "Cooldown seconds after rate-limit")
	providerAddCmd.Flags().IntP("max-retries", "r", 3, "Max retry attempts for upstream")
	providerAddCmd.Flags().Bool("default", false, "Set this provider as the default")
}

var providerCmd = &cobra.Command{
	Use:   "provider",
	Short: "Manage providers",
	Long:  `Add, list, and remove provider configurations in config.toml.`,
}

var providerAddCmd = &cobra.Command{
	Use:   "add <name>",
	Short: "Add a new provider",
	Long: `Add a new provider to the TOML configuration.

The --target flag is required. --port is required for the first provider;
subsequent providers reuse the existing port and --port can be omitted.

Example:
  akswitch provider add nvidia --target https://integrate.api.nvidia.com/v1 --port 3002
  akswitch provider add sensenova --target https://api.sensenova.com/v1`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		target, _ := cmd.Flags().GetString("target")
		port, _ := cmd.Flags().GetInt("port")
		genai, _ := cmd.Flags().GetString("genai")
		cooldown, _ := cmd.Flags().GetInt("cooldown-sec")
		maxRetries, _ := cmd.Flags().GetInt("max-retries")

		if target == "" {
			return fmt.Errorf("--target/-t is required")
		}

		source, err := config.XDGConfigPath()
		if err != nil {
			return fmt.Errorf("cannot determine XDG config path: %w", err)
		}

		// Load existing config or create a fresh one
		tc, err := config.LoadTomlConfig(source)
		if err != nil {
			if !os.IsNotExist(err) {
				return fmt.Errorf("failed to load config: %w", err)
			}
			tc = &config.TomlConfig{
				Provider: make(map[string]config.TomlProviderConfig),
			}
		}

		// Check for duplicate
		if _, exists := tc.Provider[name]; exists {
			return fmt.Errorf("provider %q already exists in %s", name, source)
		}

		// Port: first provider must set it; subsequent providers reuse the existing one
		if port == 0 {
			if tc.Port == 0 {
				return fmt.Errorf("--port/-p is required for the first provider")
			}
			port = tc.Port // reuse existing port
		} else if tc.Port == 0 {
			// First provider with a port — set it
			tc.Port = port
		}
		// If both port > 0 and tc.Port > 0, user explicitly passed --port;
		// we don't override tc.Port (first provider's port wins).

		// Add new provider
		tc.Provider[name] = config.TomlProviderConfig{
			Target:      target,
			Genai:       genai,
			CooldownSec: cooldown,
			MaxRetries:  maxRetries,
		}

		// Ensure directory exists
		dir := filepath.Dir(source)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create config directory %s: %w", dir, err)
		}

		// Check os.Args for --default to avoid cobra flag persistence across test runs
		hasDefaultFlag := false
		for _, a := range os.Args {
			if a == "--default" {
				hasDefaultFlag = true
				break
			}
		}
		if hasDefaultFlag {
			tc.DefaultProvider = name
			config.DefaultProviderName = name
		}

		// Save
		if err := config.SaveTomlConfig(tc, source); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		if hasDefaultFlag {
			fmt.Printf("Provider %q added to %s (default)\n", name, source)
		} else {
			fmt.Printf("Provider %q added to %s\n", name, source)
		}
		return nil
	},
}

var providerListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all providers",
	Long: `Display all configured providers and their settings from config.toml.

Example output:
  Providers (from /home/user/.config/akswitch/config.toml):
    NAME        TARGET                                            PORT
    nvidia      https://integrate.api.nvidia.com/v1               3002
    sensenova   https://api.sensenova.com/v1                      3001`,
	RunE: func(cmd *cobra.Command, args []string) error {
		source, err := config.XDGConfigPath()
		if err != nil {
			return fmt.Errorf("failed to determine XDG config path: %w", err)
		}
		if _, statErr := os.Stat(source); statErr != nil {
			return fmt.Errorf("no configuration file found at %s", source)
		}

		tc, err := config.LoadTomlConfig(source)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		if len(tc.Provider) == 0 {
			fmt.Printf("No providers configured in %s\n", source)
			return nil
		}

		// Sort names for deterministic output
		names := make([]string, 0, len(tc.Provider))
		for n := range tc.Provider {
			names = append(names, n)
		}
		sort.Strings(names)

		fmt.Printf("Providers (from %s):\n", source)
		fmt.Printf("  %-12s %-50s %s\n", "NAME", "TARGET", "PORT")
		for _, n := range names {
			p := tc.Provider[n]
			defaultMark := ""
			if n == tc.DefaultProvider {
				defaultMark = "  (default)"
			}
			fmt.Printf("  %-12s %-50s %d%s\n", n, p.Target, tc.Port, defaultMark)
		}

		return nil
	},
}

var providerRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove a provider",
	Long: `Remove a provider from the TOML configuration.

This only removes the provider configuration; any associated keys file
is NOT deleted. Use 'akswitch key remove' to manage individual keys.

Example:
  akswitch provider remove nvidia`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		source, err := config.XDGConfigPath()
		if err != nil {
			return fmt.Errorf("failed to determine XDG config path: %w", err)
		}
		if _, statErr := os.Stat(source); statErr != nil {
			return fmt.Errorf("no configuration file found at %s", source)
		}

		tc, err := config.LoadTomlConfig(source)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		if _, exists := tc.Provider[name]; !exists {
			return fmt.Errorf("provider %q not found in %s", name, source)
		}

		delete(tc.Provider, name)

		// If the removed provider was the default, clear it
		if tc.DefaultProvider == name {
			tc.DefaultProvider = ""
			config.DefaultProviderName = ""
			fmt.Printf("Default provider cleared (was %q)\n", name)
		}

		if err := config.SaveTomlConfig(tc, source); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Printf("Provider %q removed from %s\n", name, source)
		fmt.Println("Note: the keys file for this provider was not removed (if any)")
		return nil
	},
}

var providerDefaultCmd = &cobra.Command{
	Use:   "default <name>",
	Short: "Set the default provider",
	Long: `Set a provider as the default, which ` + "`" + `akswitch start` + "`" + ` will start
when no --provider or --all flag is given.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]

		source, err := config.XDGConfigPath()
		if err != nil {
			return fmt.Errorf("failed to determine XDG config path: %w", err)
		}
		if _, statErr := os.Stat(source); statErr != nil {
			return fmt.Errorf("no configuration file found at %s", source)
		}

		tc, err := config.LoadTomlConfig(source)
		if err != nil {
			return fmt.Errorf("failed to load config: %w", err)
		}

		// Verify the provider exists
		if _, exists := tc.Provider[name]; !exists {
			return fmt.Errorf("provider %q not found in %s", name, source)
		}

		tc.DefaultProvider = name
		config.DefaultProviderName = name

		if err := config.SaveTomlConfig(tc, source); err != nil {
			return fmt.Errorf("failed to save config: %w", err)
		}

		fmt.Printf("Default provider set to %q\n", name)
		return nil
	},
}
