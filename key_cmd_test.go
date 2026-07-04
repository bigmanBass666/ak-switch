package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"akswitch/internal/config"
	"akswitch/internal/keypool"
)

// ── Key CRUD Acceptance Tests ─────────────────────────

// TestKeyAdd_AddsKey verifies that "akswitch key add <provider> <key>"
// adds a key to the provider's encrypted key store.
func TestKeyAdd_AddsKey(t *testing.T) {
	resetConfigEnv()
	tmpDir := t.TempDir()
	config.ConfigDir = tmpDir
t.Cleanup(func() { config.ConfigDir = "" })

	// Init config and add a provider
	xdgPath, err := config.XDGConfigPath()
	if err != nil {
		t.Fatalf("XDGConfigPath failed: %v", err)
	}
	runCommand(t, "akswitch", "config", "init", "-p", xdgPath)
	runCommand(t, "akswitch", "provider", "add", "keytest",
		"--target", "https://keytest.api.com/v1",
		"--port", "9501",
	)

	// Add a key
	runCommand(t, "akswitch", "key", "add", "keytest", "sk-test-key-12345")

	// Verify key was added to the store
	keysDir := filepath.Join(filepath.Dir(xdgPath), "keys")
	keyFile := filepath.Join(keysDir, "keytest.enc")
	store, err := keypool.LoadFullStore(keyFile)
	if err != nil {
		t.Fatalf("LoadFullStore failed: %v", err)
	}
	if store == nil || len(store.Keys) == 0 {
		t.Fatal("no keys found in store after add")
	}
	if store.Keys[0].Key != "sk-test-key-12345" {
		t.Errorf("Key = %q, want %q", store.Keys[0].Key, "sk-test-key-12345")
	}
}

// TestKeyList_ShowsKeys verifies that "akswitch key list <provider>"
// displays the correct key information.
func TestKeyList_ShowsKeys(t *testing.T) {
	resetConfigEnv()
	tmpDir := t.TempDir()
	config.ConfigDir = tmpDir
t.Cleanup(func() { config.ConfigDir = "" })

	xdgPath, err := config.XDGConfigPath()
	if err != nil {
		t.Fatalf("XDGConfigPath failed: %v", err)
	}
	runCommand(t, "akswitch", "config", "init", "-p", xdgPath)
	runCommand(t, "akswitch", "provider", "add", "listtest",
		"--target", "https://listtest.api.com/v1",
		"--port", "9502",
	)

	// Add two keys
	runCommand(t, "akswitch", "key", "add", "listtest", "sk-list-key-aaaa")
	runCommand(t, "akswitch", "key", "add", "listtest", "sk-list-key-bbbb")

	// Capture list output
	var stdout bytes.Buffer
	oldStdout := os.Stdout
	r, w, _ := os.Pipe()
	os.Stdout = w

	runCommand(t, "akswitch", "key", "list", "listtest")

	w.Close()
	os.Stdout = oldStdout
	io.Copy(&stdout, r)

	output := stdout.String()
	if !strings.Contains(output, "listtest") {
		t.Errorf("output missing provider name:\n%s", output)
	}
	if !strings.Contains(output, "...") {
		t.Errorf("output missing masked key:\n%s", output)
	}
	if !strings.Contains(output, "active") {
		t.Errorf("output missing key status:\n%s", output)
	}
}

// TestKeyRemove_RemovesKey verifies that "akswitch key remove <provider> <index>"
// removes the key at the given index.
func TestKeyRemove_RemovesKey(t *testing.T) {
	resetConfigEnv()
	tmpDir := t.TempDir()
	config.ConfigDir = tmpDir
t.Cleanup(func() { config.ConfigDir = "" })

	xdgPath, err := config.XDGConfigPath()
	if err != nil {
		t.Fatalf("XDGConfigPath failed: %v", err)
	}
	runCommand(t, "akswitch", "config", "init", "-p", xdgPath)
	runCommand(t, "akswitch", "provider", "add", "removetest",
		"--target", "https://removetest.api.com/v1",
		"--port", "9503",
	)

	// Add two keys, then remove the first
	runCommand(t, "akswitch", "key", "add", "removetest", "sk-remove-key-1")
	runCommand(t, "akswitch", "key", "add", "removetest", "sk-remove-key-2")
	runCommand(t, "akswitch", "key", "remove", "removetest", "0")

	// Verify key[0] was removed (should now be "sk-remove-key-2")
	keysDir := filepath.Join(filepath.Dir(xdgPath), "keys")
	keyFile := filepath.Join(keysDir, "removetest.enc")
	store, err := keypool.LoadFullStore(keyFile)
	if err != nil {
		t.Fatalf("LoadFullStore failed: %v", err)
	}
	if len(store.Keys) != 1 {
		t.Fatalf("expected 1 key after remove, got %d", len(store.Keys))
	}
	if store.Keys[0].Key != "sk-remove-key-2" {
		t.Errorf("remaining key = %q, want %q", store.Keys[0].Key, "sk-remove-key-2")
	}
}

// TestKeyDisable_DisablesKey verifies that "akswitch key disable <provider> <index>"
// marks the key as disabled.
func TestKeyDisable_DisablesKey(t *testing.T) {
	resetConfigEnv()
	tmpDir := t.TempDir()
	config.ConfigDir = tmpDir
t.Cleanup(func() { config.ConfigDir = "" })

	xdgPath, err := config.XDGConfigPath()
	if err != nil {
		t.Fatalf("XDGConfigPath failed: %v", err)
	}
	runCommand(t, "akswitch", "config", "init", "-p", xdgPath)
	runCommand(t, "akswitch", "provider", "add", "disabletest",
		"--target", "https://disabletest.api.com/v1",
		"--port", "9504",
	)

	// Add a key and disable it
	runCommand(t, "akswitch", "key", "add", "disabletest", "sk-disable-key-1")
	runCommand(t, "akswitch", "key", "disable", "disabletest", "0")

	// Verify key is disabled
	keysDir := filepath.Join(filepath.Dir(xdgPath), "keys")
	keyFile := filepath.Join(keysDir, "disabletest.enc")
	store, err := keypool.LoadFullStore(keyFile)
	if err != nil {
		t.Fatalf("LoadFullStore failed: %v", err)
	}
	if len(store.Keys) != 1 {
		t.Fatalf("expected 1 key, got %d", len(store.Keys))
	}
	if !store.Keys[0].Disabled {
		t.Error("key should be disabled but Disabled = false")
	}
}

// TestKeyRemove_InvalidIndex verifies that removing with an out-of-range
// index returns an error.
func TestKeyRemove_InvalidIndex(t *testing.T) {
	resetConfigEnv()
	tmpDir := t.TempDir()
	config.ConfigDir = tmpDir
t.Cleanup(func() { config.ConfigDir = "" })

	xdgPath, err := config.XDGConfigPath()
	if err != nil {
		t.Fatalf("XDGConfigPath failed: %v", err)
	}
	runCommand(t, "akswitch", "config", "init", "-p", xdgPath)
	runCommand(t, "akswitch", "provider", "add", "errtest",
		"--target", "https://errtest.api.com/v1",
		"--port", "9505",
	)
	runCommand(t, "akswitch", "key", "add", "errtest", "sk-err-key-1")

	err = runCommand(t, "akswitch", "key", "remove", "errtest", "999")
	if err == nil {
		t.Fatal("expected error for out-of-range index, got nil")
	}
	if !strings.Contains(err.Error(), "out of range") {
		t.Errorf("error message = %q, want it to contain 'out of range'", err.Error())
	}
}