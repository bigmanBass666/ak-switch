package cmd

import (
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"akswitch/internal/config"
	"akswitch/internal/keypool"
	"akswitch/internal/utils"

	"github.com/spf13/cobra"
)

// keysPath returns the keys file path for a given provider.
// Directory: <XDG config dir>/keys/
// File: <provider>.enc
func keysPath(provider string) (string, error) {
	xdgPath, err := config.XDGConfigPath()
	if err != nil {
		return "", fmt.Errorf("failed to determine XDG config path: %w", err)
	}
	keysDir := filepath.Join(filepath.Dir(xdgPath), "keys")
	if err := os.MkdirAll(keysDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create keys directory %s: %w", keysDir, err)
	}
	return filepath.Join(keysDir, provider+".enc"), nil
}

// setupEncryption reads KEYS_ENCRYPTION_KEY from the environment and sets the
// package-level encryption key in the keypool package.
func setupEncryption() {
	encKeyHex := os.Getenv("KEYS_ENCRYPTION_KEY")
	if encKeyHex != "" {
		key, err := hex.DecodeString(encKeyHex)
		if err == nil {
			keypool.SetEncryptionKey(key)
		}
	}
}

func init() {
	rootCmd.AddCommand(keyCmd)
	keyCmd.AddCommand(keyAddCmd)
	keyCmd.AddCommand(keyListCmd)
	keyCmd.AddCommand(keyRemoveCmd)
	keyCmd.AddCommand(keyDisableCmd)
	keyCmd.AddCommand(keyEnableCmd)

	keyAddCmd.Flags().StringP("name", "n", "", "Display name for the key")
}

var keyCmd = &cobra.Command{
	Use:   "key",
	Short: "Manage API keys",
	Long:  `Add, list, remove, disable, and enable API keys for a provider.`,
}

var keyAddCmd = &cobra.Command{
	Use:   "add <provider> <key>",
	Short: "Add a new API key for a provider",
	Long: `Add a new API key to the encrypted key store for the specified provider.

The key is appended to the provider's key file. If the file does not exist,
it is created. Keys are encrypted using AES-256-GCM when KEYS_ENCRYPTION_KEY
is set; otherwise they are stored as base64-encoded plaintext.

Example:
  akswitch key add nvidia sk-xxxxxxxxxxxxxxxx
  akswitch key add nvidia sk-xxxxxxxxxxxxxxxx --name my-key`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider := args[0]
		apiKey := args[1]
		name, _ := cmd.Flags().GetString("name")

		setupEncryption()

		path, err := keysPath(provider)
		if err != nil {
			return err
		}

		store, err := keypool.LoadFullStore(path)
		if err != nil {
			return fmt.Errorf("failed to load keys for %q: %w", provider, err)
		}
		if store == nil {
			store = &keypool.KeyStore{Keys: []keypool.KeyEntry{}}
		}

		store.Keys = append(store.Keys, keypool.KeyEntry{
			Key:  apiKey,
			Name: name,
		})

		if err := keypool.SaveFullStore(path, store); err != nil {
			return fmt.Errorf("failed to save keys for %q: %w", provider, err)
		}

		fmt.Printf("Key added to provider %q (total: %d keys)\n", provider, len(store.Keys))
		triggerReload()
		return nil
	},
}

var keyListCmd = &cobra.Command{
	Use:   "list <provider>",
	Short: "List API keys for a provider",
	Long: `Display all API keys for the specified provider with their index,
masked value, status, and optional name.

Example output:
  Keys for provider "nvidia" (from <path>):
    [0] sk-****xx  (active)
    [1] sk-****yy  [disabled]
    [2] sk-****zz  (active)  name: my-key`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider := args[0]

		setupEncryption()

		path, err := keysPath(provider)
		if err != nil {
			return err
		}

		store, err := keypool.LoadFullStore(path)
		if err != nil {
			return fmt.Errorf("failed to load keys for %q: %w", provider, err)
		}

		if store == nil || len(store.Keys) == 0 {
			fmt.Printf("No keys found for provider %q (file: %s)\n", provider, path)
			return nil
		}

		fmt.Printf("Keys for provider %q (from %s):\n", provider, path)
		for i, entry := range store.Keys {
			status := "active"
			if entry.Disabled {
				status = "disabled"
			}
			line := fmt.Sprintf("  [%d] %s  (%s)", i, utils.MaskKey(entry.Key), status)
			if entry.Name != "" {
				line += fmt.Sprintf("  name: %s", entry.Name)
			}
			fmt.Println(line)
		}

		return nil
	},
}

var keyRemoveCmd = &cobra.Command{
	Use:   "remove <provider> <index>",
	Short: "Remove an API key by index",
	Long: `Remove an API key from the provider's key store at the specified index.

The index corresponds to the key's position as shown in 'akswitch key list'.
This operation cannot be undone.

Example:
  akswitch key remove nvidia 0`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider := args[0]
		idx, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("invalid index %q: must be a non-negative integer", args[1])
		}

		setupEncryption()

		path, err := keysPath(provider)
		if err != nil {
			return err
		}

		store, err := keypool.LoadFullStore(path)
		if err != nil {
			return fmt.Errorf("failed to load keys for %q: %w", provider, err)
		}
		if store == nil {
			return fmt.Errorf("no keys found for provider %q", provider)
		}

		if idx < 0 || idx >= len(store.Keys) {
			return fmt.Errorf("index %d out of range: provider %q has %d keys (valid: 0-%d)",
				idx, provider, len(store.Keys), len(store.Keys)-1)
		}

		removed := store.Keys[idx]
		store.Keys = append(store.Keys[:idx], store.Keys[idx+1:]...)

		if err := keypool.SaveFullStore(path, store); err != nil {
			return fmt.Errorf("failed to save keys for %q: %w", provider, err)
		}

		desc := utils.MaskKey(removed.Key)
		if removed.Name != "" {
			desc += fmt.Sprintf(" (name: %s)", removed.Name)
		}
		fmt.Printf("Removed key [%d] %s from provider %q (remaining: %d keys)\n",
			idx, desc, provider, len(store.Keys))
		triggerReload()
		return nil
	},
}

var keyDisableCmd = &cobra.Command{
	Use:   "disable <provider> <index>",
	Short: "Disable an API key by index",
	Long: `Mark an API key as disabled at the specified index.

Disabled keys are not used for new requests but remain in the key store.
Use 'akswitch key remove' to permanently remove a key.

Example:
  akswitch key disable nvidia 1`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider := args[0]
		idx, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("invalid index %q: must be a non-negative integer", args[1])
		}

		setupEncryption()

		path, err := keysPath(provider)
		if err != nil {
			return err
		}

		store, err := keypool.LoadFullStore(path)
		if err != nil {
			return fmt.Errorf("failed to load keys for %q: %w", provider, err)
		}
		if store == nil {
			return fmt.Errorf("no keys found for provider %q", provider)
		}

		if idx < 0 || idx >= len(store.Keys) {
			return fmt.Errorf("index %d out of range: provider %q has %d keys (valid: 0-%d)",
				idx, provider, len(store.Keys), len(store.Keys)-1)
		}

		store.Keys[idx].Disabled = true

		if err := keypool.SaveFullStore(path, store); err != nil {
			return fmt.Errorf("failed to save keys for %q: %w", provider, err)
		}

		desc := utils.MaskKey(store.Keys[idx].Key)
		if store.Keys[idx].Name != "" {
			desc += fmt.Sprintf(" (name: %s)", store.Keys[idx].Name)
		}
		fmt.Printf("Disabled key [%d] %s for provider %q\n", idx, desc, provider)
		triggerReload()
		return nil
	},
}
var keyEnableCmd = &cobra.Command{
	Use:   "enable <provider> <index>",
	Short: "Enable an API key by index",
	Long: `Re-enable a previously disabled API key at the specified index.

The key will be used again for new requests.  The operation triggers a
reload so the server picks up the change.

Example:
  akswitch key enable nvidia 1`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		provider := args[0]
		idx, err := strconv.Atoi(args[1])
		if err != nil {
			return fmt.Errorf("invalid index %q: must be a non-negative integer", args[1])
		}

		setupEncryption()

		path, err := keysPath(provider)
		if err != nil {
			return err
		}

		store, err := keypool.LoadFullStore(path)
		if err != nil {
			return fmt.Errorf("failed to load keys for %q: %w", provider, err)
		}
		if store == nil {
			return fmt.Errorf("no keys found for provider %q", provider)
		}

		if idx < 0 || idx >= len(store.Keys) {
			return fmt.Errorf("index %d out of range: provider %q has %d keys (valid: 0-%d)",
				idx, provider, len(store.Keys), len(store.Keys)-1)
		}

		store.Keys[idx].Disabled = false

		if err := keypool.SaveFullStore(path, store); err != nil {
			return fmt.Errorf("failed to save keys for %q: %w", provider, err)
		}

		desc := utils.MaskKey(store.Keys[idx].Key)
		if store.Keys[idx].Name != "" {
			desc += fmt.Sprintf(" (name: %s)", store.Keys[idx].Name)
		}
		fmt.Printf("Enabled key [%d] %s for provider %q\n", idx, desc, provider)
		triggerReload()
		return nil
	},
}
