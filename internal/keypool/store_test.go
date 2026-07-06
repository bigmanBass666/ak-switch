//go:build unit

package keypool

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"akswitch/internal/config"
)

func TestLoadKeysFromFile_NotEmpty(t *testing.T) {
	SetEncryptionKey(nil)
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
	SetEncryptionKey(nil)
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
	SetEncryptionKey(nil)
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
	SetEncryptionKey(nil)
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
	SetEncryptionKey(nil)
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
	SetEncryptionKey(nil)
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

// ── Encryption integration tests ──────────────────────────────

func testEncryptKey() []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = 'K'
	}
	return k
}

func TestSaveLoad_Encrypted_PlaintextNotInFile(t *testing.T) {
	// Set encryption key
	SetEncryptionKey(testEncryptKey())
	defer SetEncryptionKey(nil)

	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")

	store := &KeyStore{
		Keys: []KeyEntry{
			{Key: "nvapi-secret-key-1", Name: "prod"},
			{Key: "nvapi-secret-key-2", Name: "staging"},
		},
	}

	if err := SaveFullStore(path, store); err != nil {
		t.Fatalf("SaveFullStore: %v", err)
	}

	// Read raw file — keys should not be plaintext
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)

	if strings.Contains(content, "nvapi-secret-key-1") {
		t.Error("plaintext key found in encrypted file")
	}
	if strings.Contains(content, "nvapi-secret-key-2") {
		t.Error("plaintext key found in encrypted file")
	}
}

func TestSaveLoad_Encrypted_RoundTrip(t *testing.T) {
	SetEncryptionKey(testEncryptKey())
	defer SetEncryptionKey(nil)

	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")

	original := &KeyStore{
		Keys: []KeyEntry{
			{Key: "nvapi-secret-key-1", Name: "prod", Disabled: false},
			{Key: "nvapi-secret-key-2", Name: "", Disabled: true},
		},
	}

	if err := SaveFullStore(path, original); err != nil {
		t.Fatalf("SaveFullStore: %v", err)
	}

	loaded, err := LoadFullStore(path)
	if err != nil {
		t.Fatalf("LoadFullStore: %v", err)
	}

	if len(loaded.Keys) != 2 {
		t.Fatalf("got %d entries, want 2", len(loaded.Keys))
	}
	if loaded.Keys[0].Key != "nvapi-secret-key-1" || loaded.Keys[0].Name != "prod" || loaded.Keys[0].Disabled {
		t.Errorf("entry 0 mismatch: %+v", loaded.Keys[0])
	}
	if loaded.Keys[1].Key != "nvapi-secret-key-2" || loaded.Keys[1].Name != "" || !loaded.Keys[1].Disabled {
		t.Errorf("entry 1 mismatch: %+v", loaded.Keys[1])
	}
}

func TestSaveLoad_Encrypted_WrongKey(t *testing.T) {
	// Save with key K
	SetEncryptionKey(testEncryptKey())
	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")

	store := &KeyStore{Keys: []KeyEntry{{Key: "my-secret-key"}}}
	if err := SaveFullStore(path, store); err != nil {
		t.Fatalf("SaveFullStore: %v", err)
	}

	// Load with a different key
	wrongKey := make([]byte, 32)
	for i := range wrongKey {
		wrongKey[i] = 'X'
	}
	SetEncryptionKey(wrongKey)
	defer SetEncryptionKey(nil)

	_, err := LoadFullStore(path)
	if err == nil {
		t.Error("LoadFullStore with wrong key: expected error, got nil")
	}
}

func TestSaveLoad_NoEncryption_PreservesPlaintext(t *testing.T) {
	// Ensure no encryption key is set
	SetEncryptionKey(nil)

	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")

	store := &KeyStore{
		Keys: []KeyEntry{
			{Key: "plaintext-key-1", Name: "test"},
		},
	}

	if err := SaveFullStore(path, store); err != nil {
		t.Fatalf("SaveFullStore: %v", err)
	}

	// Raw file should contain plaintext key
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !strings.Contains(string(data), "plaintext-key-1") {
		t.Error("plaintext key should appear in unencrypted file")
	}

	// Load should return the original key
	loaded, err := LoadFullStore(path)
	if err != nil {
		t.Fatalf("LoadFullStore: %v", err)
	}
	if loaded.Keys[0].Key != "plaintext-key-1" {
		t.Errorf("key = %q, want %q", loaded.Keys[0].Key, "plaintext-key-1")
	}
}

func TestSaveLoad_Encrypted_TamperedFile(t *testing.T) {
	SetEncryptionKey(testEncryptKey())
	defer SetEncryptionKey(nil)

	dir := t.TempDir()
	path := filepath.Join(dir, "keys.json")

	store := &KeyStore{Keys: []KeyEntry{{Key: "original-key"}}}
	if err := SaveFullStore(path, store); err != nil {
		t.Fatalf("SaveFullStore: %v", err)
	}

	// Read, tamper, write back
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	// Flip a byte in the first key field (roughly)
	if len(data) > 50 {
		data[40] ^= 0xFF
		os.WriteFile(path, data, 0644)
	}

	_, err = LoadFullStore(path)
	if err == nil {
		t.Error("LoadFullStore with tampered data: expected error, got nil")
	}
}

// ── LoadKeysFromStore tests ─────────────────────────────────────

func TestLoadKeysFromStore_CustomFile(t *testing.T) {
	SetEncryptionKey(nil)

	dir := t.TempDir()
	config.ConfigDir = dir
	defer func() { config.ConfigDir = "" }()

	// Write a custom keys file
	keysPath := filepath.Join(dir, "my-keys.json")
	store := &KeyStore{
		Keys: []KeyEntry{
			{Key: "sk-key-a", Name: "prod"},
			{Key: "sk-key-b", Name: "staging"},
		},
	}
	data, err := json.MarshalIndent(store, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(keysPath, data, 0644); err != nil {
		t.Fatalf("write keys file: %v", err)
	}

	cfg := &config.Config{KeysFile: keysPath}
	keys, names, loaded := LoadKeysFromStore("test", cfg)
	if !loaded {
		t.Fatal("LoadKeysFromStore: loaded=false, want true")
	}
	if len(keys) != 2 {
		t.Fatalf("got %d keys, want 2", len(keys))
	}
	if keys[0] != "sk-key-a" || keys[1] != "sk-key-b" {
		t.Errorf("keys mismatch: %v", keys)
	}
	if names[0] != "prod" || names[1] != "staging" {
		t.Errorf("names mismatch: %v", names)
	}
}

func TestLoadKeysFromStore_CustomFileNotExist(t *testing.T) {
	SetEncryptionKey(nil)

	dir := t.TempDir()
	config.ConfigDir = dir
	defer func() { config.ConfigDir = "" }()

	cfg := &config.Config{KeysFile: filepath.Join(dir, "nonexistent.json")}
	keys, names, loaded := LoadKeysFromStore("test", cfg)
	if loaded {
		t.Error("LoadKeysFromStore: loaded=true, want false")
	}
	if keys != nil {
		t.Errorf("keys = %v, want nil", keys)
	}
	if names != nil {
		t.Errorf("names = %v, want nil", names)
	}
}

func TestLoadKeysFromStore_NoSource(t *testing.T) {
	SetEncryptionKey(nil)

	dir := t.TempDir()
	config.ConfigDir = dir
	defer func() { config.ConfigDir = "" }()

	cfg := &config.Config{}
	keys, names, loaded := LoadKeysFromStore("test", cfg)
	if loaded {
		t.Error("LoadKeysFromStore: loaded=true, want false")
	}
	if keys != nil {
		t.Errorf("keys = %v, want nil", keys)
	}
	if names != nil {
		t.Errorf("names = %v, want nil", names)
	}
}
