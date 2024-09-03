package broker

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rsa"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"

	"golang.org/x/crypto/scrypt"
)

const saltLen = 32

func decrypt(ciphered, key []byte) ([]byte, error) {
	salt, data := ciphered[len(ciphered)-saltLen:], ciphered[:len(ciphered)-saltLen]

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

	decrypted, err := gcm.Open(nil, data[:gcm.NonceSize()], data[gcm.NonceSize():], nil)
	if err != nil {
		return nil, err
	}

	return decrypted, nil
}

// decodeRawChallenge extract the base64 challenge and try to decrypt it with the private key.
func decodeRawChallenge(priv *rsa.PrivateKey, rawChallenge string) (decoded string, err error) {
	defer func() {
		// Override the error so that we don't leak information. Also, abstract it for the user.
		// We still log as error for the admin to get access.
		if err != nil {
			slog.Error(fmt.Sprintf("Error when decoding challenge: %v", err))
			err = errors.New("could not decode challenge")
		}
	}()

	if rawChallenge == "" {
		return "", nil
	}

	ciphertext, err := base64.StdEncoding.DecodeString(rawChallenge)
	if err != nil {
		return "", err
	}

	plaintext, err := rsa.DecryptOAEP(sha512.New(), nil, priv, ciphertext, nil)
	if err != nil {
		return "", fmt.Errorf("could not decrypt challenge: %v", err)
	}

	return string(plaintext), nil
}
