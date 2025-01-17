package broker

import (
	"context"
	"crypto/rsa"
	"crypto/sha512"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/ubuntu/authd/log"
)

// decodeRawChallenge extract the base64 challenge and try to decrypt it with the private key.
func decodeRawChallenge(priv *rsa.PrivateKey, rawChallenge string) (decoded string, err error) {
	defer func() {
		// Override the error so that we don't leak information. Also, abstract it for the user.
		// We still log as error for the admin to get access.
		if err != nil {
			log.Errorf(context.Background(), "Error when decoding challenge: %v", err)
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
