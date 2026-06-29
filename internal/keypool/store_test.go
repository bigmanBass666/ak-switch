package keypool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestLoadKeysFromFile_NotEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")

	store := &KeyStore{
		Keys: []KeyEntry{
			{Key: "nvkey-1", Name: "prod"},
			{Key: "nvkey-2", Name: "staging"},
			{Key: "nvkey-3"},
		},
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	keys, names, err := LoadKeysFromFile(path)
	if err != nil {
		t.Fatalf("LoadKeysFromFile: %v", err)
	}
	if len(keys) != 3 {
		t.Fatalf("got %d keys, want 3", len(keys))
	}
	if keys[0] != "nvkey-1" || keys[1] != "nvkey-2" || keys[2] != "nvkey-3" {
		t.Errorf("keys mismatch: %v", keys)
	}
	if names[0] != "prod" || names[1] != "staging" || names[2] != "" {
		t.Errorf("names mismatch: %v", names)
	}
}

func TestLoadKeysFromFile_NotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.json")

	keys, names, err := LoadKeysFromFile(path)
	if err != nil {
		t.Fatalf("LoadKeysFromFile for missing file: %v", err)
	}
	if keys != nil {
		t.Errorf("keys = %v, want nil", keys)
	}
	if names != nil {
		t.Errorf("names = %v, want nil", names)
	}
}

func TestLoadKeysFromFile_Malformed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.json")

	if err := os.WriteFile(path, []byte("not json"), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	_, _, err := LoadKeysFromFile(path)
	if err == nil {
		t.Error("expected error for malformed JSON, got nil")
	}
}

func TestSaveThenLoad(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")

	keys := []string{"k1", "k2", "k3"}
	names := []string{"alpha", "beta", "gamma"}

	if err := SaveKeysToFile(path, keys, names); err != nil {
		t.Fatalf("SaveKeysToFile: %v", err)
	}

	gotKeys, gotNames, err := LoadKeysFromFile(path)
	if err != nil {
		t.Fatalf("LoadKeysFromFile: %v", err)
	}

	if len(gotKeys) != 3 {
		t.Fatalf("got %d keys, want 3", len(gotKeys))
	}
	for i := range keys {
		if gotKeys[i] != keys[i] {
			t.Errorf("key[%d] = %q, want %q", i, gotKeys[i], keys[i])
		}
		if gotNames[i] != names[i] {
			t.Errorf("name[%d] = %q, want %q", i, gotNames[i], names[i])
		}
	}
}

func TestSaveThenLoad_NamesShorterThanKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")

	keys := []string{"k1", "k2", "k3"}
	names := []string{"alpha"}

	if err := SaveKeysToFile(path, keys, names); err != nil {
		t.Fatalf("SaveKeysToFile: %v", err)
	}

	gotKeys, gotNames, err := LoadKeysFromFile(path)
	if err != nil {
		t.Fatalf("LoadKeysFromFile: %v", err)
	}

	if gotKeys[0] != "k1" || gotNames[0] != "alpha" {
		t.Errorf("entry 0: key=%q name=%q", gotKeys[0], gotNames[0])
	}
	if gotKeys[1] != "k2" || gotNames[1] != "" {
		t.Errorf("entry 1: key=%q name=%q", gotKeys[1], gotNames[1])
	}
	if gotKeys[2] != "k3" || gotNames[2] != "" {
		t.Errorf("entry 2: key=%q name=%q", gotKeys[2], gotNames[2])
	}
}

func TestSaveFullStore_AndLoadFullStore(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")

	store := &KeyStore{
		Keys: []KeyEntry{
			{Key: "nvkey-1", Name: "prod", Disabled: false},
			{Key: "nvkey-2", Name: "", Disabled: true},
		},
	}

	if err := SaveFullStore(path, store); err != nil {
		t.Fatalf("SaveFullStore: %v", err)
	}

	loaded, err := LoadFullStore(path)
	if err != nil {
		t.Fatalf("LoadFullStore: %v", err)
	}

	if len(loaded.Keys) != 2 {
		t.Fatalf("got %d entries, want 2", len(loaded.Keys))
	}

	entry0 := loaded.Keys[0]
	if entry0.Key != "nvkey-1" || entry0.Name != "prod" || entry0.Disabled {
		t.Errorf("entry 0: key=%q name=%q disabled=%v", entry0.Key, entry0.Name, entry0.Disabled)
	}

	entry1 := loaded.Keys[1]
	if entry1.Key != "nvkey-2" || entry1.Name != "" || !entry1.Disabled {
		t.Errorf("entry 1: key=%q name=%q disabled=%v", entry1.Key, entry1.Name, entry1.Disabled)
	}
}

func TestLoadFullStore_NotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent.json")

	store, err := LoadFullStore(path)
	if err != nil {
		t.Fatalf("LoadFullStore for missing file: %v", err)
	}
	if store != nil {
		t.Errorf("store = %+v, want nil", store)
	}
}

func TestSaveFullStore_IndentedFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")

	store := &KeyStore{
		Keys: []KeyEntry{
			{Key: "nvkey-1", Name: "prod"},
		},
	}
	if err := SaveFullStore(path, store); err != nil {
		t.Fatalf("SaveFullStore: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	var parsed struct {
		Keys []struct {
			Key      string `json:"key"`
			Name     string `json:"name"`
			Disabled bool   `json:"disabled"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("unmarshal saved file: %v", err)
	}
	if parsed.Keys[0].Key != "nvkey-1" {
		t.Errorf("key = %q, want %q", parsed.Keys[0].Key, "nvkey-1")
	}
}

func TestLoadKeysFromFile_EmptyKeyList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.json")

	store := &KeyStore{Keys: []KeyEntry{}}
	data, _ := json.MarshalIndent(store, "", "  ")
	os.WriteFile(path, data, 0644)

	keys, names, err := LoadKeysFromFile(path)
	if err != nil {
		t.Fatalf("LoadKeysFromFile: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("keys = %v, want empty", keys)
	}
	if names == nil {
		t.Errorf("names = nil, want non-nil")
	}
}