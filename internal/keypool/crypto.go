package keypool

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"sync"
)

var (
	encryptionKey []byte
	encryptionMu  sync.RWMutex
)

// SetEncryptionKey sets the package-level encryption key.
// If key is nil or empty, encryption is disabled.
func SetEncryptionKey(key []byte) {
	encryptionMu.Lock()
	defer encryptionMu.Unlock()
	if len(key) == 0 {
		encryptionKey = nil
		return
	}
	encryptionKey = make([]byte, len(key))
	copy(encryptionKey, key)
}

// EncryptionKeySet returns true if a package-level encryption key has been set.
func EncryptionKeySet() bool {
	encryptionMu.RLock()
	defer encryptionMu.RUnlock()
	return encryptionKey != nil
}

// EncryptKey encrypts plaintext with AES-256-GCM using the given key.
// Returns nonce || ciphertext.
func EncryptKey(plaintext []byte, key []byte) ([]byte, error) {
	if len(key) == 0 {
		return nil, errors.New("encryption key is empty")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cipher.NewGCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("rand.Read: %w", err)
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// DecryptKey decrypts data that was encrypted with EncryptKey.
// data should be nonce || ciphertext.
func DecryptKey(data []byte, key []byte) ([]byte, error) {
	if len(key) == 0 {
		return nil, errors.New("decryption key is empty")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("aes.NewCipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("cipher.NewGCM: %w", err)
	}
	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return nil, errors.New("ciphertext too short")
	}
	nonce, ciphertext := data[:nonceSize], data[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("gcm.Open: %w", err)
	}
	return plaintext, nil
}

// EncryptString encrypts a string with the given key and returns base64-encoded ciphertext.
func EncryptString(plaintext string, key []byte) (string, error) {
	ciphertext, err := EncryptKey([]byte(plaintext), key)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// DecryptString decodes base64 and decrypts the result using the given key.
func DecryptString(ciphertext string, key []byte) (string, error) {
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}
	plaintext, err := DecryptKey(data, key)
	if err != nil {
		return "", err
	}
	return string(plaintext), nil
}

// Encrypt encrypts plaintext using the package-level encryption key.
// If no encryption key is set, returns base64-encoded plaintext (no-op pass-through).
func Encrypt(plaintext []byte) (string, error) {
	encryptionMu.RLock()
	key := encryptionKey
	encryptionMu.RUnlock()
	if key == nil {
		return base64.StdEncoding.EncodeToString(plaintext), nil
	}
	return EncryptString(string(plaintext), key)
}

// Decrypt decrypts ciphertext using the package-level encryption key.
// If no encryption key is set, returns base64-decoded data (no-op pass-through).
func Decrypt(ciphertext string) ([]byte, error) {
	encryptionMu.RLock()
	key := encryptionKey
	encryptionMu.RUnlock()
	if key == nil {
		return base64.StdEncoding.DecodeString(ciphertext)
	}
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return nil, fmt.Errorf("base64 decode: %w", err)
	}
	return DecryptKey(data, key)
}