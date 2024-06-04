package broker

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"fmt"

	"golang.org/x/crypto/scrypt"
	"golang.org/x/exp/slog"
)

const saltLen = 32

func encrypt(raw, key []byte) ([]byte, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}

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

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}

	ciphered := gcm.Seal(nonce, nonce, raw, nil)
	return append(ciphered, salt...), nil
}

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
