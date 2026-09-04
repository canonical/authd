// Package password provides functions for creating and using the hashed password file.
package password

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	"golang.org/x/crypto/argon2"
)

// ErrInvalidHash indicates that a password file does not contain a valid hash.
var ErrInvalidHash = errors.New("invalid password hash")

// HashAndStorePassword hashes the password and stores it in the data directory.
func HashAndStorePassword(password, path string) error {
	encoded, err := HashPassword(password)
	if err != nil {
		return err
	}
	return StoreHashedPassword(encoded, path)
}

// HashPassword hashes a plaintext password and returns the base64-encoded
// salt+hash string. The result can later be persisted with StoreHashedPassword.
//
// Splitting hashing from storage lets callers narrow the plaintext memory
// window: hash early, then drop the plaintext.
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("could not generate salt: %w", err)
	}

	hash := hashPassword(password, salt)
	return base64.StdEncoding.EncodeToString(append(salt, hash...)), nil
}

// StoreHashedPassword writes a pre-computed password hash (from HashPassword)
// to the given path.
func StoreHashedPassword(encoded, path string) error {
	// Ensure that the password file's parent directory exists.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("could not create password parent directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(encoded), 0o600); err != nil {
		return fmt.Errorf("could not store password: %w", err)
	}
	return nil
}

// ValidateHash checks that the password file contains a valid encoded hash.
func ValidateHash(path string) error {
	_, err := loadHash(path)
	return err
}

// CheckPassword checks if the provided password matches the hash stored in the password file.
func CheckPassword(password, path string) (bool, error) {
	decoded, err := loadHash(path)
	if err != nil {
		return false, err
	}

	salt, hash := decoded[:16], decoded[16:]
	if !slices.Equal(hash, hashPassword(password, salt)) {
		return false, nil
	}

	return true, nil
}

func loadHash(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("could not read password file: %w", err)
	}

	decoded, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		return nil, fmt.Errorf("could not decode password: %w: %w", ErrInvalidHash, err)
	}
	if len(decoded) != 16+32 {
		return nil, fmt.Errorf("could not decode password: %w", ErrInvalidHash)
	}
	return decoded, nil
}

func hashPassword(password string, salt []byte) []byte {
	// If you change these parameters, update the section in the security overview doc.
	return argon2.IDKey([]byte(password), salt, 1, 64*1024, 4, 32)
}
