package keypool

import (
	"encoding/json"
	"os"
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
	return &store, nil
}

// SaveFullStore writes the complete KeyStore to file.
func SaveFullStore(path string, store *KeyStore) error {
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0644)
}