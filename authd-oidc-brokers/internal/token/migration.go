package token

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ubuntu/authd-oidc-brokers/internal/fileutils"
	"github.com/ubuntu/authd/log"
	"golang.org/x/crypto/scrypt"
)

const saltLen = 32

// UseOldEncryptedToken checks if the password file or the old encrypted token file exists. It returns false if the
// password file exists, true if only the old encrypted token file exists, and an error if neither exists.
func UseOldEncryptedToken(passwordPath, tokenPath, oldEncryptedTokenPath string) (bool, error) {
	passwordExists, err := fileutils.FileExists(passwordPath)
	if err != nil {
		return false, fmt.Errorf("could not check password file: %w", err)
	}
	tokenExists, err := fileutils.FileExists(tokenPath)
	if err != nil {
		return false, fmt.Errorf("could not check token file: %w", err)
	}
	if passwordExists && tokenExists {
		return false, nil
	}

	oldEncryptedTokenExists, err := fileutils.FileExists(oldEncryptedTokenPath)
	if err != nil {
		return false, fmt.Errorf("could not check old encrypted token file: %w", err)
	}
	if !oldEncryptedTokenExists {
		if !passwordExists {
			// We mention the password file in the error message instead of the old encrypted token file, because the latter
			// is only used for backward compatibility, so if it doesn't exist, the missing password file is the real issue.
			return false, fmt.Errorf("password file %q does not exist", passwordPath)
		}
		// We only get here if the password file exists and the token file does not exist.
		return false, fmt.Errorf("token file %q does not exist", tokenPath)
	}

	return true, nil
}

// LoadOldEncryptedAuthInfo reads the token in the old encrypted format from the given path and decrypts it using the
// given password. It's used for backward compatibility.
func LoadOldEncryptedAuthInfo(path, password string) (AuthCachedInfo, error) {
	encryptedData, err := os.ReadFile(path)
	if err != nil {
		return AuthCachedInfo{}, fmt.Errorf("could not read token: %v", err)
	}
	jsonData, err := decrypt(encryptedData, []byte(password))
	if err != nil {
		return AuthCachedInfo{}, fmt.Errorf("could not decrypt token: %v", err)
	}

	var cachedInfo AuthCachedInfo
	if err := json.Unmarshal(jsonData, &cachedInfo); err != nil {
		return AuthCachedInfo{}, fmt.Errorf("could not unmarshal token: %v", err)
	}

	return cachedInfo, nil
}

func decrypt(blob, key []byte) ([]byte, error) {
	if len(blob) < saltLen {
		return nil, fmt.Errorf("blob is too short to contain a valid salt")
	}

	salt, data := blob[len(blob)-saltLen:], blob[:len(blob)-saltLen]

	derivedKey, err := scrypt.Key(key, salt, 32768, 8, 1, 32)
	if err != nil {
		return nil, err
	}

	block, err := aes.NewCipher(derivedKey)
	if err != nil {
		return nil, err
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}

	if len(data) < gcm.NonceSize() {
		return nil, fmt.Errorf("data is too short to contain a valid nonce")
	}

	decrypted, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
	if err != nil {
		return nil, err
	}

	return decrypted, nil
}

// CleanupOldEncryptedToken removes the old encrypted token file at the given path and its parent directories if they are empty.
func CleanupOldEncryptedToken(path string) {
	exists, err := fileutils.FileExists(path)
	if err != nil {
		log.Warningf(context.Background(), "Failed to check if old encrypted token exists %s: %v", path, err)
	}
	if !exists {
		return
	}

	if err := os.Remove(path); err != nil {
		log.Warningf(context.Background(), "Failed to remove old encrypted token %s: %v", path, err)
		return
	}

	// Also remove the parent directory and the parent's parent directory if they are empty. The directory structure was:
	//   $SNAP_DATA/cache/$ISSUER/$USERNAME.cache
	// so we try to remove the $SNAP_DATA/cache/$ISSUER directory and the $SNAP_DATA/cache directory.

	// Check if the parent directory is empty.
	empty, err := fileutils.IsDirEmpty(filepath.Dir(path))
	if err != nil {
		log.Warningf(context.Background(), "Failed to check if old encrypted token parent directory %s is empty: %v", filepath.Dir(path), err)
		return
	}
	if !empty {
		return
	}
	if err := os.Remove(filepath.Dir(path)); err != nil {
		log.Warningf(context.Background(), "Failed to remove old encrypted token directory %s: %v", filepath.Dir(path), err)
	}

	// Check if the parent's parent directory is empty.
	empty, err = fileutils.IsDirEmpty(filepath.Dir(filepath.Dir(path)))
	if err != nil {
		log.Warningf(context.Background(), "Failed to check if old encrypted token parent directory %s is empty: %v", filepath.Dir(filepath.Dir(path)), err)
		return
	}
	if !empty {
		return
	}
	if err := os.Remove(filepath.Dir(filepath.Dir(path))); err != nil {
		log.Warningf(context.Background(), "Failed to remove old encrypted token parent directory %s: %v", filepath.Dir(filepath.Dir(path)), err)
	}
}
