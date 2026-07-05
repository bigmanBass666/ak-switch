//go:build unit

package keypool

import (
	"bytes"
	"testing"
)

// testKey returns a valid 32-byte key for AES-256.
func testKey(b byte) []byte {
	k := make([]byte, 32)
	for i := range k {
		k[i] = b
	}
	return k
}

func TestEncryptDecrypt_RoundTrip(t *testing.T) {
	key := testKey('K')
	original := "hello, world! 你好"

	encrypted, err := EncryptString(original, key)
	if err != nil {
		t.Fatalf("EncryptString: %v", err)
	}
	if encrypted == "" {
		t.Fatal("EncryptString returned empty string")
	}
	if encrypted == original {
		t.Error("EncryptString returned plaintext (no encryption)")
	}

	decrypted, err := DecryptString(encrypted, key)
	if err != nil {
		t.Fatalf("DecryptString: %v", err)
	}
	if decrypted != original {
		t.Errorf("DecryptString = %q, want %q", decrypted, original)
	}
}

func TestEncryptDecrypt_WrongKey(t *testing.T) {
	keyA := testKey('A')
	keyB := testKey('B')
	original := "secret data"

	encrypted, err := EncryptString(original, keyA)
	if err != nil {
		t.Fatalf("EncryptString: %v", err)
	}

	_, err = DecryptString(encrypted, keyB)
	if err == nil {
		t.Error("DecryptString with wrong key: expected error, got nil")
	}
}

func TestEncryptDecrypt_TamperedCiphertext(t *testing.T) {
	key := testKey('T')
	plaintext := []byte("tamper test")

	ciphertext, err := EncryptKey(plaintext, key)
	if err != nil {
		t.Fatalf("EncryptKey: %v", err)
	}

	// Modify one byte in the nonce portion
	ciphertext[0] ^= 0xFF

	_, err = DecryptKey(ciphertext, key)
	if err == nil {
		t.Error("DecryptKey with tampered ciphertext: expected error, got nil")
	}
}

func TestEncryptDecrypt_EmptyKey_Noop(t *testing.T) {
	// Ensure encryption key is not set
	SetEncryptionKey(nil)

	if EncryptionKeySet() {
		t.Fatal("EncryptionKeySet() = true after SetEncryptionKey(nil)")
	}

	original := []byte("noop-test-data")
	encrypted, err := Encrypt(original)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	decrypted, err := Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	if !bytes.Equal(decrypted, original) {
		t.Errorf("Decrypt = %q, want %q", decrypted, original)
	}
}

func TestEncryptionKeySet(t *testing.T) {
	SetEncryptionKey(nil)
	if EncryptionKeySet() {
		t.Error("EncryptionKeySet() = true after SetEncryptionKey(nil)")
	}

	key := testKey('S')
	SetEncryptionKey(key)
	if !EncryptionKeySet() {
		t.Error("EncryptionKeySet() = false after SetEncryptionKey(key)")
	}

	SetEncryptionKey([]byte{})
	if EncryptionKeySet() {
		t.Error("EncryptionKeySet() = true after SetEncryptionKey(empty)")
	}
}

func TestEncryptDecrypt_VariousLengths(t *testing.T) {
	key := testKey('V')
	tests := []struct {
		name  string
		input string
	}{
		{"empty", ""},
		{"single byte", "x"},
		{"short", "hello"},
		{"medium", "the quick brown fox jumps over the lazy dog"},
		{"long", string(bytes.Repeat([]byte("A"), 4096))},
		{"unicode", "你好，世界！"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encrypted, err := EncryptString(tt.input, key)
			if err != nil {
				t.Fatalf("EncryptString: %v", err)
			}

			decrypted, err := DecryptString(encrypted, key)
			if err != nil {
				t.Fatalf("DecryptString: %v", err)
			}

			if decrypted != tt.input {
				t.Errorf("DecryptString = %q, want %q", decrypted, tt.input)
			}
		})
	}
}

func TestEncryptDeterministicNonce(t *testing.T) {
	key := testKey('R')
	plaintext := []byte("same plaintext every time")

	c1, err := EncryptKey(plaintext, key)
	if err != nil {
		t.Fatalf("first EncryptKey: %v", err)
	}

	c2, err := EncryptKey(plaintext, key)
	if err != nil {
		t.Fatalf("second EncryptKey: %v", err)
	}

	if bytes.Equal(c1, c2) {
		t.Error("EncryptKey produced identical output for same input (nonce is not random)")
	}
}

func TestEncryptDecrypt_NilSlice(t *testing.T) {
	key := testKey('N')

	encrypted, err := EncryptKey(nil, key)
	if err != nil {
		t.Fatalf("EncryptKey(nil): %v", err)
	}

	decrypted, err := DecryptKey(encrypted, key)
	if err != nil {
		t.Fatalf("DecryptKey: %v", err)
	}

	if len(decrypted) != 0 {
		t.Errorf("DecryptKey = %v (len=%d), want empty slice", decrypted, len(decrypted))
	}
}

func TestEncryptDecrypt_EmptyKeyExplicit(t *testing.T) {
	_, err := EncryptKey([]byte("data"), nil)
	if err == nil {
		t.Error("EncryptKey with nil key: expected error, got nil")
	}

	_, err = DecryptKey([]byte("data"), nil)
	if err == nil {
		t.Error("DecryptKey with nil key: expected error, got nil")
	}

	_, err = EncryptKey([]byte("data"), []byte{})
	if err == nil {
		t.Error("EncryptKey with empty key: expected error, got nil")
	}
}

func TestEncryptDecrypt_PackageLevelRoundTrip(t *testing.T) {
	key := testKey('P')
	SetEncryptionKey(key)

	if !EncryptionKeySet() {
		t.Fatal("EncryptionKeySet() = false after SetEncryptionKey")
	}

	original := []byte("package-level round trip")
	encrypted, err := Encrypt(original)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	decrypted, err := Decrypt(encrypted)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}

	if !bytes.Equal(decrypted, original) {
		t.Errorf("Decrypt = %q, want %q", decrypted, original)
	}
}

func TestEncryptDecrypt_PackageLevelWrongKey(t *testing.T) {
	// Set a key
	SetEncryptionKey(testKey('1'))

	original := []byte("data with key")
	encrypted, err := Encrypt(original)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}

	// Change the key
	SetEncryptionKey(testKey('2'))

	_, err = Decrypt(encrypted)
	if err == nil {
		t.Error("Decrypt after key change: expected error, got nil")
	}
}
