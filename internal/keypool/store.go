package keypool

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"akswitch/internal/config"
)

// KeyEntry represents a persisted key entry with its metadata.
type KeyEntry struct {
	Key      string `json:"key"`
	Name     string `json:"name,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
}

// KeyStore is a JSON file backed store for API keys.
type KeyStore struct {
	Keys []KeyEntry `json:"keys"`
}

// LoadKeysFromFile reads keys from a JSON file at the given path.
// Returns the keys slice, names slice, and any error.
// If the file does not exist, returns empty slices with nil error.
func LoadKeysFromFile(path string) (keys []string, names []string, err error) {
	store, err := LoadFullStore(path)
	if err != nil {
		return nil, nil, err
	}
	if store == nil {
		return nil, nil, nil
	}
	keys = make([]string, len(store.Keys))
	names = make([]string, len(store.Keys))
	for i, entry := range store.Keys {
		keys[i] = entry.Key
		names[i] = entry.Name
	}
	return keys, names, nil
}

// SaveKeysToFile writes keys to a JSON file at the given path.
// names slice may be nil or shorter than keys.
func SaveKeysToFile(path string, keys []string, names []string) error {
	entries := make([]KeyEntry, len(keys))
	for i, k := range keys {
		name := ""
		if i < len(names) {
			name = names[i]
		}
		entries[i] = KeyEntry{Key: k, Name: name}
	}
	store := &KeyStore{Keys: entries}
	return SaveFullStore(path, store)
}

// LoadFullStore loads the complete KeyStore from file (including disabled state).
// Returns nil store with nil error if the file does not exist.
// If encryption is enabled (via SetEncryptionKey), Key fields are automatically decrypted.
func LoadFullStore(path string) (*KeyStore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var store KeyStore
	if err := json.Unmarshal(data, &store); err != nil {
		return nil, err
	}
	if store.Keys == nil {
		store.Keys = []KeyEntry{}
	}

	// Decrypt keys if encryption is enabled
	if EncryptionKeySet() {
		for i := range store.Keys {
			decrypted, err := Decrypt(store.Keys[i].Key)
			if err != nil {
				return nil, fmt.Errorf("decrypt key %d: %w", i, err)
			}
			store.Keys[i].Key = string(decrypted)
		}
	}

	return &store, nil
}

// SaveFullStore writes the complete KeyStore to file.
// If encryption is enabled (via SetEncryptionKey), Key fields are automatically encrypted.
func SaveFullStore(path string, store *KeyStore) error {
	// Encrypt keys if encryption is enabled (work on a copy to avoid mutating the caller's store)
	if EncryptionKeySet() {
		encrypted := make([]KeyEntry, len(store.Keys))
		for i, entry := range store.Keys {
			enc, err := Encrypt([]byte(entry.Key))
			if err != nil {
				return fmt.Errorf("encrypt key %d: %w", i, err)
			}
			encrypted[i] = KeyEntry{
				Key:      enc,
				Name:     entry.Name,
				Disabled: entry.Disabled,
			}
		}
		store = &KeyStore{Keys: encrypted}
	}

	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}

// LoadKeysFromStore loads API keys for a provider from the configured keys file
// or the standard encrypted store. Returns loaded keys and whether keys were loaded.
func LoadKeysFromStore(name string, cfg *config.Config) (keys, names []string, loaded bool) {
	if cfg.KeysFile != "" {
		fileKeys, fileNames, err := LoadKeysFromFile(cfg.KeysFile)
		if err == nil && fileKeys != nil {
			return fileKeys, fileNames, true
		}
	}
	xdgPath, err := config.XDGConfigPath()
	if err != nil {
		return nil, nil, false
	}
	keyFile := filepath.Join(filepath.Dir(xdgPath), "keys", name+".enc")
	fileKeys, fileNames, err := LoadKeysFromFile(keyFile)
	if err == nil && fileKeys != nil {
		return fileKeys, fileNames, true
	}
	return nil, nil, false
}
