package token_test

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/ubuntu/authd-oidc-brokers/internal/token"
	"golang.org/x/crypto/scrypt"
)

func TestUseOldEncryptedToken(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		passwordFileExists          bool
		newTokenFileExists          bool
		oldEncryptedTokenFileExists bool

		wantRet   bool
		wantError bool
	}{
		"Success_when_both_the_password_file_and_the_new_token_file_exist": {passwordFileExists: true, newTokenFileExists: true, wantRet: false},
		"Success_when_old_encrypted_token_file_exists":                     {oldEncryptedTokenFileExists: true, wantRet: true, wantError: false},

		"Error_if_only_the_password_file_exists":                                     {passwordFileExists: true, wantRet: false, wantError: true},
		"Error_if_only_the_new_token_file_exists":                                    {newTokenFileExists: true, wantRet: false, wantError: true},
		"Error_if_neither_the_password_file_nor_the_old_encrypted_token_file_exists": {wantError: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			passwordPath := t.TempDir() + "/password"
			tokenPath := t.TempDir() + "/token"
			oldEncryptedTokenPath := t.TempDir() + "/oldtoken"

			if tc.passwordFileExists {
				err := os.WriteFile(passwordPath, []byte("password"), 0600)
				require.NoError(t, err, "WriteFile should not return an error")
			}
			if tc.newTokenFileExists {
				err := os.WriteFile(tokenPath, []byte("token"), 0600)
				require.NoError(t, err, "WriteFile should not return an error")
			}
			if tc.oldEncryptedTokenFileExists {
				err := os.WriteFile(oldEncryptedTokenPath, []byte("encryptedtoken"), 0600)
				require.NoError(t, err, "WriteFile should not return an error")
			}

			got, err := token.UseOldEncryptedToken(passwordPath, tokenPath, oldEncryptedTokenPath)
			if tc.wantError {
				require.Error(t, err, "UseOldEncryptedToken should return an error")
				return
			}
			require.NoError(t, err, "UseOldEncryptedToken should not return an error")
			require.Equal(t, tc.wantRet, got, "UseOldEncryptedToken should return the expected value")
		})
	}
}

func TestLoadOldEncryptedAuthInfo(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		noOldToken        bool
		invalidData       bool
		incorrectPassword bool

		wantToken token.AuthCachedInfo
		wantError bool
	}{
		"Successfully_load_old_encrypted_token": {wantToken: testToken, wantError: false},
		"Error_when_file_does_not_exist":        {noOldToken: true, wantError: true},
		"Error_when_file_contains_invalid_data": {invalidData: true, wantError: true},
		"Error_when_password_is_incorrect":      {incorrectPassword: true, wantError: true},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			tokenPath := t.TempDir() + "/oldtoken"

			enteredPassword := "password"
			oldPassword := enteredPassword
			if tc.incorrectPassword {
				oldPassword = "wrongpassword"
			}

			if !tc.noOldToken {
				jsonData, err := json.Marshal(testToken)
				require.NoError(t, err, "Marshal should not return an error")
				encrypted, err := encrypt(jsonData, []byte(oldPassword))
				require.NoError(t, err, "encrypt should not return an error")
				err = os.WriteFile(tokenPath, encrypted, 0600)
				require.NoError(t, err, "WriteFile should not return an error")
			}

			if tc.invalidData {
				err := os.WriteFile(tokenPath, []byte("invalid data"), 0600)
				require.NoError(t, err, "WriteFile should not return an error")
			}

			got, err := token.LoadOldEncryptedAuthInfo(tokenPath, enteredPassword)
			if tc.wantError {
				require.Error(t, err, "LoadOldEncryptedAuthInfo should return an error")
				return
			}
			require.NoError(t, err, "LoadOldEncryptedAuthInfo should not return an error")
			require.Equal(t, tc.wantToken, got, "LoadOldEncryptedAuthInfo should return the expected value")
		})
	}
}

func encrypt(data, key []byte) ([]byte, error) {
	// Generate a random salt
	salt := make([]byte, token.Saltlen())
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("could not generate salt: %v", err)
	}

	// Derive a key from the password using the salt
	derivedKey, err := scrypt.Key(key, salt, 32768, 8, 1, 32)
	if err != nil {
		return nil, fmt.Errorf("could not derive key: %v", err)
	}

	// Create a new AES cipher block
	block, err := aes.NewCipher(derivedKey)
	if err != nil {
		return nil, fmt.Errorf("could not create cipher block: %v", err)
	}

	// Create a GCM cipher
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("could not create GCM cipher: %v", err)
	}

	// Generate a random nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, fmt.Errorf("could not generate nonce: %v", err)
	}

	// Encrypt the data
	ciphertext := gcm.Seal(nonce, nonce, data, nil)

	// Concatenate the nonce, encrypted data, and salt
	result := append(ciphertext, salt...)

	return result, nil
}
